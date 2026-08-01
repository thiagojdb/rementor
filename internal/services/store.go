package services

import (
	"github.com/thiagojdb/rementor/internal/config"
	"github.com/thiagojdb/rementor/internal/models"
)

// WorkspaceStore owns durable workspace configuration and route state.
// Registry keeps an in-memory runtime projection of this data for routing,
// health checks, and streaming updates.
type WorkspaceStore interface {
	LoadWorkspaces() ([]*models.Workspace, error)
	LoadState([]*models.Workspace) error
	SaveState([]*models.Workspace) error
	ReplaceWorkspaces([]*models.Workspace) error
	WorkspaceFromConfig(models.WorkspaceConfig) *models.Workspace
}

type configWorkspaceStore struct{}

func NewConfigWorkspaceStore() WorkspaceStore {
	return configWorkspaceStore{}
}

func (configWorkspaceStore) LoadWorkspaces() ([]*models.Workspace, error) {
	return config.LoadWorkspaces()
}

func (configWorkspaceStore) LoadState(workspaces []*models.Workspace) error {
	return config.LoadState(workspaces)
}

func (configWorkspaceStore) SaveState(workspaces []*models.Workspace) error {
	return config.SaveState(workspaces)
}

func (configWorkspaceStore) ReplaceWorkspaces(workspaces []*models.Workspace) error {
	return config.ReplaceWorkspaces(workspaces)
}

func (configWorkspaceStore) WorkspaceFromConfig(ws models.WorkspaceConfig) *models.Workspace {
	return config.WorkspaceFromConfig(ws)
}
