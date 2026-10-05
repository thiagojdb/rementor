package fleet

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/thiagojdb/rementor/internal/models"
	"github.com/thiagojdb/rementor/internal/validation"
)

func SnapshotReference(snapshot Snapshot) models.PeerWorkspaceReference {
	return models.PeerWorkspaceReference{OwnerFingerprint: snapshot.OwnerFingerprint, Scope: snapshot.Intent.Scope, Version: snapshot.Version, Digest: snapshot.Digest}
}

func CachedSnapshot(state *models.WorkspaceInheritance) (Snapshot, error) {
	var snapshot Snapshot
	if state == nil || state.SchemaVersion != SchemaVersion || len(state.Snapshot) == 0 || len(state.Snapshot) > MaxSnapshotBytes {
		return snapshot, errors.New("invalid inheritance cache schema or size")
	}
	decoder := json.NewDecoder(bytes.NewReader(state.Snapshot))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return snapshot, fmt.Errorf("read inheritance cache: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return snapshot, errors.New("unexpected inheritance cache data")
	}
	if err := snapshot.Validate(state.Source.OwnerFingerprint, state.Source.Scope.WorkspaceID); err != nil {
		return snapshot, err
	}
	if SnapshotReference(snapshot) != state.Source {
		return snapshot, errors.New("inheritance source does not match its cached snapshot")
	}
	return snapshot, nil
}

// ProjectInheritance creates a detached effective workspace. A source port is
// always converted to its owner reference, including an inactive registration.
// Receiver hostnames remain stable; source route paths and selections follow the
// peer. Overrides are explicit, and dropping one resumes the cached selection.
func ProjectInheritance(localOwner string, localScope Scope, base models.WorkspaceConfig, snapshot Snapshot, now time.Time) (models.WorkspaceConfig, error) {
	if !validDigest(localOwner) {
		return base, errors.New("invalid local computer identity")
	}
	if err := ValidateScope(localScope); err != nil {
		return base, err
	}
	if err := snapshot.Validate(snapshot.OwnerFingerprint, snapshot.Intent.Scope.WorkspaceID); err != nil {
		return base, err
	}
	if err := checkInheritanceCycle(localOwner, localScope, snapshot); err != nil {
		return base, err
	}
	workspaceType := base.Type
	if workspaceType == "" {
		workspaceType = models.WorkspaceTypeRouting
	}
	if workspaceType != snapshot.Intent.Type {
		return base, errors.New("source and receiver must have the same workspace type")
	}
	state := base.Inheritance.Clone()
	if state == nil {
		state = &models.WorkspaceInheritance{SchemaVersion: SchemaVersion, OriginalRouting: base.Routing, OriginalApplications: models.CloneApplicationConfigs(base.Applications), Overrides: map[string]models.RouteSelection{}}
	} else {
		prior, err := CachedSnapshot(state)
		if err != nil {
			return base, err
		}
		if prior.OwnerFingerprint != snapshot.OwnerFingerprint || prior.Intent.Scope != snapshot.Intent.Scope {
			return base, errors.New("stop following the current source before choosing another")
		}
		if snapshot.Version < prior.Version || (snapshot.Version == prior.Version && snapshot.Digest != prior.Digest) {
			return base, errors.New("source snapshot version regressed or changed without a new version")
		}
	}
	state.Source = SnapshotReference(snapshot)
	state.Snapshot, _ = json.Marshal(snapshot)
	state.UpdatedAt = now.UTC()
	apps := models.CloneApplicationConfigs(snapshot.Intent.Applications)
	hosts := map[string]string{}
	for _, app := range state.OriginalApplications {
		hosts[app.ID] = app.Domain
	}
	seen := map[string]bool{}
	for i := range apps {
		app := &apps[i]
		seen[app.ID] = true
		if host, exists := hosts[app.ID]; exists {
			app.Domain = host
		}
		if app.Port > 0 {
			endpoint := snapshot.Endpoint(app.ID)
			app.PeerEndpoint = &endpoint
			app.Port = 0
		}
		if selection, exists := state.Overrides[app.ID]; exists {
			if err := ApplySelection(app, selection); err != nil {
				return base, fmt.Errorf("override %s: %w", app.ID, err)
			}
		}
	}
	for id := range state.Overrides {
		if !seen[id] {
			return base, fmt.Errorf("source removed overridden application %q; reset its override before synchronizing", id)
		}
	}
	out := base
	out.Inheritance = state
	out.Applications = apps
	out.Routing = snapshot.Intent.Routing
	out.Routing.LocalDomain = state.OriginalRouting.LocalDomain
	if err := validation.Workspace(workspaceType, out.Routing.LocalDomain, out.Routing.DefaultRemoteBaseURL, out.Applications); err != nil {
		return base, err
	}
	return out, nil
}

func ApplySelection(app *models.ApplicationConfig, selection models.RouteSelection) error {
	if selection.RoutePattern != nil {
		pattern := *selection.RoutePattern
		if pattern == "" {
			app.RoutePattern = nil
		} else {
			app.RoutePattern = &pattern
		}
	}
	switch selection.Mode {
	case "inherit":
		if selection.Port != 0 || selection.RemoteBaseURL != "" {
			return errors.New("inherited selection cannot include an explicit endpoint")
		}
	case models.RouteModeLocal:
		if selection.Port < 0 || selection.Port > 65535 || (selection.Port == 0 && app.Port == 0 && app.PeerEndpoint == nil) {
			return errors.New("choose a local port between 1 and 65535")
		}
		app.Active = true
		if selection.Port > 0 {
			app.Port = selection.Port
			app.PeerEndpoint = nil
		}
	case models.RouteModeRemote:
		if selection.Port < 0 || selection.Port > 65535 {
			return errors.New("local registration port must be between 1 and 65535")
		}
		app.Active = false
		if selection.Port > 0 {
			app.Port = selection.Port
			app.PeerEndpoint = nil
		}
	default:
		return errors.New("choose local or remote, or reset to inherit")
	}
	if selection.RemoteBaseURL != "" {
		app.RemoteBaseUrl = selection.RemoteBaseURL
	}
	return nil
}

func checkInheritanceCycle(localOwner string, localScope Scope, snapshot Snapshot) error {
	for _, ref := range append([]models.PeerWorkspaceReference{SnapshotReference(snapshot)}, snapshot.Intent.Parents...) {
		if ref.OwnerFingerprint == localOwner && ref.Scope.WorkspaceID == localScope.WorkspaceID {
			return errors.New("environment inheritance cycle")
		}
	}
	return nil
}

func ValidateInheritance(localOwner string, localScope Scope, state *models.WorkspaceInheritance) error {
	if state == nil {
		return nil
	}
	snapshot, err := CachedSnapshot(state)
	if err != nil {
		return err
	}
	if localOwner != "" {
		if err := checkInheritanceCycle(localOwner, localScope, snapshot); err != nil {
			return err
		}
	}
	if err := validation.Workspace(snapshot.Intent.Type, state.OriginalRouting.LocalDomain, state.OriginalRouting.DefaultRemoteBaseURL, state.OriginalApplications); err != nil {
		return fmt.Errorf("invalid original workspace: %w", err)
	}
	for id, selection := range state.Overrides {
		found := false
		for _, source := range snapshot.Intent.Applications {
			if source.ID != id {
				continue
			}
			found = true
			if err := ApplySelection(&source, selection); err != nil {
				return err
			}
			if err := validation.Application(snapshot.Intent.Type, source); err != nil {
				return err
			}
		}
		if !found {
			return fmt.Errorf("override references unknown application %q", id)
		}
	}
	return nil
}
