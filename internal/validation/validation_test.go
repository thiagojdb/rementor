package validation

import (
	"testing"

	"github.com/thiagojdb/rementor/internal/models"
)

func TestWorkspaceRejectsNginxInjection(t *testing.T) {
	err := Workspace(models.WorkspaceTypeRouting, "api.localhost;\nserver {}", "https://example.test", nil)
	if err == nil {
		t.Fatal("expected unsafe local domain to be rejected")
	}
}

func TestWorkspaceRejectsCredentialBearingRemoteURL(t *testing.T) {
	err := Workspace(models.WorkspaceTypeRouting, "api.localhost", "https://user:secret@example.test", nil)
	if err == nil {
		t.Fatal("expected URL credentials to be rejected")
	}
}

func TestApplicationAllowsRemoteOnlyRoutingApp(t *testing.T) {
	err := Application(models.WorkspaceTypeRouting, models.ApplicationConfig{
		ID:      "orders-api",
		Path:    "/orders",
		Context: "/orders",
		Port:    0,
	})
	if err != nil {
		t.Fatalf("expected remote-only routing application to be valid: %v", err)
	}
}

func TestApplicationRequiresPortForLocalApps(t *testing.T) {
	err := Application(models.WorkspaceTypeLocalApps, models.ApplicationConfig{
		ID:     "orders-api",
		Domain: "orders.localhost",
	})
	if err == nil {
		t.Fatal("expected local-apps application without a port to be rejected")
	}
}

func TestRoutePatternRejectsEmbeddedWildcard(t *testing.T) {
	if err := RoutePattern("/orders/*/admin"); err == nil {
		t.Fatal("expected embedded wildcard to be rejected")
	}
}
