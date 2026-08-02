package services

import (
	"errors"
	"sync"
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

func TestRouteApplyGeneratedPlanReplayUsesStableIdempotencyFingerprint(t *testing.T) {
	provider := &mockRoutingProvider{}
	r := routeTestRegistry(provider)
	first, err := r.PlanRoute("demo", "orders", "local", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ApplyRoutePlan("demo", first, first.BaseRouteVersion, "stable-key", "corr-1"); err != nil {
		t.Fatal(err)
	}

	// A server-side request without an explicit plan is re-planned on every
	// retry. Its base version and route snapshots have changed, but the
	// idempotency key still identifies the same intended local route.
	second, err := r.PlanRoute("demo", "orders", "local", nil)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := r.ApplyRoutePlan("demo", second, second.BaseRouteVersion, "stable-key", "corr-2")
	if err != nil {
		t.Fatal(err)
	}
	if replay.Changed || replay.Status != "idempotent-replay" {
		t.Fatalf("replay = %#v, want unchanged idempotent replay", replay)
	}
	if len(provider.snapshots) != 1 {
		t.Fatalf("idempotent replay reloaded proxy %d times", len(provider.snapshots))
	}
}

func TestRouteApplyRejectsStaleExpectedVersionEvenWhenDesiredStateMatches(t *testing.T) {
	provider := &mockRoutingProvider{}
	r := routeTestRegistry(provider)
	plan, err := r.PlanRoute("demo", "orders", "local", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ApplyRoutePlan("demo", plan, plan.BaseRouteVersion, "first-key", "corr-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ApplyRoutePlan("demo", plan, plan.BaseRouteVersion, "different-key", "corr-2"); !errors.Is(err, ErrRouteVersionConflict) {
		t.Fatalf("stale same-state error = %v, want route version conflict", err)
	}
	if len(provider.snapshots) != 1 {
		t.Fatalf("stale request touched proxy %d times", len(provider.snapshots))
	}
}

func TestRouteApplySerializesConcurrentIdempotentCallers(t *testing.T) {
	provider := &mockRoutingProvider{}
	r := routeTestRegistry(provider)
	plan, err := r.PlanRoute("demo", "orders", "local", nil)
	if err != nil {
		t.Fatal(err)
	}

	const callers = 16
	results := make(chan RouteApplyResult, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, applyErr := r.ApplyRoutePlan("demo", plan, plan.BaseRouteVersion, "concurrent-key", "corr-concurrent")
			results <- result
			errs <- applyErr
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	changed := 0
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent apply failed: %v", err)
		}
	}
	for result := range results {
		if result.Changed {
			changed++
		}
	}
	if changed != 1 {
		t.Fatalf("changed results = %d, want exactly one", changed)
	}
	if len(provider.snapshots) != 1 {
		t.Fatalf("concurrent callers reloaded proxy %d times", len(provider.snapshots))
	}
}
