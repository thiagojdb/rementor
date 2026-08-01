package services

import (
	"errors"
	"testing"

	"github.com/thiagojdb/rementor/internal/models"
)

func routeTestRegistry(provider *mockRoutingProvider) *Registry {
	ws := &models.Workspace{
		WorkspaceID: "demo",
		Type:        models.WorkspaceTypeRouting,
		RoutingConfig: &models.RoutingConfig{
			LocalDomain:          "api.localhost",
			DefaultRemoteBaseURL: "https://remote.example.test",
		},
		Applications: []*models.Application{
			{ID: "orders", AppID: "orders", ServiceID: "orders-service", Path: "/orders", Port: 1901},
			{ID: "portal", AppID: "portal", Path: "/", Context: "/portal", Port: 1902, RemoteBaseUrl: "https://portal.example.test"},
		},
	}
	ws.SetDefaults()
	return &Registry{
		workspaces:      []*models.Workspace{ws},
		store:           &fakeWorkspaceStore{},
		routingProvider: provider,
		stopChan:        make(chan struct{}),
		subscribers:     make(map[string]int),
		healthStreams:   make(map[uint64]healthStream),
	}
}

func TestRouteResolutionUsesExactAndLongestPrefixPrecedence(t *testing.T) {
	r := routeTestRegistry(&mockRoutingProvider{})
	resolution, err := r.ResolveRoute("demo", "api.localhost", "/orders/123")
	if err != nil {
		t.Fatal(err)
	}
	if resolution.CanonicalAppID != "orders" {
		t.Fatalf("resolved app = %q, want orders", resolution.CanonicalAppID)
	}
	if resolution.MatchingPattern != "/orders/*" {
		t.Fatalf("matching pattern = %q", resolution.MatchingPattern)
	}

	resolution, err = r.ResolveRoute("demo", "api.localhost", "/portal/home")
	if err != nil {
		t.Fatal(err)
	}
	if resolution.CanonicalAppID != "portal" {
		t.Fatalf("resolved root app = %q, want portal", resolution.CanonicalAppID)
	}
	if resolution.MatchingPattern != "/portal/*" {
		t.Fatalf("matching context pattern = %q, want /portal/*", resolution.MatchingPattern)
	}
	resolution, err = r.ResolveRoute("demo", "api.localhost", "/")
	if err != nil {
		t.Fatal(err)
	}
	if resolution.MatchingPattern != "/" || !resolution.Route.Exact {
		t.Fatalf("root resolution = %#v, want exact root route", resolution)
	}
}

func TestRouteApplyIsNoOpAndRejectsDifferentStalePlan(t *testing.T) {
	provider := &mockRoutingProvider{}
	r := routeTestRegistry(provider)
	plan, err := r.PlanRoute("demo", "orders", "local", nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.ApplyRoutePlan("demo", plan, plan.BaseRouteVersion, "orders-local", "corr-1")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Operation == nil {
		t.Fatalf("first apply = %#v", result)
	}
	if len(provider.snapshots) != 1 {
		t.Fatalf("proxy reload count = %d, want 1", len(provider.snapshots))
	}
	result, err = r.ApplyRoutePlan("demo", plan, plan.BaseRouteVersion, "orders-local", "corr-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed {
		t.Fatal("replaying the same idempotency key changed the route")
	}
	if len(provider.snapshots) != 1 {
		t.Fatalf("replayed apply reloaded proxy %d times", len(provider.snapshots))
	}

	remotePlan, err := r.PlanRoute("demo", "orders", "remote", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ApplyRoutePlan("demo", remotePlan, plan.BaseRouteVersion, "", "corr-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ApplyRoutePlan("demo", plan, plan.BaseRouteVersion, "different-key", "corr-3"); !errors.Is(err, ErrRouteVersionConflict) {
		t.Fatalf("stale plan error = %v, want route version conflict", err)
	}
}
