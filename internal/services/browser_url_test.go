package services

import (
	"errors"
	"testing"
	"time"

	"github.com/thiagojdb/rementor/internal/models"
)

func TestResolveBrowserURLUsesCanonicalBindingAndKeepsURLStableAcrossModes(t *testing.T) {
	ws := &models.Workspace{
		WorkspaceID: "desenvolvimento",
		Type:        models.WorkspaceTypeRouting,
		RoutingConfig: &models.RoutingConfig{
			LocalDomain:          "api.localhost",
			DefaultRemoteBaseURL: "https://remote.example.test",
		},
		Applications: []*models.Application{
			{ID: "front", AppID: "front", ServiceID: "front-service", Aliases: []string{"front-giss-v2"}, Path: "/", Port: 3000},
			{ID: "orders", AppID: "orders", Aliases: []string{"orders-api"}, Path: "/orders///", Port: 3001},
			{ID: "admin", AppID: "admin", Domain: "Admin.Localhost.", Path: "/admin/", Port: 3002},
		},
	}
	ws.SetDefaults()
	r := &Registry{workspaces: []*models.Workspace{ws}}

	remote, err := r.ResolveBrowserURL("desenvolvimento", " FRONT_GISS_V2 ")
	if err != nil {
		t.Fatal(err)
	}
	if remote.URL != "http://api.localhost/" || remote.PublicPath != "/" {
		t.Fatalf("root browser URL = %#v, want http://api.localhost/", remote)
	}
	if remote.Target != "https://remote.example.test" || remote.EffectiveMode != "remote" {
		t.Fatalf("root route target/mode = %q/%q", remote.Target, remote.EffectiveMode)
	}

	nested, err := r.ResolveBrowserURL("desenvolvimento", "orders-api")
	if err != nil {
		t.Fatal(err)
	}
	if nested.URL != "http://api.localhost/orders" {
		t.Fatalf("nested browser URL = %q, want http://api.localhost/orders", nested.URL)
	}

	perDomain, err := r.ResolveBrowserURL("desenvolvimento", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if perDomain.URL != "http://admin.localhost/admin" {
		t.Fatalf("per-domain browser URL = %q, want http://admin.localhost/admin", perDomain.URL)
	}

	// The public route follows the normalized context path when it differs
	// from the legacy application path.
	ws.Applications = append(ws.Applications, &models.Application{ID: "billing", Context: "/service-billing", Path: "/billing", Port: 3003})
	nestedContext, err := r.ResolveBrowserURL("desenvolvimento", "billing")
	if err != nil {
		t.Fatal(err)
	}
	if nestedContext.URL != "http://api.localhost/service-billing" {
		t.Fatalf("context browser URL = %q, want http://api.localhost/service-billing", nestedContext.URL)
	}

	// A route toggle changes the proxy target and mode metadata, never the
	// stable public browser entry point.
	ws.Applications[1].Active = true
	local, err := r.ResolveBrowserURL("desenvolvimento", "orders")
	if err != nil {
		t.Fatal(err)
	}
	if local.URL != nested.URL || local.Target != "http://localhost:3001" || local.EffectiveMode != "local" {
		t.Fatalf("local route = %#v, want stable URL and localhost target", local)
	}

	pattern := "/orders-v2/*"
	ws.Applications[1].RoutePattern = &pattern
	patternURL, err := r.ResolveBrowserURL("desenvolvimento", "orders-api")
	if err != nil {
		t.Fatal(err)
	}
	if patternURL.URL != "http://api.localhost/orders-v2" {
		t.Fatalf("explicit route pattern URL = %q, want /orders-v2", patternURL.URL)
	}
}

func TestResolveBrowserURLSupportsLocalAppsAndOperationMetadata(t *testing.T) {
	completed := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	operation := &models.OperationMetadata{OperationID: "op-7", CorrelationID: "corr-7", RouteVersion: 7, Kind: "toggle", CreatedAt: completed, CompletedAt: completed}
	ws := &models.Workspace{
		WorkspaceID: "qualidade", Type: models.WorkspaceTypeLocalApps,
		Route: models.RouteState{RouteVersion: 7, OperationID: "op-7"}, LastOperation: operation,
		Applications: []*models.Application{{ID: "front", AppID: "front", Domain: "front.localhost", Path: "/ignored", Port: 4000, LastOperation: operation, Route: models.RouteState{RouteVersion: 7, OperationID: "op-7"}}},
	}
	ws.SetDefaults()
	r := &Registry{workspaces: []*models.Workspace{ws}}
	result, err := r.ResolveBrowserURL("qualidade", "front")
	if err != nil {
		t.Fatal(err)
	}
	if result.URL != "http://front.localhost/" || result.PublicPath != "/" {
		t.Fatalf("local-apps URL = %q/%q", result.URL, result.PublicPath)
	}
	if result.RouteVersion != 7 || result.OperationID != "op-7" || result.CorrelationID != "corr-7" || result.Operation == nil {
		t.Fatalf("operation metadata = %#v", result)
	}
	if result.EffectiveMode != "local" {
		t.Fatalf("local-apps effective mode = %q, want local", result.EffectiveMode)
	}
}

func TestResolveBrowserURLRejectsMissingPublicBinding(t *testing.T) {
	ws := &models.Workspace{WorkspaceID: "desenvolvimento", Type: models.WorkspaceTypeLocalApps, Applications: []*models.Application{{ID: "front", Port: 3000}}}
	ws.SetDefaults()
	r := &Registry{workspaces: []*models.Workspace{ws}}
	_, err := r.ResolveBrowserURL("desenvolvimento", "front")
	if !errors.Is(err, ErrBrowserURLBinding) {
		t.Fatalf("error = %v, want browser URL binding error", err)
	}
}

func TestResolveBrowserURLReportsMissingEnvironmentBinding(t *testing.T) {
	dev := &models.Workspace{
		WorkspaceID:   "desenvolvimento",
		Type:          models.WorkspaceTypeRouting,
		RoutingConfig: &models.RoutingConfig{LocalDomain: "dev.localhost", DefaultRemoteBaseURL: "https://remote.example.test"},
		Applications:  []*models.Application{{AppID: "orders", Aliases: []string{"orders-api"}, Path: "/orders"}},
	}
	quality := &models.Workspace{WorkspaceID: "qualidade", Type: models.WorkspaceTypeRouting, RoutingConfig: &models.RoutingConfig{LocalDomain: "qa.localhost"}}
	dev.SetDefaults()
	quality.SetDefaults()
	r := &Registry{workspaces: []*models.Workspace{dev, quality}}

	_, err := r.ResolveBrowserURL("qualidade", "orders-api")
	var bindingErr *BrowserURLBindingError
	if !errors.As(err, &bindingErr) || bindingErr.Field != "environment binding" {
		t.Fatalf("error = %v, want environment binding error", err)
	}
}
