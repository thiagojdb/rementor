package services

import (
	"github.com/thiagojdb/rementor/internal/fleet"
	"github.com/thiagojdb/rementor/internal/models"
)

func peerScope(workspace *models.Workspace) fleet.Scope {
	return fleet.Scope{WorkspaceID: workspace.WorkspaceID, EnvironmentID: workspace.EnvironmentID(), SessionID: workspace.SessionID()}
}

func (r *Registry) ExportPeerWorkspaces() []fleet.WorkspaceSummary {
	workspaces := r.workspaceSnapshot()
	result := make([]fleet.WorkspaceSummary, 0, len(workspaces))
	for _, workspace := range workspaces {
		result = append(result, fleet.WorkspaceSummary{Scope: peerScope(workspace), Name: workspace.NameOrID(), Type: workspace.GetType(), Version: workspace.Route.RouteVersion})
	}
	return result
}

func (r *Registry) ExportPeerWorkspace(id string) (fleet.WorkspaceIntent, uint64, error) {
	workspace := findWorkspace(r.workspaceSnapshot(), id)
	if workspace == nil {
		return fleet.WorkspaceIntent{}, 0, fleet.ErrWorkspaceNotFound
	}
	intent, err := peerIntent(workspace)
	return intent, workspace.Route.RouteVersion, err
}
func peerIntent(workspace *models.Workspace) (fleet.WorkspaceIntent, error) {
	intent := fleet.WorkspaceIntent{Scope: peerScope(workspace), Name: workspace.NameOrID(), Type: workspace.GetType(), Applications: applicationConfigs(workspace.Applications)}
	if workspace.RoutingConfig != nil {
		intent.Routing = *workspace.RoutingConfig
	}
	if workspace.Inheritance != nil {
		cached, err := fleet.CachedSnapshot(workspace.Inheritance)
		if err != nil {
			return fleet.WorkspaceIntent{}, err
		}
		intent.Parents = append([]models.PeerWorkspaceReference{fleet.SnapshotReference(cached)}, cached.Intent.Parents...)
	}
	// Resolve legacy semantics here so JSON encoding cannot discard the legacy
	// flags and cause a receiving instance to interpret a different route.
	for index, app := range workspace.Applications {
		binding := &intent.Applications[index]
		binding.Path = app.IngressPath()
		binding.PublicPath = binding.Path
		binding.Context = app.BackendContextPath()
		binding.UpstreamContext = binding.Context
		binding.LegacyPublicPath = false
		binding.LegacyUpstreamContext = false
	}
	return intent, intent.Validate()
}
