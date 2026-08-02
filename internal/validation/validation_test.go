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
	if err := RoutePattern("//*"); err == nil {
		t.Fatal("expected duplicate separators in a route pattern to be rejected")
	}
}

func TestNormalizeRoutePathCanonicalizesTrailingSlash(t *testing.T) {
	got, err := NormalizeRoutePath("public path", "/portal/home///", false)
	if err != nil {
		t.Fatalf("unexpected normalization error: %v", err)
	}
	if got != "/portal/home" {
		t.Fatalf("normalized path = %q, want /portal/home", got)
	}
}

func TestNormalizeRoutePathRejectsWhitespaceAndTraversal(t *testing.T) {
	for _, value := range []string{"/portal home", "/portal/../admin", "/portal//home"} {
		if _, err := NormalizeRoutePath("public path", value, false); err == nil {
			t.Fatalf("expected malformed path %q to be rejected", value)
		}
	}
}

func TestRouteMetadataUnknownFrontendRootIsStructuredWarning(t *testing.T) {
	warnings, err := ValidateApplicationMetadata(models.WorkspaceTypeRouting, models.ApplicationConfig{
		ID: "portal", PublicPath: "/portal/home", UpstreamContext: "/portal",
	}, MetadataValidationOptions{})
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if len(warnings) == 0 || warnings[0].Code != WarningFrontendRootUnknown || warnings[0].Field != "frontendRoot" || warnings[0].Severity != "warning" || warnings[0].Remediation == "" {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
}

func TestRouteMetadataStrictModePromotesUnknownRoot(t *testing.T) {
	_, err := ValidateApplicationMetadata(models.WorkspaceTypeRouting, models.ApplicationConfig{
		ID: "portal", PublicPath: "/portal/home",
	}, MetadataValidationOptions{Strict: true})
	if err == nil {
		t.Fatal("expected strict mode to reject unknown frontend root")
	}
	metadataErr, ok := err.(*MetadataValidationError)
	if !ok || metadataErr.Warning.Code != WarningFrontendRootUnknown || metadataErr.Warning.Severity != "error" {
		t.Fatalf("unexpected strict error: %#v", err)
	}
}

func TestRouteMetadataStrictModeKeepsLegacyMigrationWarningNonFatal(t *testing.T) {
	warnings, err := ValidateApplicationMetadata(models.WorkspaceTypeRouting, models.ApplicationConfig{
		ID: "root", Path: "/", Context: "/",
	}, MetadataValidationOptions{Strict: true})
	if err != nil {
		t.Fatalf("legacy migration warning should remain non-fatal in strict mode: %v", err)
	}
	if len(warnings) == 0 || warnings[0].Code != WarningLegacyRouteMetadata {
		t.Fatalf("expected legacy migration warning, got %#v", warnings)
	}
}

func TestRouteMetadataRejectsExplicitPathConflict(t *testing.T) {
	_, err := ValidateApplicationMetadata(models.WorkspaceTypeRouting, models.ApplicationConfig{
		ID: "portal", Path: "/legacy", PublicPath: "/portal",
	}, MetadataValidationOptions{})
	if err == nil {
		t.Fatal("expected contradictory legacy and explicit public paths to be rejected")
	}
}

func TestRouteMetadataAllowsNestedFrontendRoot(t *testing.T) {
	warnings, err := ValidateApplicationMetadata(models.WorkspaceTypeRouting, models.ApplicationConfig{
		ID: "portal", PublicPath: "/portal/home", FrontendRoot: "/portal",
	}, MetadataValidationOptions{})
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	for _, warning := range warnings {
		if warning.Code == WarningFrontendRootUnknown || warning.Code == WarningFrontendRootMismatch {
			t.Fatalf("unexpected frontend root warning: %#v", warning)
		}
	}
}

func TestRouteMetadataRejectsRootFrontendForNestedPublicPath(t *testing.T) {
	_, err := ValidateApplicationMetadata(models.WorkspaceTypeRouting, models.ApplicationConfig{
		ID: "portal", PublicPath: "/portal/home", FrontendRoot: "/",
	}, MetadataValidationOptions{})
	if err == nil {
		t.Fatal("expected known root frontend to reject an incompatible nested public path")
	}
	metadataErr, ok := err.(*MetadataValidationError)
	if !ok || metadataErr.Warning.Code != WarningFrontendRootMismatch || metadataErr.Warning.Severity != "error" {
		t.Fatalf("unexpected mismatch error: %#v", err)
	}
}

func TestRouteMetadataRejectsNestedFrontendForRootPublicPath(t *testing.T) {
	_, err := ValidateApplicationMetadata(models.WorkspaceTypeRouting, models.ApplicationConfig{
		ID: "portal", PublicPath: "/", FrontendRoot: "/portal",
	}, MetadataValidationOptions{})
	if err == nil {
		t.Fatal("expected nested frontend root to reject a root public path")
	}
	metadataErr, ok := err.(*MetadataValidationError)
	if !ok || metadataErr.Warning.Code != WarningFrontendRootMismatch {
		t.Fatalf("unexpected mismatch error: %#v", err)
	}
}
