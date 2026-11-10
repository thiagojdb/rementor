package services

import (
	"strings"
	"testing"

	"github.com/thiagojdb/rementor/internal/fleet"
	"github.com/thiagojdb/rementor/internal/models"
)

func TestPeerSnapshotChangesOnlyWithCommittedIntent(t *testing.T) {
	workspace := &models.Workspace{WorkspaceID: "dev", Type: models.WorkspaceTypeRouting, RoutingConfig: &models.RoutingConfig{LocalDomain: "dev.localhost", DefaultRemoteBaseURL: "https://remote.example.test"}, Applications: []*models.Application{{ID: "orders", Path: "/orders", Port: 8080}}}
	registry := &Registry{workspaces: []*models.Workspace{workspace}, store: &fakeWorkspaceStore{}, routingProvider: &mockRoutingProvider{}}
	owner := strings.Repeat("a", 64)
	export := func() fleet.Snapshot {
		t.Helper()
		intent, version, err := registry.ExportPeerWorkspace("dev")
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := fleet.NewSnapshot(owner, version, intent)
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	before := export()
	workspace.Applications[0].Runtime.SetHealthOk(true)
	if export().Digest != before.Digest {
		t.Fatal("health observations changed routing intent")
	}
	if _, _, err := registry.ToggleAppWithMetadata("dev", "orders", "peer-export"); err != nil {
		t.Fatal(err)
	}
	after := export()
	if after.Version <= before.Version || after.Digest == before.Digest || !after.Intent.Applications[0].Active {
		t.Fatal("committed route change was not reflected in peer snapshot")
	}
}

func TestExportPeerIntentIsDetachedAndPreservesScopeAndLegacyPaths(t *testing.T) {
	app := &models.Application{ID: "Orders_API", AppID: "orders-api", Name: "Orders", Path: "/old", PublicPath: "/old", Context: "/orders", UpstreamContext: "/orders", LegacyPublicPath: true, LegacyUpstreamContext: true, Port: 8080, Active: true, Route: models.RouteState{RouteVersion: 3}, LastOperation: &models.OperationMetadata{OperationID: "private-operation"}}
	workspace := &models.Workspace{WorkspaceID: "feature-123", Session: &models.RoutingSession{EnvironmentID: "dev", HostPrefix: "feature", Baseline: []models.ApplicationConfig{{ID: "secret-baseline"}}}, Type: models.WorkspaceTypeRouting, RoutingConfig: &models.RoutingConfig{LocalDomain: "feature.dev.localhost"}, Applications: []*models.Application{app}, Route: models.RouteState{RouteVersion: 7}}
	registry := &Registry{workspaces: []*models.Workspace{workspace}}
	intent, version, err := registry.ExportPeerWorkspace(workspace.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if version != 7 || intent.Scope.SessionID != workspace.WorkspaceID || intent.Scope.EnvironmentID != "dev" || intent.Applications[0].PublicPath != "/orders" || !intent.Applications[0].Active {
		t.Fatalf("incorrect exported intent: %#v", intent)
	}
	if intent.Applications[0].LastOperation != nil || intent.Applications[0].Route != (models.RouteState{}) {
		t.Fatal("export leaked runtime state")
	}
	intent.Applications[0].Port = 9999
	if app.Port != 8080 {
		t.Fatal("export shares mutable state with registry")
	}
	catalog := registry.ExportPeerWorkspaces()
	if len(catalog) != 1 || catalog[0].Scope != intent.Scope || catalog[0].Version != 7 {
		t.Fatal("catalog does not identify the selected session")
	}
	if _, _, err := registry.ExportPeerWorkspace("missing"); err != fleet.ErrWorkspaceNotFound {
		t.Fatalf("unknown workspace was not rejected: %v", err)
	}
}
