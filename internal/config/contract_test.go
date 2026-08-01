package config

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/thiagojdb/rementor/internal/models"
)

func TestRouteOperationMetadataPersistsWithLegacyWorkspaceState(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))

	if err := AppendWorkspace(models.WorkspaceConfig{
		ID: "dev", Type: models.WorkspaceTypeRouting,
		Routing:      models.RoutingConfig{LocalDomain: "dev.localhost"},
		Applications: []models.ApplicationConfig{{ID: "orders", Path: "/orders", Port: 8080}},
	}); err != nil {
		t.Fatalf("append workspace: %v", err)
	}
	workspaces, err := LoadWorkspaces()
	if err != nil {
		t.Fatalf("load workspaces: %v", err)
	}
	when := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	operation := &models.OperationMetadata{OperationID: "op-1", CorrelationID: "corr-1", RouteVersion: 1, Kind: "toggle", CreatedAt: when, CompletedAt: when.Add(time.Millisecond)}
	workspaces[0].Route.RouteVersion = 1
	workspaces[0].Route.OperationID = operation.OperationID
	workspaces[0].LastOperation = operation
	app := workspaces[0].Applications[0]
	app.Route.RouteVersion = 1
	app.Route.OperationID = operation.OperationID
	app.LastOperation = operation
	app.Active = true
	if err := SaveState(workspaces); err != nil {
		t.Fatalf("save state: %v", err)
	}

	reloaded, err := LoadWorkspaces()
	if err != nil {
		t.Fatalf("reload workspaces: %v", err)
	}
	loadedState := make([]*models.Workspace, len(reloaded))
	copy(loadedState, reloaded)
	if err := LoadState(loadedState); err != nil {
		t.Fatalf("load route state: %v", err)
	}
	if got := loadedState[0].LastOperation; got == nil || got.OperationID != operation.OperationID || got.CorrelationID != operation.CorrelationID {
		t.Fatalf("workspace operation metadata did not persist: %#v", got)
	}
	if got := loadedState[0].Applications[0].LastOperation; got == nil || got.OperationID != operation.OperationID || got.RouteVersion != 1 {
		t.Fatalf("application operation metadata did not persist: %#v", got)
	}
}
