package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"github.com/thiagojdb/rementor/internal/gen/rementor/v1/rementorv1connect"
)

type inheritanceWire struct {
	rementorv1connect.UnimplementedControlPlaneServiceHandler
	request *rementorv1.OverrideInheritedRouteRequest
}

func (s *inheritanceWire) OverrideInheritedRoute(ctx context.Context, req *connect.Request[rementorv1.OverrideInheritedRouteRequest]) (*connect.Response[rementorv1.OverrideInheritedRouteResponse], error) {
	s.request = req.Msg
	return connect.NewResponse(&rementorv1.OverrideInheritedRouteResponse{Changed: true}), nil
}
func TestInheritanceCLIAndMCPKeepVersionAndResetSemantics(t *testing.T) {
	service := &inheritanceWire{}
	path, handler := rementorv1connect.NewControlPlaneServiceHandler(service)
	mux := http.NewServeMux()
	mux.Handle("/rpc"+path, http.StripPrefix("/rpc", handler))
	server := httptest.NewServer(mux)
	defer server.Close()
	client := NewClient(server.URL)
	var output bytes.Buffer
	const highVersion = uint64(18446744073709551614)
	if err := runInheritance(client, &output, []string{"override", "dev", "orders", "--mode", "local", "--port", "8090", "--expected-version", "18446744073709551614"}); err != nil {
		t.Fatal(err)
	}
	if service.request.ExpectedRouteVersion != highVersion || service.request.Selection.Port != 8090 || service.request.Selection.RoutePattern != nil {
		t.Fatal("CLI lost version/selection or created unwanted pattern override")
	}
	if err := runInheritance(client, &output, []string{"reset", "dev", "orders", "--expected-version", "0"}); err != nil {
		t.Fatal(err)
	}
	if service.request.Selection != nil || service.request.ExpectedRouteVersion != 0 {
		t.Fatal("CLI reset did not omit selection")
	}
	previous := service.request
	if err := runInheritance(client, &output, []string{"reset", "dev", "orders"}); err == nil || service.request != previous {
		t.Fatal("CLI guessed missing expected version")
	}
	mcp := &mcpServer{client: client}
	result, err := mcp.toolInheritance("rementor.inheritance_override", map[string]any{"workspace": "dev", "application": "orders", "mode": "remote", "remote_url": "https://remote.example.test", "pattern": "", "expected_version": "18446744073709551614"})
	if err != nil || result == nil {
		t.Fatal(err)
	}
	if service.request.ExpectedRouteVersion != highVersion || service.request.Selection.RoutePattern == nil || *service.request.Selection.RoutePattern != "" {
		t.Fatal("MCP lost exact version or explicit empty pattern")
	}
	if _, err := mcp.toolInheritance("rementor.inheritance_reset", map[string]any{"workspace": "dev", "application": "orders", "expected_version": "0"}); err != nil {
		t.Fatal(err)
	}
	if service.request.Selection != nil {
		t.Fatal("MCP reset created a selection")
	}
	if _, err := mcp.toolInheritance("rementor.inheritance_override", map[string]any{"workspace": "dev", "application": "orders", "mode": "local", "port": 8080.5, "expected_version": "0"}); err == nil {
		t.Fatal("MCP truncated fractional port")
	}
}
