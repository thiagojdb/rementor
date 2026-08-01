package services

import (
	"errors"
	"reflect"
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

func TestRouteConflictsFollowNginxPrecedenceAndIgnoreHostBoundaries(t *testing.T) {
	routes := []Route{
		{WorkspaceID: "demo", Environment: "demo", PublicHost: "api.localhost", Pattern: "/*", CanonicalAppID: "fallback", ServiceID: "frontend", Precedence: 0, PrecedenceReason: "root fallback"},
		{WorkspaceID: "demo", Environment: "demo", PublicHost: "api.localhost", Pattern: "/orders/*", CanonicalAppID: "orders", ServiceID: "orders-svc", Precedence: 7, PrecedenceReason: "longest prefix"},
		{WorkspaceID: "demo", Environment: "demo", PublicHost: "api.localhost", Pattern: "/orders/health", CanonicalAppID: "health", ServiceID: "health-svc", Precedence: 100000 + 14, PrecedenceReason: "exact match", Exact: true},
		{WorkspaceID: "demo", Environment: "demo", PublicHost: "other.localhost", Pattern: "/*", CanonicalAppID: "other", ServiceID: "other-svc", Precedence: 0, PrecedenceReason: "root fallback"},
	}
	conflicts := conflictsForRoutes(routes)
	if len(conflicts) != 3 {
		t.Fatalf("conflict count = %d, want 3 (host boundary and exact/prefix semantics)", len(conflicts))
	}
	for _, conflict := range conflicts {
		if conflict.PublicHost != "api.localhost" {
			t.Fatalf("reported cross-host conflict: %#v", conflict)
		}
	}
	if conflicts[0].Reason != "narrower route shadows broader route" || conflicts[1].Reason != "narrower route shadows broader route" {
		t.Fatalf("conflict reasons = %#v", conflicts)
	}
	var foundExact, foundRoot bool
	for _, conflict := range conflicts {
		if conflict.WinningAppID == "health" {
			foundExact = true
			if conflict.ShadowedAppID == "fallback" {
				foundRoot = true
			}
		}
	}
	if !foundExact || !foundRoot {
		t.Fatalf("exact route conflicts were not reported correctly: %#v", conflicts)
	}
}

func TestRouteConflictsDetectNestedPrefixesAndDuplicateOwnershipDeterministically(t *testing.T) {
	routes := []Route{
		{WorkspaceID: "demo", Environment: "demo", PublicHost: "api.localhost", Pattern: "/api/*", CanonicalAppID: "zeta", ServiceID: "svc-z", Precedence: 4, PrecedenceReason: "longest prefix"},
		{WorkspaceID: "demo", Environment: "demo", PublicHost: "api.localhost", Pattern: "/api/*", CanonicalAppID: "alpha", ServiceID: "svc-a", Precedence: 4, PrecedenceReason: "longest prefix"},
		{WorkspaceID: "demo", Environment: "demo", PublicHost: "api.localhost", Pattern: "/api/admin/*", CanonicalAppID: "admin", ServiceID: "svc-admin", Precedence: 10, PrecedenceReason: "longest prefix"},
	}
	conflicts := conflictsForRoutes(routes)
	if len(conflicts) != 3 {
		t.Fatalf("conflict count = %d, want duplicate + two nested shadowing conflicts", len(conflicts))
	}
	var duplicate, nested int
	for _, conflict := range conflicts {
		if conflict.Reason == "same route pattern" {
			duplicate++
			if conflict.WinningAppID != "alpha" || conflict.ShadowedAppID != "zeta" {
				t.Fatalf("duplicate winner is not canonical/deterministic: %#v", conflict)
			}
		}
		if conflict.WinningAppID == "admin" {
			nested++
		}
	}
	if duplicate != 1 || nested != 2 {
		t.Fatalf("duplicate=%d nested=%d conflicts=%#v", duplicate, nested, conflicts)
	}
}

func TestIntentionalRouteOverrideIsReportedButDoesNotWarn(t *testing.T) {
	routes := []Route{
		{WorkspaceID: "demo", Environment: "demo", PublicHost: "api.localhost", Pattern: "/*", CanonicalAppID: "frontend", ServiceID: "frontend", Precedence: 0, IntentionalOverride: true},
		{WorkspaceID: "demo", Environment: "demo", PublicHost: "api.localhost", Pattern: "/api/*", CanonicalAppID: "api", ServiceID: "api", Precedence: 4},
	}
	conflicts := conflictsForRoutes(routes)
	if len(conflicts) != 1 || !conflicts[0].Intentional {
		t.Fatalf("intentional conflict = %#v", conflicts)
	}
	if hasAccidentalConflicts(conflicts) {
		t.Fatal("intentional route override was classified as accidental")
	}
}

func TestGeneratedRootFallbackDoesNotAuthorizeDuplicateOwnership(t *testing.T) {
	pattern := "/*"
	ws := &models.Workspace{
		WorkspaceID: "demo",
		Type:        models.WorkspaceTypeRouting,
		RoutingConfig: &models.RoutingConfig{
			LocalDomain:          "api.localhost",
			DefaultRemoteBaseURL: "https://remote.example.test",
		},
		Applications: []*models.Application{
			{ID: "root", AppID: "root", ServiceID: "root-service", Path: "/", Port: 1901, Active: true},
			{ID: "wildcard", AppID: "wildcard", ServiceID: "wildcard-service", Path: "/wildcard", Port: 1902, Active: true, RoutePattern: &pattern},
			{ID: "nested", AppID: "nested", ServiceID: "nested-service", Path: "/nested", Port: 1903, Active: true},
		},
	}
	ws.SetDefaults()
	conflicts := RouteConflicts(ws)
	var duplicate, fallbackShadow int
	for _, conflict := range conflicts {
		switch {
		case conflict.WinningPattern == "/*" && conflict.ShadowedPattern == "/*":
			duplicate++
			if conflict.Intentional {
				t.Fatalf("generated fallback masked duplicate ownership: %#v", conflict)
			}
		case conflict.ShadowedPattern == "/*" && conflict.ShadowedAppID == "root":
			fallbackShadow++
			if !conflict.Intentional {
				t.Fatalf("generated fallback shadow was classified as accidental: %#v", conflict)
			}
		}
	}
	if duplicate != 1 || fallbackShadow == 0 {
		t.Fatalf("duplicate=%d fallbackShadow=%d conflicts=%#v", duplicate, fallbackShadow, conflicts)
	}
	ws.Applications[0].RouteOverride = true
	ws.Applications[1].RouteOverride = true
	for _, conflict := range RouteConflicts(ws) {
		if conflict.WinningPattern == "/*" && conflict.ShadowedPattern == "/*" && !conflict.Intentional {
			t.Fatalf("explicit root override was not honored: %#v", conflict)
		}
	}
}

func TestRouteConflictsAreStableAcrossApplicationRegistrationOrder(t *testing.T) {
	ws := &models.Workspace{
		WorkspaceID: "demo",
		Type:        models.WorkspaceTypeRouting,
		RoutingConfig: &models.RoutingConfig{
			LocalDomain:          "api.localhost",
			DefaultRemoteBaseURL: "https://remote.example.test",
		},
		Applications: []*models.Application{
			{ID: "zeta", AppID: "zeta", ServiceID: "zeta-service", Path: "/", Port: 1901, Active: true},
			{ID: "alpha", AppID: "alpha", ServiceID: "alpha-service", Path: "/", Port: 1902, Active: true},
			{ID: "admin", AppID: "admin", ServiceID: "admin-service", Path: "/admin", Port: 1903, Active: true},
		},
	}
	ws.SetDefaults()
	forward := RouteConflicts(ws)
	reversed := cloneWorkspace(ws)
	for left, right := 0, len(reversed.Applications)-1; left < right; left, right = left+1, right-1 {
		reversed.Applications[left], reversed.Applications[right] = reversed.Applications[right], reversed.Applications[left]
	}
	backward := RouteConflicts(reversed)
	if !reflect.DeepEqual(forward, backward) {
		t.Fatalf("route conflicts depend on registration order:\nforward=%#v\nbackward=%#v", forward, backward)
	}
}
