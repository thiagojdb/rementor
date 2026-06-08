package rpc

import (
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/thiagojdb/rementor/internal/gen/rementor/v1/rementorv1connect"
)

func TestCSRFGuardAllowsNonBrowserMutations(t *testing.T) {
	guard := NewCSRFGuard("token")

	err := guard.validate(rementorv1connect.ControlPlaneServiceDeleteWorkspaceProcedure, http.Header{})
	if err != nil {
		t.Fatalf("expected non-browser mutation to pass, got %v", err)
	}
}

func TestCSRFGuardRequiresTokenForBrowserMutations(t *testing.T) {
	guard := NewCSRFGuard("token")

	header := http.Header{
		"Origin":         {"http://localhost:9300"},
		"Sec-Fetch-Site": {"same-origin"},
	}

	err := guard.validate(rementorv1connect.ControlPlaneServiceDeleteWorkspaceProcedure, header)
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("expected permission denied without token, got %v", err)
	}

	header.Set(CSRFHeader, "token")
	if err := guard.validate(rementorv1connect.ControlPlaneServiceDeleteWorkspaceProcedure, header); err != nil {
		t.Fatalf("expected browser mutation with token to pass, got %v", err)
	}
}

func TestCSRFGuardAllowsBrowserReadsWithoutToken(t *testing.T) {
	guard := NewCSRFGuard("token")
	header := http.Header{
		"Origin":         {"http://rementor.localhost:9300"},
		"Sec-Fetch-Site": {"same-origin"},
	}

	err := guard.validate(rementorv1connect.ControlPlaneServiceListWorkspacesProcedure, header)
	if err != nil {
		t.Fatalf("expected browser read without token to pass, got %v", err)
	}
}

func TestCSRFGuardRejectsCrossSiteRequests(t *testing.T) {
	guard := NewCSRFGuard("token")
	header := http.Header{
		"Origin":         {"https://example.com"},
		"Sec-Fetch-Site": {"cross-site"},
	}

	err := guard.validate(rementorv1connect.ControlPlaneServiceListWorkspacesProcedure, header)
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("expected permission denied for cross-site read, got %v", err)
	}
}

func TestCSRFGuardRejectsUnexpectedBrowserOrigins(t *testing.T) {
	guard := NewCSRFGuard("token")
	header := http.Header{
		"Origin":         {"https://example.com"},
		"Sec-Fetch-Site": {"same-site"},
		CSRFHeader:       {"token"},
	}

	err := guard.validate(rementorv1connect.ControlPlaneServiceDeleteWorkspaceProcedure, header)
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("expected permission denied for unexpected origin, got %v", err)
	}
}
