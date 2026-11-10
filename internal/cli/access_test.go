package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"github.com/thiagojdb/rementor/internal/gen/rementor/v1/rementorv1connect"
)

type accessWire struct {
	rementorv1connect.UnimplementedControlPlaneServiceHandler
	request *rementorv1.SavePublicAccessRequest
}

func (s *accessWire) SavePublicAccess(ctx context.Context, r *connect.Request[rementorv1.SavePublicAccessRequest]) (*connect.Response[rementorv1.SavePublicAccessResponse], error) {
	s.request = r.Msg
	return connect.NewResponse(&rementorv1.SavePublicAccessResponse{}), nil
}
func TestPublicAccessCLIAndMCPKeepRevisionAndAccountPrecision(t *testing.T) {
	wire := &accessWire{}
	path, handler := rementorv1connect.NewControlPlaneServiceHandler(wire)
	mux := http.NewServeMux()
	mux.Handle("/rpc"+path, http.StripPrefix("/rpc", handler))
	server := httptest.NewServer(mux)
	defer server.Close()
	client := NewClient(server.URL)
	var out bytes.Buffer
	file := filepath.Join(t.TempDir(), "access.json")
	if err := os.WriteFile(file, []byte(`{"schemaVersion":1,"allowedUserIds":["9007199254740993"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runAccess(client, &out, []string{"save", "--file", file, "--expected-revision", "18446744073709551614"}); err != nil {
		t.Fatal(err)
	}
	if wire.request.ExpectedRevision != 18446744073709551614 || wire.request.Document.AllowedUserIds[0] != 9007199254740993 {
		t.Fatal("CLI lost version/account precision")
	}
	previous := wire.request
	if err := runAccess(client, &out, []string{"save", "--file", file}); err == nil || wire.request != previous {
		t.Fatal("CLI guessed policy revision")
	}
	mcp := &mcpServer{client: client}
	if _, err := mcp.toolPublicAccess("rementor.access_save", map[string]any{"expected_revision": "18446744073709551614", "document": map[string]any{"schemaVersion": 1, "allowedUserIds": []string{"9007199254740993"}}}); err != nil {
		t.Fatal(err)
	}
	if wire.request.ExpectedRevision != 18446744073709551614 || wire.request.Document.AllowedUserIds[0] != 9007199254740993 {
		t.Fatal("MCP lost precision")
	}
	if err := runAccess(client, &out, []string{"clear", "--expected-revision", "0"}); err != nil || wire.request.Document.SchemaVersion != 1 || len(wire.request.Document.Origins) != 0 {
		t.Fatal("CLI could not revoke publication", err)
	}
}
