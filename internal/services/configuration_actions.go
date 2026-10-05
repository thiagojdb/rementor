package services

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/thiagojdb/rementor/internal/fleet"
	"github.com/thiagojdb/rementor/internal/models"
)

// SourceRegistrationPorts reports metadata from the cached source snapshot.
// These ports belong to the source; they must never become receiver registrations.
func (r *Registry) SourceRegistrationPorts() map[string]map[string]int {
	r.workspacesMu.RLock()
	raw := r.computerFollowing.Snapshot
	managed := map[string]bool{}
	for id := range r.computerFollowing.Originals {
		managed[id] = true
	}
	r.workspacesMu.RUnlock()
	// Stored snapshots are immutable and already validated at their commit boundary.
	var snapshot fleet.ConfigurationSnapshot
	if len(managed) == 0 || json.Unmarshal(raw, &snapshot) != nil {
		return nil
	}
	result := map[string]map[string]int{}
	for _, env := range snapshot.Environments {
		id := env.Snapshot.Intent.Scope.WorkspaceID
		if !managed[id] {
			continue
		}
		ports := map[string]int{}
		for _, app := range env.Snapshot.Intent.Applications {
			ports[app.CanonicalAppID()] = app.Port
		}
		result[id] = ports
	}
	return result
}

func applySourceConfigurationAction(ws *models.Workspace, edit fleet.ConfigurationEdit) (bool, error) {
	if edit.Action == "update-routing" {
		if ws.IsLocalApps() {
			return false, errors.New("local-app environments have no routing settings")
		}
		next := *edit.Routing
		changed := !reflect.DeepEqual(ws.RoutingConfig, &next)
		ws.RoutingConfig = &next
		return changed, nil
	}
	apps := applicationConfigs(ws.Applications)
	index := -1
	for i, app := range apps {
		if app.CanonicalAppID() == edit.ApplicationID {
			index = i
			break
		}
	}
	switch edit.Action {
	case "delete-application":
		if index < 0 {
			return false, errors.New("application not found")
		}
		apps = append(apps[:index], apps[index+1:]...)
	case "register-port":
		if index < 0 {
			return false, errors.New("application not found")
		}
		apps[index].Port = *edit.RegistrationPort
		apps[index].PeerEndpoint = nil
	case "upsert-application":
		next := *edit.Application
		if index >= 0 {
			prior := apps[index]
			next.Active, next.RoutePattern, next.StripOrigin = prior.Active, cloneStringPtr(prior.RoutePattern), prior.StripOrigin
			next.Port, next.PeerEndpoint = prior.Port, prior.PeerEndpoint.Clone()
		}
		if edit.RegistrationPort != nil {
			next.Port = *edit.RegistrationPort
			next.PeerEndpoint = nil
		}
		if index >= 0 {
			apps[index] = next
		} else {
			apps = append(apps, next)
		}
	default:
		return false, errors.New("unknown configuration action")
	}
	if reflect.DeepEqual(apps, applicationConfigs(ws.Applications)) {
		return false, nil
	}
	ws.Applications = applicationsFromConfigs(ws, apps)
	ws.SetDefaults()
	for _, app := range ws.Applications {
		app.InitializeRuntime()
	}
	return true, nil
}

func (r *Registry) deletePeerEnvironment(ctx context.Context, edit fleet.ConfigurationEdit) error {
	_, err := r.mutate(true, func(candidate *[]*models.Workspace) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		ws := findWorkspace(*candidate, edit.WorkspaceID)
		if ws == nil {
			return fleet.ErrWorkspaceNotFound
		}
		if ws.Route.RouteVersion != edit.ExpectedVersion {
			return fleet.ErrConfigurationConflict
		}
		if ws.Session != nil || ws.Inheritance != nil || r.IsComputerFollowed(ws.WorkspaceID) {
			return errors.New("configuration changed; reload the environment")
		}
		for _, child := range *candidate {
			if child.Session != nil && child.Session.EnvironmentID == ws.WorkspaceID {
				return errors.New("close routing sessions before deleting the environment")
			}
		}
		for i, item := range *candidate {
			if item.WorkspaceID == ws.WorkspaceID {
				*candidate = append((*candidate)[:i], (*candidate)[i+1:]...)
				break
			}
		}
		return nil
	}, r.workspaceStore().ReplaceWorkspaces)
	return err
}
