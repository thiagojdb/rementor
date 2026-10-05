package rpc

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"github.com/thiagojdb/rementor/internal/config"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"github.com/thiagojdb/rementor/internal/gen/rementor/v1/rementorv1connect"
	"github.com/thiagojdb/rementor/internal/ingress"
	"github.com/thiagojdb/rementor/internal/models"
	"github.com/thiagojdb/rementor/internal/peers"
	"github.com/thiagojdb/rementor/internal/services"
	"github.com/thiagojdb/rementor/internal/shared"
)

func TestSharedIngressRPCValidatesPinnedRoutingBindingsAndFencesOwnership(t *testing.T) {
	ctx := context.Background()
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "routing"))
	if err := config.AppendWorkspace(models.WorkspaceConfig{ID: "dev", Type: "routing", Routing: models.RoutingConfig{LocalDomain: "dev.localhost"}, Applications: []models.ApplicationConfig{{ID: "orders", PublicPath: "/orders", Port: 8080, Active: true}}}); err != nil {
		t.Fatal(err)
	}
	registry := &services.Registry{}
	registry.SetRoutingProvider(inheritanceProxy{})
	if err := registry.Load(); err != nil {
		t.Fatal(err)
	}
	type node struct {
		manager   *peers.Manager
		authority *shared.Authority
		client    rementorv1connect.ControlPlaneServiceClient
	}
	newNode := func(name string) node {
		m, err := peers.Open(filepath.Join(t.TempDir(), name), name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(m.Close)
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := listener.Addr().String()
		listener.Close()
		if err := m.Configure(address, "https://"+address); err != nil {
			t.Fatal(err)
		}
		m.SetWorkspaceSource(registry)
		t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "store"))
		store, err := config.NewSharedIngressStore()
		if err != nil {
			t.Fatal(err)
		}
		a, err := shared.Open(m.State().Fingerprint, store)
		if err != nil {
			t.Fatal(err)
		}
		m.SetSharedAuthority(a)
		runtime := ingress.NewRuntime(registry, rejectIdentity{}, "127.0.0.1:0", "http://127.0.0.1:18080", nil)
		runtime.ConfigureShared(a, m)
		t.Cleanup(runtime.Close)
		s := NewControlPlaneService(registry)
		s.SetPeerManager(m)
		s.SetSharedIngress(a)
		s.SetPublicIngress(runtime, nil)
		path, handler := rementorv1connect.NewControlPlaneServiceHandler(s, connect.WithInterceptors(NewCSRFGuard("shared-token")))
		mux := http.NewServeMux()
		mux.Handle(path, handler)
		server := httptest.NewServer(mux)
		t.Cleanup(server.Close)
		return node{m, a, rementorv1connect.NewControlPlaneServiceClient(server.Client(), server.URL)}
	}
	a, b := newNode("owner"), newNode("successor")
	aID, bID := a.manager.State().Fingerprint, b.manager.State().Fingerprint
	code, invite, err := a.manager.CreateInvitation()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.manager.Pair(ctx, code, invite.Fingerprint); err != nil {
		t.Fatal(err)
	}
	doc := &rementorv1.PublicAccessDocument{SchemaVersion: 1, Origins: []*rementorv1.PublicOrigin{{Id: "api", Url: "https://api.example.test"}}, Bindings: []*rementorv1.PublicOriginBinding{{WorkspaceId: "dev", OriginId: "api", LocalHost: "dev.localhost"}}, SharedWorkspaceId: "dev", AllowedUserIds: []uint64{9007199254740993}}
	input := &rementorv1.SharedIngressConfiguration{Policy: doc, RoutingOwnerFingerprint: aID, Gateways: []string{aID, bID}}
	request := connect.NewRequest(&rementorv1.CreateSharedIngressRequest{Configuration: input})
	request.Header().Set("Origin", "http://rementor.localhost")
	if _, err := a.client.CreateSharedIngress(ctx, request); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("shared mutation bypassed CSRF", err)
	}
	request.Header().Set(CSRFHeader, "shared-token")
	created, err := a.client.CreateSharedIngress(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if created.Msg.State.Record.Policy.AllowedUserIds[0] != 9007199254740993 || created.Msg.State.Record.RoutingOwnerFingerprint != aID {
		t.Fatal("wire lost account or routing owner")
	}
	if _, err := a.client.CreateSharedIngress(ctx, request); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("replayed creation accepted", err)
	}
	joined, err := b.client.JoinSharedIngress(ctx, connect.NewRequest(&rementorv1.JoinSharedIngressRequest{OwnerFingerprint: aID}))
	if err != nil || joined.Msg.State.Record.Digest != created.Msg.State.Record.Digest {
		t.Fatal("explicit join failed", err)
	}
	if _, err := b.client.SaveSharedIngress(ctx, connect.NewRequest(&rementorv1.SaveSharedIngressRequest{Configuration: input, ExpectedVersion: 1})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("replica changed policy", err)
	}
	transferred, err := a.client.TransferSharedIngress(ctx, connect.NewRequest(&rementorv1.TransferSharedIngressRequest{DestinationFingerprint: bID, ExpectedVersion: 1}))
	if err != nil {
		t.Fatal(err)
	}
	if transferred.Msg.State.Record.OwnerFingerprint != bID || transferred.Msg.State.Record.RoutingOwnerFingerprint != aID {
		t.Fatal("handoff changed routing target")
	}
	catalog, err := b.client.GetRoutingWorkspaces(ctx, connect.NewRequest(&rementorv1.GetRoutingWorkspacesRequest{RoutingOwnerFingerprint: aID}))
	if err != nil || catalog.Msg.RoutingOwnerFingerprint != aID || len(catalog.Msg.Workspaces) != 1 || catalog.Msg.Workspaces[0].Name != "dev" || catalog.Msg.Workspaces[0].LocalHosts[0] != "dev.localhost" {
		t.Fatal("moved selector lost routing-computer catalog", err)
	}
	// The new selector must validate bindings over the pinned old routing host,
	// rather than accepting its own locally matching workspace IDs.
	input.Policy.Bindings[0].LocalHost = "missing.localhost"
	if _, err := b.client.SaveSharedIngress(ctx, connect.NewRequest(&rementorv1.SaveSharedIngressRequest{Configuration: input, ExpectedVersion: 2})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("unvalidated routing binding accepted", err)
	}
	if b.authority.State().Record.Version != 2 {
		t.Fatal("invalid candidate changed live record")
	}
	input.Policy.Bindings[0].LocalHost = "dev.localhost"
	saved, err := b.client.SaveSharedIngress(ctx, connect.NewRequest(&rementorv1.SaveSharedIngressRequest{Configuration: input, ExpectedVersion: 2}))
	if err != nil || saved.Msg.State.Record.Version != 3 {
		t.Fatal("new owner could not validate/save on fixed routing host", err)
	}
	if _, err := b.client.SaveSharedIngress(ctx, connect.NewRequest(&rementorv1.SaveSharedIngressRequest{Configuration: input, ExpectedVersion: 2})); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("stale policy version accepted", err)
	}
	if _, err := a.client.SyncSharedIngress(ctx, connect.NewRequest(&rementorv1.SyncSharedIngressRequest{})); err != nil {
		t.Fatal(err)
	}
	if _, err := a.client.CancelSharedIngress(ctx, connect.NewRequest(&rementorv1.CancelSharedIngressRequest{ExpectedRevision: 0})); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("stale cancellation accepted", err)
	}
	if _, err := a.client.RecoverSharedIngress(ctx, connect.NewRequest(&rementorv1.RecoverSharedIngressRequest{ExpectedRevision: 0})); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("stale recovery accepted", err)
	}
	if err := a.manager.Revoke(bID); err != nil {
		t.Fatal(err)
	}
	if _, err := b.client.GetRoutingWorkspaces(ctx, connect.NewRequest(&rementorv1.GetRoutingWorkspacesRequest{RoutingOwnerFingerprint: aID})); err == nil {
		t.Fatal("revoked selector read routing-computer catalog")
	}
	if _, err := b.client.SaveSharedIngress(ctx, connect.NewRequest(&rementorv1.SaveSharedIngressRequest{Configuration: input, ExpectedVersion: 3})); err == nil {
		t.Fatal("revoked selector published a new routing-host policy")
	}
	if b.authority.State().Record.Version != 3 {
		t.Fatal("revocation mutated policy")
	}
}
