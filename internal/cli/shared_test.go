package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"github.com/thiagojdb/rementor/internal/gen/rementor/v1/rementorv1connect"
)

type sharedWire struct {
	rementorv1connect.UnimplementedControlPlaneServiceHandler
	save         *rementorv1.SaveSharedIngressRequest
	catalogOwner string
	recover      *rementorv1.RecoverSharedIngressRequest
}

func (s *sharedWire) GetRoutingWorkspaces(_ context.Context, r *connect.Request[rementorv1.GetRoutingWorkspacesRequest]) (*connect.Response[rementorv1.GetRoutingWorkspacesResponse], error) {
	s.catalogOwner = r.Msg.RoutingOwnerFingerprint
	return connect.NewResponse(&rementorv1.GetRoutingWorkspacesResponse{RoutingOwnerFingerprint: s.catalogOwner, Workspaces: []*rementorv1.PublicWorkspaceHosts{{WorkspaceId: "qa", Name: "Routing Quality", LocalHosts: []string{"qa.localhost"}}}}), nil
}
func (s *sharedWire) SaveSharedIngress(_ context.Context, r *connect.Request[rementorv1.SaveSharedIngressRequest]) (*connect.Response[rementorv1.SaveSharedIngressResponse], error) {
	s.save = r.Msg
	return connect.NewResponse(&rementorv1.SaveSharedIngressResponse{State: &rementorv1.SharedIngressState{Revision: 9007199254740993}}), nil
}
func (s *sharedWire) RecoverSharedIngress(_ context.Context, r *connect.Request[rementorv1.RecoverSharedIngressRequest]) (*connect.Response[rementorv1.RecoverSharedIngressResponse], error) {
	s.recover = r.Msg
	return connect.NewResponse(&rementorv1.RecoverSharedIngressResponse{}), nil
}

func TestSharedIngressCLIAndMCPRequireExplicitLosslessVersions(t *testing.T) {
	wire := &sharedWire{}
	path, handler := rementorv1connect.NewControlPlaneServiceHandler(wire)
	mux := http.NewServeMux()
	mux.Handle("/rpc"+path, http.StripPrefix("/rpc", handler))
	server := httptest.NewServer(mux)
	defer server.Close()
	client := NewClient(server.URL)
	var out bytes.Buffer
	file := filepath.Join(t.TempDir(), "shared.json")
	document := `{"policy":{"schemaVersion":1,"allowedUserIds":["9007199254740993"]},"routingOwnerFingerprint":"owner","gateways":["owner"]}`
	if err := os.WriteFile(file, []byte(document), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runSharedIngress(client, &out, []string{"save", "--file", file, "--expected-version", "18446744073709551614"}); err != nil {
		t.Fatal(err)
	}
	if wire.save.ExpectedVersion != 18446744073709551614 || wire.save.Configuration.Policy.AllowedUserIds[0] != 9007199254740993 || !strings.Contains(out.String(), `"9007199254740993"`) {
		t.Fatal("CLI lost record/account precision")
	}
	previous := wire.save
	if err := runSharedIngress(client, &out, []string{"save", "--file", file}); err == nil || previous != wire.save {
		t.Fatal("CLI guessed a version")
	}
	if err := runSharedIngress(client, &out, []string{"recover"}); err == nil || wire.recover != nil {
		t.Fatal("CLI guessed a recovery revision")
	}
	if err := runSharedIngress(client, &out, []string{"recover", "--expected-revision", "9007199254740993"}); err != nil || wire.recover.ExpectedRevision != 9007199254740993 {
		t.Fatal("CLI recovery lost revision", err)
	}
	mcp := &mcpServer{client: client}
	if _, err := mcp.toolSharedIngress("rementor.shared_ingress_recover", map[string]any{"expected_revision": float64(9007199254740993)}); err == nil {
		t.Fatal("MCP accepted lossy numeric revision")
	}
	if _, err := mcp.toolSharedIngress("rementor.shared_ingress_save", map[string]any{"expected_version": "18446744073709551614", "configuration": map[string]any{"policy": map[string]any{"schemaVersion": 1, "allowedUserIds": []string{"9007199254740993"}}, "routingOwnerFingerprint": "owner", "gateways": []string{"owner"}}}); err != nil {
		t.Fatal(err)
	}
	if wire.save.ExpectedVersion != 18446744073709551614 || wire.save.Configuration.Policy.AllowedUserIds[0] != 9007199254740993 {
		t.Fatal("MCP lost record/account precision")
	}
	if err := os.WriteFile(file, []byte(`{"unknown":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	previous = wire.save
	if err := runSharedIngress(client, &out, []string{"save", "--file", file, "--expected-version", "1"}); err == nil || wire.save != previous {
		t.Fatal("CLI accepted malformed configuration")
	}
	if err := os.WriteFile(file, []byte(strings.Repeat(" ", (1<<20)+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runSharedIngress(client, &out, []string{"save", "--file", file, "--expected-version", "1"}); err == nil || wire.save != previous {
		t.Fatal("CLI accepted oversized configuration")
	}
	if err := runSharedIngress(client, &out, []string{"workspaces", "--routing-computer", "routing-owner"}); err != nil || wire.catalogOwner != "routing-owner" {
		t.Fatal("CLI read the wrong routing catalog", err)
	}
	if _, err := mcp.toolSharedIngress("rementor.shared_ingress_workspaces", map[string]any{"routing_owner_fingerprint": "mcp-routing-owner"}); err != nil || wire.catalogOwner != "mcp-routing-owner" {
		t.Fatal("MCP read the wrong routing catalog", err)
	}
	listed := map[string]bool{}
	for _, tool := range mcpToolList() {
		listed[tool["name"].(string)] = true
	}
	for _, action := range []string{"show", "workspaces", "create", "save", "join", "transfer", "recover", "cancel", "sync"} {
		if !listed["rementor.shared_ingress_"+action] {
			t.Fatal("MCP omitted action", action)
		}
	}
}
