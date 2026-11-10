package cli

import (
	"bytes"
	"connectrpc.com/connect"
	"context"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"github.com/thiagojdb/rementor/internal/gen/rementor/v1/rementorv1connect"
	"net/http"
	"net/http/httptest"
	"testing"
)

type environmentFollowingWire struct {
	rementorv1connect.UnimplementedControlPlaneServiceHandler
	request *rementorv1.SetEnvironmentFollowingRequest
}

func (s *environmentFollowingWire) SetEnvironmentFollowing(ctx context.Context, req *connect.Request[rementorv1.SetEnvironmentFollowingRequest]) (*connect.Response[rementorv1.SetEnvironmentFollowingResponse], error) {
	s.request = req.Msg
	return connect.NewResponse(&rementorv1.SetEnvironmentFollowingResponse{}), nil
}
func TestEnvironmentFollowingCLIAndMCPPreserveExplicitVersions(t *testing.T) {
	wire := &environmentFollowingWire{}
	path, handler := rementorv1connect.NewControlPlaneServiceHandler(wire)
	mux := http.NewServeMux()
	mux.Handle("/rpc"+path, http.StripPrefix("/rpc", handler))
	server := httptest.NewServer(mux)
	defer server.Close()
	client := NewClient(server.URL)
	var output bytes.Buffer
	if err := runComputerFollowing(client, &output, []string{"exclude", "--workspace", "dev", "--expected-revision", "1", "--expected-workspace-version", "18446744073709551614"}); err != nil {
		t.Fatal(err)
	}
	if wire.request.WorkspaceId != "dev" || wire.request.Following || wire.request.ExpectedRevision != 1 || wire.request.ExpectedWorkspaceVersion != 18446744073709551614 {
		t.Fatal("CLI lost scope or exact versions")
	}
	previous := wire.request
	if err := runComputerFollowing(client, &output, []string{"include", "--workspace", "dev", "--expected-revision", "1"}); err == nil || wire.request != previous {
		t.Fatal("CLI guessed local workspace version")
	}
	mcp := &mcpServer{client: client}
	if _, err := mcp.toolComputerFollowing("rementor.computer_include", map[string]any{"workspace": "dev", "expected_revision": "2", "expected_workspace_version": "18446744073709551614"}); err != nil {
		t.Fatal(err)
	}
	if !wire.request.Following || wire.request.ExpectedRevision != 2 || wire.request.ExpectedWorkspaceVersion != 18446744073709551614 {
		t.Fatal("MCP lost resume semantics or versions")
	}
}
