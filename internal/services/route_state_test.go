package services

import (
	"errors"
	"testing"
	"time"

	"github.com/thiagojdb/rementor/internal/models"
)

type routeStateProvider struct {
	available bool
	loadErr   error
}

func (p *routeStateProvider) IsAvailable() bool                           { return p.available }
func (p *routeStateProvider) LoadInitialConfig([]*models.Workspace) error { return p.loadErr }
func (p *routeStateProvider) Close() error                                { return nil }

func newRouteStateRegistry(provider *routeStateProvider, active bool) *Registry {
	ws := &models.Workspace{
		WorkspaceID: "demo",
		Type:        models.WorkspaceTypeRouting,
		RoutingConfig: &models.RoutingConfig{
			LocalDomain:          "api.localhost",
			DefaultRemoteBaseURL: "https://remote.example.test",
		},
		Applications: []*models.Application{{
			ID: "orders", AppID: "orders", Path: "/orders", Port: 1903,
			RemoteBaseUrl: "https://orders.example.test", Active: active,
		}},
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

func TestRouteStateKeepsEffectiveModeIndependentFromLocalHealth(t *testing.T) {
	r := newRouteStateRegistry(&routeStateProvider{available: true}, false)
	plan, err := r.PlanRoute("demo", "orders", "local", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ApplyRoutePlan("demo", plan, plan.BaseRouteVersion, "", "test"); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	r.workspaces[0].Applications[0].Runtime.UpdateBothStatuses(false, &now, false, &now)
	_, app, err := r.GetApplicationView("demo", "orders")
	if err != nil {
		t.Fatal(err)
	}
	if app.Route.DesiredMode != models.RouteModeLocal || app.Route.EffectiveMode != models.RouteModeLocal {
		t.Fatalf("health changed route mode: %#v", app.Route)
	}
	if app.Route.ProxyHealth != models.ProxyHealthUp || app.Route.VerificationStatus != models.RouteVerificationVerified {
		t.Fatalf("unexpected proxy projection: %#v", app.Route)
	}
	if app.Route.OperationID == "" || app.Route.VerifiedAt == nil {
		t.Fatalf("route verification metadata missing: %#v", app.Route)
	}
}

func TestRouteStateDistinguishesUnavailableProxyAndStaleLoadedRoute(t *testing.T) {
	provider := &routeStateProvider{available: true}
	r := newRouteStateRegistry(provider, true)
	plan, err := r.PlanRoute("demo", "orders", "remote", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ApplyRoutePlan("demo", plan, plan.BaseRouteVersion, "", "test"); err != nil {
		t.Fatal(err)
	}

	// The proxy still serves the successfully loaded remote route while the
	// persisted intent changes to local and the provider becomes unavailable.
	provider.available = false
	ws := r.workspaces[0]
	ws.Applications[0].Active = true
	ws.Route.RouteVersion++
	ws.Applications[0].Route.RouteVersion = ws.Route.RouteVersion
	r.markRoutingState([]*models.Workspace{ws}, models.RouteVerificationProviderUnavailable)
	_, app, err := r.GetApplicationView("demo", "orders")
	if err != nil {
		t.Fatal(err)
	}
	if app.Route.DesiredMode != models.RouteModeLocal || app.Route.EffectiveMode != models.RouteModeRemote {
		t.Fatalf("expected local intent/remote loaded route, got %#v", app.Route)
	}
	if app.Route.VerificationStatus != models.RouteVerificationStale || app.Route.ProxyHealth != models.ProxyHealthUnavailable {
		t.Fatalf("expected stale unavailable projection, got %#v", app.Route)
	}
	if app.Route.RemoteFallback {
		t.Fatal("application read must not claim fallback without request resolution")
	}

	resolution, err := r.ResolveRoute("demo", "api.localhost", "/orders/1")
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Route == nil || !resolution.Route.RemoteFallback {
		t.Fatalf("request resolution did not confirm remote fallback: %#v", resolution)
	}
	if resolution.Route.VerificationStatus != models.RouteVerificationStale {
		t.Fatalf("request resolution hid stale proxy verification: %#v", resolution.Route)
	}
}

func TestRouteApplyWhileProviderUnavailableDoesNotPersistCandidate(t *testing.T) {
	provider := &routeStateProvider{available: true}
	r := newRouteStateRegistry(provider, true)
	remotePlan, err := r.PlanRoute("demo", "orders", "remote", nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.ApplyRoutePlan("demo", remotePlan, remotePlan.BaseRouteVersion, "", "test")
	if err != nil {
		t.Fatal(err)
	}
	if result.Operation == nil {
		t.Fatal("route apply did not return operation metadata")
	}
	persisted := r.workspaces[0].Applications[0].Route
	if persisted.RouteVersion != result.Operation.RouteVersion || persisted.VerificationStatus != models.RouteVerificationVerified {
		t.Fatalf("persisted route metadata = %#v, want committed version and verification", persisted)
	}

	provider.available = false
	localPlan, err := r.PlanRoute("demo", "orders", "local", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ApplyRoutePlan("demo", localPlan, localPlan.BaseRouteVersion, "", "test"); err == nil {
		t.Fatal("expected provider-unavailable route apply to fail")
	}
	unchanged := r.workspaces[0].Applications[0].Route
	if unchanged.RouteVersion != persisted.RouteVersion || unchanged.VerificationStatus != persisted.VerificationStatus {
		t.Fatalf("failed apply changed persisted route metadata: before=%#v after=%#v", persisted, unchanged)
	}
}

func TestRouteStateProviderUnavailableWithoutSnapshotIsUnknown(t *testing.T) {
	r := newRouteStateRegistry(&routeStateProvider{available: false}, true)
	r.markRoutingState(r.workspaces, models.RouteVerificationProviderUnavailable)
	_, app, err := r.GetApplicationView("demo", "orders")
	if err != nil {
		t.Fatal(err)
	}
	if app.Route.EffectiveMode != models.RouteModeUnknown || app.Route.VerificationStatus != models.RouteVerificationUnknown {
		t.Fatalf("expected unknown effective route, got %#v", app.Route)
	}
	if app.Route.ProxyHealth != models.ProxyHealthUnavailable {
		t.Fatalf("expected unavailable proxy health, got %#v", app.Route)
	}
}

func TestRouteStateFailedReloadDoesNotPromoteDesiredRoute(t *testing.T) {
	provider := &routeStateProvider{available: true}
	r := newRouteStateRegistry(provider, true)
	remotePlan, err := r.PlanRoute("demo", "orders", "remote", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ApplyRoutePlan("demo", remotePlan, remotePlan.BaseRouteVersion, "", "test"); err != nil {
		t.Fatal(err)
	}

	localPlan, err := r.PlanRoute("demo", "orders", "local", nil)
	if err != nil {
		t.Fatal(err)
	}
	provider.loadErr = errors.New("reload failed")
	if _, err := r.ApplyRoutePlan("demo", localPlan, localPlan.BaseRouteVersion, "", "test"); err == nil {
		t.Fatal("expected failed proxy reload")
	}

	_, app, err := r.GetApplicationView("demo", "orders")
	if err != nil {
		t.Fatal(err)
	}
	if app.Route.DesiredMode != models.RouteModeRemote || app.Route.EffectiveMode != models.RouteModeRemote {
		t.Fatalf("failed reload changed live route projection: %#v", app.Route)
	}
}
