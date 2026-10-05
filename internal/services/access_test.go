package services

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thiagojdb/rementor/internal/access"
	"github.com/thiagojdb/rementor/internal/config"
	"github.com/thiagojdb/rementor/internal/models"
)

func TestPublicWorkspaceCatalogUsesRoutingNamesAndHostsWithoutTargets(t *testing.T) {
	qaName, adminName := "Routing Quality", "Management"
	r := &Registry{workspaces: []*models.Workspace{
		{WorkspaceID: "qa", Name: &qaName, Type: "routing", RoutingConfig: &models.RoutingConfig{LocalDomain: "qa.localhost"}, Applications: []*models.Application{{ID: "orders", PublicPath: "/orders", Domain: "orders.qa.localhost", Port: 8080, Active: true, RemoteBaseUrl: "https://private-upstream.example.test"}}},
		{WorkspaceID: "admin", Name: &adminName, Type: "routing", RoutingConfig: &models.RoutingConfig{LocalDomain: config.DefaultRementorDomain}, Applications: []*models.Application{{ID: "admin", PublicPath: "/", Port: 9300, Active: true}}},
	}}
	entries := r.PublicWorkspaceHosts()
	if len(entries) != 1 || entries[0].WorkspaceID != "qa" || entries[0].Name != "Routing Quality" || len(entries[0].LocalHosts) != 2 || entries[0].LocalHosts[0] != "orders.qa.localhost" || entries[0].LocalHosts[1] != "qa.localhost" {
		t.Fatal("catalog did not match routable source hosts", entries)
	}
	raw, _ := json.Marshal(entries)
	if strings.Contains(string(raw), "8080") || strings.Contains(string(raw), "private-upstream") || strings.Contains(string(raw), "9300") {
		t.Fatal("catalog exposed target data")
	}
	entries[0].LocalHosts[0] = "changed.localhost"
	if r.PublicWorkspaceHosts()[0].LocalHosts[0] != "orders.qa.localhost" {
		t.Fatal("caller mutated source catalog")
	}
}

func TestPublicAccessTracksWorkspaceRoutesAndCancelsPolicyChanges(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := &Registry{store: &fakeWorkspaceStore{}, routingProvider: &mockRoutingProvider{}, workspaces: []*models.Workspace{{WorkspaceID: "dev", Type: "routing", Route: models.RouteState{RouteVersion: 12}, RoutingConfig: &models.RoutingConfig{LocalDomain: "dev.localhost"}, Applications: []*models.Application{{ID: "orders", PublicPath: "/orders", Port: 8080, Active: true}}}, {WorkspaceID: "branch", Type: "routing", Session: &models.RoutingSession{EnvironmentID: "dev", HostPrefix: "branch"}, Route: models.RouteState{RouteVersion: 13}, RoutingConfig: &models.RoutingConfig{LocalDomain: "branch.dev.localhost"}, Applications: []*models.Application{{ID: "orders", PublicPath: "/orders", Port: 8090, Active: true}}}}}
	doc := access.Document{SchemaVersion: 1, Origins: []access.Origin{{ID: "api", URL: "https://api.example.test"}}, Bindings: []access.Binding{{WorkspaceID: "dev", OriginID: "api", LocalHost: "dev.localhost"}, {WorkspaceID: "branch", OriginID: "api", LocalHost: "branch.dev.localhost"}}, Devices: []access.Device{{NodeID: "computer", WorkspaceID: "branch"}}, SharedWorkspaceID: "dev", AllowedUserIDs: []uint64{99}}
	saved, err := r.SavePublicAccessConfiguration(doc, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, request, done, err := r.BeginPublicRequest(context.Background(), access.Identity{NodeID: "computer", UserID: 99}, "https://api.example.test", "/orders/123", false)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if request.Guard != "branch:orders:13" || request.LocalHost != "branch.dev.localhost" || request.Manifest.EnvironmentID != "dev" || request.Manifest.SessionID != "branch" || request.Route.LocalTarget != "http://localhost:8090" {
		t.Fatalf("selection lost scope/ownership: %+v", request)
	}
	browser, err := r.ResolveBrowserURL("branch", "orders")
	if err != nil || browser.BrowserURL != "https://api.example.test/orders" {
		t.Fatalf("browser URL ignored public origin: %v %s", err, browser.BrowserURL)
	}
	if _, err := r.DeleteWorkspaceWithMetadata("branch", ""); err == nil {
		t.Fatal("deleted a published workspace without updating access configuration")
	}
	if r.GetWorkspaceView("branch") == nil {
		t.Fatal("rejected publication mutation changed runtime")
	}
	doc.Devices[0].WorkspaceID = "dev"
	if _, err := r.SavePublicAccessConfiguration(doc, 0); !errors.Is(err, config.ErrPublicAccessConflict) {
		t.Fatal("access ignored stale expected version", err)
	}
	if ctx.Err() != nil {
		t.Fatal("failed CAS cancelled a valid request")
	}
	if _, err := r.SavePublicAccessConfiguration(doc, saved.Revision); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("policy change kept old request/stream alive")
	}
	if _, _, _, err := r.BeginPublicRequest(context.Background(), access.Identity{NodeID: "computer", UserID: 100}, "https://api.example.test", "/orders", false); !errors.Is(err, access.ErrNotAuthorized) {
		t.Fatal("unapproved user inherited published scope", err)
	}
}

func TestPublicTargetRejectsManagementAndRemoteLoopback(t *testing.T) {
	protected := func(port int) bool { return port == 9300 }
	for _, route := range []*Route{
		{Target: "http://localhost:9300", EffectiveMode: models.RouteModeLocal},
		{Target: "http://127.0.0.1:9300", EffectiveMode: models.RouteModeLocal},
		{Target: "http://127.0.0.1:8080", EffectiveMode: models.RouteModeRemote},
		{Target: "http://evil.localhost:8080", EffectiveMode: models.RouteModeRemote},
	} {
		if AccessTargetAllowed(context.Background(), route, protected) == nil {
			t.Fatal("published control plane or implicit remote loopback", route)
		}
	}
	if err := AccessTargetAllowed(context.Background(), &Route{Target: "http://localhost:8080", EffectiveMode: models.RouteModeLocal}, protected); err != nil {
		t.Fatal("rejected registered local app", err)
	}
}
