package services

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/thiagojdb/rementor/internal/fleet"
	"github.com/thiagojdb/rementor/internal/models"
)

func followedActionsFixture(t *testing.T) (*Registry, *Registry) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	receiver, manager := inheritanceManagers(t)
	ws := &models.Workspace{WorkspaceID: "dev", Type: models.WorkspaceTypeRouting, RoutingConfig: &models.RoutingConfig{Mode: "path-based", LocalDomain: "dev.localhost", DefaultRemoteBaseURL: "https://example.com"}, Applications: []*models.Application{{ID: "orders", AppID: "orders", PublicPath: "/orders", UpstreamContext: "/orders", Port: 9101, Active: true, Health: "health", RoutePattern: stringPointer("/orders/*")}}}
	ws.SetDefaults()
	source := &Registry{store: &fakeWorkspaceStore{}, routingProvider: &mockRoutingProvider{}, workspaces: []*models.Workspace{ws}}
	if err := source.SetPeerWorkspaceClient(manager.State().Fingerprint, manager); err != nil {
		t.Fatal(err)
	}
	manager.SetWorkspaceSource(source)
	r := inheritanceRegistry(t, receiver)
	if err := r.FollowComputer(context.Background(), manager.State().Fingerprint, 0); err != nil {
		t.Fatal(err)
	}
	return r, source
}

func stringPointer(s string) *string { return &s }

func TestFollowedConfigurationActionsEditSourceAndPreserveOwnership(t *testing.T) {
	r, source := followedActionsFixture(t)
	ctx := context.Background()
	apply := func(app string, edit fleet.ConfigurationEdit) {
		t.Helper()
		if err := r.EditFollowedRoute(ctx, "dev", app, r.GetWorkspaceView("dev").Route.RouteVersion, edit); err != nil {
			t.Fatal(err)
		}
	}
	metadata := applicationConfigs(source.GetWorkspaceView("dev").Applications)[0]
	metadata.Port = 0
	metadata.Name = "Renamed service"
	metadata.PublicPath, metadata.Path = "/new-orders", "/new-orders"
	metadata.Health = "ready"
	apply("orders", fleet.ConfigurationEdit{Action: "upsert-application", Application: &metadata})
	app := source.GetWorkspaceView("dev").Applications[0]
	if app.Port != 9101 || app.Name != metadata.Name || app.Health != "ready" || !app.Active || app.RoutePattern == nil || *app.RoutePattern != "/orders/*" {
		t.Fatal("metadata edit lost source registration or routing selection")
	}
	projected := r.GetWorkspaceView("dev").Applications[0]
	if projected.Port != 0 || projected.PeerEndpoint == nil || projected.PeerEndpoint.OwnerFingerprint != r.GetWorkspaceView("dev").Inheritance.Source.OwnerFingerprint {
		t.Fatal("source port became a receiver registration")
	}
	port := 9123
	apply("orders", fleet.ConfigurationEdit{Action: "register-port", RegistrationPort: &port})
	if source.GetWorkspaceView("dev").Applications[0].Port != port || r.GetWorkspaceView("dev").Applications[0].Port != 0 || r.SourceRegistrationPorts()["dev"]["orders"] != port {
		t.Fatal("registration did not stay in source namespace")
	}
	newApp := models.ApplicationConfig{ID: "billing", AppID: "billing", PublicPath: "/billing", UpstreamContext: "/billing", Health: "health"}
	apply("billing", fleet.ConfigurationEdit{Action: "upsert-application", Application: &newApp, RegistrationPort: &port})
	if len(source.GetWorkspaceView("dev").Applications) != 2 || len(r.GetWorkspaceView("dev").Applications) != 2 {
		t.Fatal("added service did not synchronize")
	}
	apply("billing", fleet.ConfigurationEdit{Action: "delete-application"})
	if len(source.GetWorkspaceView("dev").Applications) != 1 || len(r.GetWorkspaceView("dev").Applications) != 1 {
		t.Fatal("deleted service did not synchronize")
	}
	apply("", fleet.ConfigurationEdit{Action: "update-routing", Routing: &models.RoutingConfig{Mode: "path-based", LocalDomain: "renamed.localhost", DefaultRemoteBaseURL: "https://new.example.com"}})
	if source.GetWorkspaceView("dev").GetLocalDomain() != "renamed.localhost" || r.GetWorkspaceView("dev").GetDefaultRemoteBaseURL() != "https://new.example.com" || source.GetWorkspaceView("dev").Applications[0].Port != port {
		t.Fatal("environment settings did not synchronize or changed registration")
	}
	apply("", fleet.ConfigurationEdit{Action: "delete-environment"})
	if source.GetWorkspaceView("dev") != nil || r.GetWorkspaceView("dev") != nil {
		t.Fatal("environment deletion did not synchronize")
	}
}

func TestFollowedConfigurationActionsRejectStaleAndInvalidEdits(t *testing.T) {
	r, source := followedActionsFixture(t)
	old := r.GetWorkspaceView("dev").Route.RouteVersion
	if _, err := source.ToggleApp("dev", "orders"); err != nil {
		t.Fatal(err)
	}
	port := 9200
	if err := r.EditFollowedRoute(context.Background(), "dev", "orders", old, fleet.ConfigurationEdit{Action: "register-port", RegistrationPort: &port}); !errors.Is(err, fleet.ErrConfigurationConflict) {
		t.Fatalf("stale edit accepted: %v", err)
	}
	if source.GetWorkspaceView("dev").Applications[0].Port != 9101 {
		t.Fatal("stale port overwritten")
	}
	if err := r.SyncComputerConfiguration(context.Background()); err != nil {
		t.Fatal(err)
	}
	invalid := models.ApplicationConfig{ID: "orders", AppID: "orders", PublicPath: "/orders", UpstreamContext: "/orders", RemoteBaseUrl: "file:///etc/passwd"}
	if err := r.EditFollowedRoute(context.Background(), "dev", "orders", r.GetWorkspaceView("dev").Route.RouteVersion, fleet.ConfigurationEdit{Action: "upsert-application", Application: &invalid}); err == nil {
		t.Fatal("invalid metadata accepted")
	}
	if source.GetWorkspaceView("dev").Applications[0].RemoteBaseUrl != "" {
		t.Fatal("invalid edit changed source")
	}
	if _, err := r.CreateRoutingSession("dev", "local branch", ""); err != nil {
		t.Fatal(err)
	}
	if err := r.EditFollowedRoute(context.Background(), "dev", "", r.GetWorkspaceView("dev").Route.RouteVersion, fleet.ConfigurationEdit{Action: "delete-environment"}); err == nil {
		t.Fatal("deleted environment with local session")
	}
	if source.GetWorkspaceView("dev") == nil {
		t.Fatal("source deleted before local preflight")
	}
}

func TestSourceConfigurationActionsRollbackPersistenceFailure(t *testing.T) {
	r, source := followedActionsFixture(t)
	source.store.(*fakeWorkspaceStore).replaceErr = errors.New("disk failure")
	port := 9200
	if err := r.EditFollowedRoute(context.Background(), "dev", "orders", r.GetWorkspaceView("dev").Route.RouteVersion, fleet.ConfigurationEdit{Action: "register-port", RegistrationPort: &port}); err == nil {
		t.Fatal("persistence failure accepted")
	}
	if source.GetWorkspaceView("dev").Applications[0].Port != 9101 || r.SourceRegistrationPorts()["dev"]["orders"] != 9101 {
		t.Fatal("failed edit changed source or receiver")
	}
}
