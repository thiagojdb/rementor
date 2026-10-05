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

type sharingWire struct {
	rementorv1connect.UnimplementedControlPlaneServiceHandler
	request *rementorv1.SetTailscaleSharingRequest
}

func (s *sharingWire) SetTailscaleSharing(ctx context.Context, request *connect.Request[rementorv1.SetTailscaleSharingRequest]) (*connect.Response[rementorv1.SetTailscaleSharingResponse], error) {
	s.request = request.Msg
	return connect.NewResponse(&rementorv1.SetTailscaleSharingResponse{State: &rementorv1.TailscaleSharingState{Enabled: request.Msg.Enabled}}), nil
}
func TestTailscaleSharingCLIAndMCPRequireExplicitRevisionAndBoolean(t *testing.T) {
	wire := &sharingWire{}
	path, handler := rementorv1connect.NewControlPlaneServiceHandler(wire)
	mux := http.NewServeMux()
	mux.Handle("/rpc"+path, http.StripPrefix("/rpc", handler))
	server := httptest.NewServer(mux)
	defer server.Close()
	client := NewClient(server.URL)
	var output bytes.Buffer
	if err := runTailscaleSharing(client, &output, []string{"on"}); err == nil || wire.request != nil {
		t.Fatal("CLI guessed revision")
	}
	if err := runTailscaleSharing(client, &output, []string{"on", "--expected-revision", "9007199254740993"}); err != nil || !wire.request.Enabled || wire.request.ExpectedRevision != 9007199254740993 {
		t.Fatal("CLI lost revision precision", err)
	}
	mcp := &mcpServer{client: client}
	if _, err := mcp.toolTailscaleSharing("rementor.tailscale_sharing_set", map[string]any{"enabled": false, "expected_revision": "9007199254740993"}); err != nil || wire.request.Enabled || wire.request.ExpectedRevision != 9007199254740993 {
		t.Fatal("MCP lost explicit disabled intent", err)
	}
	if _, err := mcp.toolTailscaleSharing("rementor.tailscale_sharing_set", map[string]any{"enabled": "false", "expected_revision": "0"}); err == nil {
		t.Fatal("MCP accepted non-boolean toggle")
	}
}
