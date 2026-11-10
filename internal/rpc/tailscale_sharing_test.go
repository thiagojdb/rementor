package rpc

import (
	"context"
	"errors"
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
	"github.com/thiagojdb/rementor/internal/tailshare"
)

type unconfiguredGateway struct{}

func (unconfiguredGateway) Inspect(context.Context) (tailshare.Gateway, error) {
	return tailshare.Gateway{DNSSuffix: "tailtest.ts.net", Setup: "dedicated tagged gateway needed"}, nil
}
func (unconfiguredGateway) Ensure(context.Context, tailshare.Origin, tailshare.Ownership, func() error) error {
	return errors.New("unexpected provider mutation")
}
func (unconfiguredGateway) Remove(context.Context, tailshare.Origin) error {
	return errors.New("unexpected provider mutation")
}
func (unconfiguredGateway) Verify(context.Context, tailshare.Origin, access.Configuration) error {
	return errors.New("unexpected provider mutation")
}
func TestTailscaleSharingRPCPreparationToggleCSRFAndPersistence(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	if err := config.AppendWorkspace(models.WorkspaceConfig{ID: "dev", Type: "routing", Routing: models.RoutingConfig{LocalDomain: "dev.giss.localhost"}, Applications: []models.ApplicationConfig{{ID: "orders", PublicPath: "/orders", Port: 8080, Active: true}}}); err != nil {
		t.Fatal(err)
	}
	registry := &services.Registry{}
	registry.SetRoutingProvider(inheritanceProxy{})
	if err := registry.Load(); err != nil {
		t.Fatal(err)
	}
	runtime := ingress.NewRuntime(registry, rejectIdentity{}, "127.0.0.1:0", "http://127.0.0.1:18080", nil)
	defer runtime.Close()
	m, err := tailshare.New(config.TailscaleSharingStore{}, unconfiguredGateway{}, registry.PublicAccessConfiguration)
	if err != nil {
		t.Fatal(err)
	}
	m.Reconcile(context.Background())
	s := NewControlPlaneService(registry)
	s.SetPublicIngress(runtime, deviceCatalog{})
	s.SetTailscalePublisher(m)
	path, handler := rementorv1connect.NewControlPlaneServiceHandler(s, connect.WithInterceptors(NewCSRFGuard("fixture")))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := rementorv1connect.NewControlPlaneServiceClient(http.DefaultClient, server.URL)
	prepared, err := client.PrepareTailscaleSharing(context.Background(), connect.NewRequest(&rementorv1.PrepareTailscaleSharingRequest{WorkspaceId: "dev"}))
	if err != nil {
		t.Fatal(err)
	}
	doc := prepared.Msg.Document
	if doc.Origins[0].Url != "https://dev-giss.tailtest.ts.net" || doc.Origins[0].Id != "api" || doc.AllowedUserIds[0] != 9007199254740993 || registry.PublicAccessConfiguration().Revision != 0 {
		t.Fatal("preparation did not preserve name/account or mutated policy", doc)
	}
	if _, err := client.SavePublicAccess(context.Background(), connect.NewRequest(&rementorv1.SavePublicAccessRequest{Document: doc})); err != nil {
		t.Fatal(err)
	}
	request := connect.NewRequest(&rementorv1.SetTailscaleSharingRequest{Enabled: true})
	request.Header().Set("Origin", server.URL)
	if _, err := client.SetTailscaleSharing(context.Background(), request); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("toggle bypassed CSRF", err)
	}
	request.Header().Set(CSRFHeader, "fixture")
	enabled, err := client.SetTailscaleSharing(context.Background(), request)
	if err != nil || !enabled.Msg.State.Enabled || enabled.Msg.State.Revision != 1 {
		t.Fatal("could not persist sharing intent", err)
	}
	if _, err := client.SetTailscaleSharing(context.Background(), request); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("accepted stale toggle", err)
	}
	m.Reconcile(context.Background())
	status, err := client.GetTailscaleSharing(context.Background(), connect.NewRequest(&rementorv1.GetTailscaleSharingRequest{}))
	if err != nil || status.Msg.State.Publications[0].Status != "needs_setup" {
		t.Fatal("misreported pending gateway", err)
	}
	if _, err := client.SetTailscaleSharing(context.Background(), connect.NewRequest(&rementorv1.SetTailscaleSharingRequest{ExpectedRevision: 1})); err != nil {
		t.Fatal(err)
	}
	restarted, err := tailshare.New(config.TailscaleSharingStore{}, unconfiguredGateway{}, registry.PublicAccessConfiguration)
	if err != nil || restarted.State().Enabled || restarted.State().Revision != 2 {
		t.Fatal("restart lost disabled toggle", err)
	}
	// Existing origin IDs and device mappings survive URL conversion.
	doc.Origins[0].Id = "custom-api"
	doc.Origins[0].Url = "https://computer.tailtest.ts.net:8444"
	doc.Bindings[0].OriginId = "custom-api"
	doc.Devices = []*rementorv1.PublicDeviceSelection{{NodeId: "phone", WorkspaceId: "dev"}}
	if _, err := client.SavePublicAccess(context.Background(), connect.NewRequest(&rementorv1.SavePublicAccessRequest{Document: doc, ExpectedRevision: 1})); err != nil {
		t.Fatal(err)
	}
	prepared, err = client.PrepareTailscaleSharing(context.Background(), connect.NewRequest(&rementorv1.PrepareTailscaleSharingRequest{}))
	if err != nil || prepared.Msg.Document.Origins[0].Id != "custom-api" || prepared.Msg.Document.Devices[0].NodeId != "phone" || prepared.Msg.Document.Origins[0].Url != "https://dev-giss.tailtest.ts.net" {
		t.Fatal("preparation lost existing topology", err)
	}
}
