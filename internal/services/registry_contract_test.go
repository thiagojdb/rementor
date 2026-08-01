package services

import (
	"testing"

	"github.com/thiagojdb/rementor/internal/models"
)

func TestRouteMutationReturnsMonotonicOperationMetadata(t *testing.T) {
	ws := &models.Workspace{
		WorkspaceID: "demo",
		Type:        models.WorkspaceTypeRouting,
		RoutingConfig: &models.RoutingConfig{
			LocalDomain: "api.localhost",
		},
		Applications: []*models.Application{{ID: "orders", Path: "/orders", Port: 8080}},
	}
	r := &Registry{
		workspaces:      []*models.Workspace{ws},
		store:           &fakeWorkspaceStore{},
		routingProvider: &mockRoutingProvider{},
		stopChan:        make(chan struct{}),
		subscribers:     make(map[string]int),
		healthStreams:   make(map[uint64]healthStream),
	}

	app, first, err := r.ToggleAppWithMetadata("demo", "orders", "corr-first")
	if err != nil {
		t.Fatalf("first toggle failed: %v", err)
	}
	if app == nil || first == nil || first.OperationID == "" || first.CorrelationID != "corr-first" || first.RouteVersion != 1 {
		t.Fatalf("unexpected first operation: app=%#v operation=%#v", app, first)
	}
	if app.Route.RouteVersion != first.RouteVersion || app.Route.OperationID != first.OperationID {
		t.Fatalf("application route metadata not updated: %#v", app.Route)
	}

	_, second, err := r.UpdateRoutePatternWithMetadata("demo", "orders", stringPtrForTest("/orders/*"), "corr-second")
	if err != nil {
		t.Fatalf("route pattern update failed: %v", err)
	}
	if second == nil || second.RouteVersion <= first.RouteVersion || second.CorrelationID != "corr-second" {
		t.Fatalf("unexpected second operation: %#v", second)
	}
}

func stringPtrForTest(value string) *string { return &value }
