package rpc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"github.com/thiagojdb/rementor/internal/access"
	"github.com/thiagojdb/rementor/internal/config"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"github.com/thiagojdb/rementor/internal/gen/rementor/v1/rementorv1connect"
	"github.com/thiagojdb/rementor/internal/ingress"
	"github.com/thiagojdb/rementor/internal/models"
	"github.com/thiagojdb/rementor/internal/services"
	"google.golang.org/protobuf/encoding/protojson"
)

type deviceCatalog struct{}

func (deviceCatalog) Devices(context.Context) ([]ingress.Device, error) {
	return []ingress.Device{{NodeID: "computer", UserID: 9007199254740993, Name: "computer", Self: true, DNSName: "computer.example.ts.net", Online: true}}, nil
}

type rejectIdentity struct{}

func (rejectIdentity) Resolve(context.Context, string) (access.Identity, error) {
	return access.Identity{}, access.ErrNotAuthorized
}
func TestPublicAccessRPCPublishesVersionedPolicyWithoutManagement(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	if err := config.AppendWorkspace(models.WorkspaceConfig{ID: "dev", Type: "routing", Routing: models.RoutingConfig{LocalDomain: "dev.localhost"}, Applications: []models.ApplicationConfig{{ID: "orders", PublicPath: "/orders", Port: 8080, Active: true}}}); err != nil {
		t.Fatal(err)
	}
	registry := &services.Registry{}
	registry.SetRoutingProvider(inheritanceProxy{})
	if err := registry.Load(); err != nil {
		t.Fatal(err)
	}
	runtime := ingress.NewRuntime(registry, rejectIdentity{}, "127.0.0.1:0", "http://127.0.0.1:18080", nil)
	defer runtime.Close()
	service := NewControlPlaneService(registry)
	service.SetPublicIngress(runtime, deviceCatalog{})
	path, handler := rementorv1connect.NewControlPlaneServiceHandler(service, connect.WithInterceptors(NewCSRFGuard("fixture-token")))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := rementorv1connect.NewControlPlaneServiceClient(http.DefaultClient, server.URL)
	doc := &rementorv1.PublicAccessDocument{SchemaVersion: 1, Origins: []*rementorv1.PublicOrigin{{Id: "api", Url: "https://api.example.test"}}, Bindings: []*rementorv1.PublicOriginBinding{{WorkspaceId: "dev", OriginId: "api", LocalHost: "dev.localhost"}}, SharedWorkspaceId: "dev", AllowedUserIds: []uint64{9007199254740993}}
	request := connect.NewRequest(&rementorv1.SavePublicAccessRequest{Document: doc})
	request.Header().Set("Origin", server.URL)
	if _, err := client.SavePublicAccess(context.Background(), request); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("browser policy mutation bypassed CSRF", err)
	}
	request.Header().Set(CSRFHeader, "fixture-token")
	saved, err := client.SavePublicAccess(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Msg.Configuration.Revision != 1 || saved.Msg.Configuration.Document.AllowedUserIds[0] != 9007199254740993 {
		t.Fatal("lost version/user precision")
	}
	if _, err := client.SavePublicAccess(context.Background(), request); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("stale expected revision accepted", err)
	}
	status, err := client.GetPublicAccess(context.Background(), connect.NewRequest(&rementorv1.GetPublicAccessRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if !status.Msg.Ingress.Active || len(status.Msg.Devices) != 1 || len(status.Msg.Workspaces) != 1 {
		t.Fatal("missing ingress/catalog/hosts", status.Msg)
	}
	raw, _ := protojson.Marshal(status.Msg)
	var parsed rementorv1.GetPublicAccessResponse
	if err := protojson.Unmarshal(raw, &parsed); err != nil || parsed.Devices[0].UserId != 9007199254740993 {
		t.Fatal("wire lost identity precision", err)
	}
	list, err := client.ListWorkspaces(context.Background(), connect.NewRequest(&rementorv1.ListWorkspacesRequest{}))
	if err != nil || list.Msg.Workspaces[0].BrowserUrl != "https://api.example.test/" {
		t.Fatal("workspace entry ignored configured origin", err)
	}
	doc.Bindings[0].LocalHost = "rementor.localhost"
	if _, err := client.SavePublicAccess(context.Background(), connect.NewRequest(&rementorv1.SavePublicAccessRequest{Document: doc, ExpectedRevision: 1})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("published management host", err)
	}
	if current := registry.PublicAccessConfiguration(); current.Revision != 1 {
		t.Fatal("invalid publication changed policy")
	}
	if _, err := client.SavePublicAccess(context.Background(), connect.NewRequest(&rementorv1.SavePublicAccessRequest{Document: &rementorv1.PublicAccessDocument{SchemaVersion: 1}, ExpectedRevision: 1})); err != nil {
		t.Fatal(err)
	}
	restarted := &services.Registry{}
	restarted.SetRoutingProvider(inheritanceProxy{})
	if err := restarted.Load(); err != nil {
		t.Fatal(err)
	}
	if got := restarted.PublicAccessConfiguration(); got.Revision != 2 || len(got.Document.Origins) != 0 {
		t.Fatal("restart lost publication removal")
	}
}
