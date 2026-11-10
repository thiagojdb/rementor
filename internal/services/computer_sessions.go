package services

import (
	"fmt"

	"github.com/thiagojdb/rementor/internal/fleet"
	"github.com/thiagojdb/rementor/internal/models"
)

// Session membership is committed in the same transaction as its environment.
// An independently created receiver session is never replaced or rebased.
func (r *Registry) projectComputerSessions(owner, peer string, snapshot fleet.ConfigurationSnapshot, previous, next []*models.Workspace, state *models.ComputerFollowing) ([]*models.Workspace, error) {
	if state.SessionIDs == nil {
		state.SessionIDs = map[string]string{}
	}
	active := map[string]bool{}
	present := map[string]bool{}
	for _, session := range snapshot.Sessions {
		id := session.Snapshot.Intent.Scope.WorkspaceID
		present[id] = true
		active[id] = !state.Excluded[session.Session.EnvironmentID]
	}
	without := func(id string) {
		filtered := make([]*models.Workspace, 0, len(next))
		for _, ws := range next {
			if ws.WorkspaceID != id {
				filtered = append(filtered, ws)
			}
		}
		next = filtered
	}
	for sourceID, localID := range state.SessionIDs {
		if active[sourceID] {
			continue
		}
		if _, managed := state.Originals[localID]; managed {
			without(localID)
			delete(state.Originals, localID)
		}
		if !present[sourceID] {
			delete(state.SessionIDs, sourceID)
		}
	}
	for _, entry := range snapshot.Sessions {
		source := entry.Snapshot
		sourceID := source.Intent.Scope.WorkspaceID
		if !active[sourceID] {
			continue
		}
		localID := state.SessionIDs[sourceID]
		if localID == "" {
			// Reuse individually imported sessions so existing browser URLs and
			// explicit session selectors survive upgrading computer following.
			for _, ws := range previous {
				if ws.Session != nil && ws.Inheritance != nil && ws.Inheritance.Source.OwnerFingerprint == peer && ws.Inheritance.Source.Scope == source.Intent.Scope {
					if localID != "" {
						return nil, fmt.Errorf("multiple local sessions follow source session %s", sourceID)
					}
					if len(ws.Inheritance.Overrides) != 0 {
						return nil, fmt.Errorf("session %s has local overrides; reset them before following its computer", ws.WorkspaceID)
					}
					localID = ws.WorkspaceID
				}
			}
			if localID == "" {
				localID = sourceID
				if findWorkspace(previous, localID) != nil {
					return nil, fmt.Errorf("source session %s conflicts with a local workspace", sourceID)
				}
			}
		}
		prior := findWorkspace(previous, localID)
		if prior != nil && (prior.Session == nil || prior.Session.EnvironmentID != entry.Session.EnvironmentID) {
			return nil, fmt.Errorf("source session %s conflicts with local session membership", sourceID)
		}
		_, managed := state.Originals[localID]
		if !managed {
			state.Originals[localID] = nil
			if prior != nil {
				state.Originals[localID] = cloneWorkspace(prior)
			}
		}
		workspace, err := r.projectComputerWorkspace(owner, entry.ConfigurationEnvironment, localID, &entry.Session, prior, managed)
		if err != nil {
			return nil, fmt.Errorf("follow session %s: %w", sourceID, err)
		}
		without(localID)
		next = append(next, workspace)
		state.SessionIDs[sourceID] = localID
	}
	return next, nil
}
