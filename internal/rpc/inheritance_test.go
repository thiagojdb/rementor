package rpc

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/thiagojdb/rementor/internal/config"
	"github.com/thiagojdb/rementor/internal/fleet"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"github.com/thiagojdb/rementor/internal/gen/rementor/v1/rementorv1connect"
	"github.com/thiagojdb/rementor/internal/models"
	"github.com/thiagojdb/rementor/internal/peers"
	"github.com/thiagojdb/rementor/internal/services"
	"google.golang.org/protobuf/encoding/protojson"
)

type inheritanceProxy struct{}

func (inheritanceProxy) IsAvailable() bool                           { return true }
func (inheritanceProxy) LoadInitialConfig([]*models.Workspace) error { return nil }
func (inheritanceProxy) Close() error                                { return nil }

type inheritanceSource struct{}

func (inheritanceSource) ExportPeerWorkspaces() []fleet.WorkspaceSummary {
	return []fleet.WorkspaceSummary{{Scope: models.PeerScope{WorkspaceID: "source", EnvironmentID: "source"}, Name: "Source", Type: "routing", Version: 17}, {Scope: models.PeerScope{WorkspaceID: "branch", EnvironmentID: "source", SessionID: "branch"}, Name: "Feature", Type: "routing", Version: 18}}
}
func (inheritanceSource) ExportPeerWorkspace(id string) (fleet.WorkspaceIntent, uint64, error) {
	scope := models.PeerScope{WorkspaceID: "source", EnvironmentID: "source"}
	if id == "branch" {
		scope.WorkspaceID = "branch"
		scope.SessionID = "branch"
	}
	return fleet.WorkspaceIntent{Scope: scope, Type: "routing", Routing: models.RoutingConfig{LocalDomain: "source.localhost", DefaultRemoteBaseURL: "https://remote.example.test"}, Applications: []models.ApplicationConfig{{ID: "orders", PublicPath: "/orders", Port: 8081, Active: true}}}, 17, nil
}

func TestInheritanceRPCPreservesScopesOwnershipAndOfflineReset(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	manager := func(name string) *peers.Manager {
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
		return m
	}
	owner, receiver := manager("owner"), manager("receiver")
	owner.SetWorkspaceSource(inheritanceSource{})
	code, invitation, err := owner.CreateInvitation()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := receiver.Pair(context.Background(), code, invitation.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if err := config.AppendWorkspace(models.WorkspaceConfig{ID: "receiving", Type: "routing", Routing: models.RoutingConfig{LocalDomain: "receiving.localhost", DefaultRemoteBaseURL: "https://original.example.test"}, Applications: []models.ApplicationConfig{{ID: "original", PublicPath: "/original", Port: 8090}}}); err != nil {
		t.Fatal(err)
	}
	registry := &services.Registry{}
	registry.SetRoutingProvider(inheritanceProxy{})
	if err := registry.Load(); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetPeerWorkspaceClient(receiver.State().Fingerprint, receiver); err != nil {
		t.Fatal(err)
	}
	service := NewControlPlaneService(registry)
	service.SetPeerManager(receiver)
	path, handler := rementorv1connect.NewControlPlaneServiceHandler(service, connect.WithInterceptors(NewCSRFGuard("token")))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := rementorv1connect.NewControlPlaneServiceClient(server.Client(), server.URL)
	ctx := context.Background()
	catalog, err := client.ListPeerWorkspaces(ctx, connect.NewRequest(&rementorv1.ListPeerWorkspacesRequest{Fingerprint: invitation.Fingerprint}))
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Msg.Workspaces) != 2 || catalog.Msg.Workspaces[1].Scope.SessionId != "branch" || catalog.Msg.Workspaces[1].Scope.EnvironmentId != "source" {
		t.Fatal("catalog erased session scope")
	}
	followReq := connect.NewRequest(&rementorv1.FollowPeerWorkspaceRequest{WorkspaceId: "receiving", Fingerprint: invitation.Fingerprint, Scope: catalog.Msg.Workspaces[1].Scope})
	followReq.Header().Set("Origin", "http://rementor.localhost")
	if _, err := client.FollowPeerWorkspace(ctx, followReq); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("follow bypassed CSRF: %v", err)
	}
	followReq.Header().Set(CSRFHeader, "token")
	followed, err := client.FollowPeerWorkspace(ctx, followReq)
	if err != nil {
		t.Fatal(err)
	}
	ws := followed.Msg.Workspace
	app := ws.Applications[0]
	if !followed.Msg.Changed || followed.Msg.Status.Status != "current" || app.Port != 0 || app.PeerEndpoint.GetOwnerFingerprint() != invitation.Fingerprint || ws.Inheritance.Source.Scope.SessionId != "branch" || ws.Routing.LocalDomain != "receiving.localhost" {
		t.Fatal("follow lost ownership/scope/status/address")
	}
	raw, _ := protojson.Marshal(ws.Inheritance)
	if strings.Contains(string(raw), "snapshot") || strings.Contains(string(raw), "original") {
		t.Fatal("raw private cache exposed")
	}
	if _, err := client.FollowPeerWorkspace(ctx, followReq); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("stale version not aborted: %v", err)
	}
	overridden, err := client.OverrideInheritedRoute(ctx, connect.NewRequest(&rementorv1.OverrideInheritedRouteRequest{WorkspaceId: "receiving", ApplicationId: "orders", ExpectedRouteVersion: ws.Route.Version.Value, Selection: &rementorv1.InheritedRouteSelection{Mode: "local", Port: 8091}}))
	if err != nil {
		t.Fatal(err)
	}
	if overridden.Msg.Workspace.Applications[0].PeerEndpoint != nil || overridden.Msg.Workspace.Applications[0].Port != 8091 {
		t.Fatal("own port retained peer owner")
	}
	if err := owner.Configure("", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SyncPeerWorkspace(ctx, connect.NewRequest(&rementorv1.SyncPeerWorkspaceRequest{WorkspaceId: "receiving"})); err == nil {
		t.Fatal("offline sync reported success")
	}
	status, err := client.GetWorkspaceInheritance(ctx, connect.NewRequest(&rementorv1.GetWorkspaceInheritanceRequest{WorkspaceId: "receiving"}))
	if err != nil {
		t.Fatal(err)
	}
	if status.Msg.Status.Status != "stale" || status.Msg.Status.Error == "" {
		t.Fatal("offline status omitted failure")
	}
	reset, err := client.OverrideInheritedRoute(ctx, connect.NewRequest(&rementorv1.OverrideInheritedRouteRequest{WorkspaceId: "receiving", ApplicationId: "orders", ExpectedRouteVersion: overridden.Msg.Workspace.Route.Version.Value}))
	if err != nil {
		t.Fatal(err)
	}
	if reset.Msg.Workspace.Applications[0].PeerEndpoint == nil || len(reset.Msg.Workspace.Inheritance.Overrides) != 0 {
		t.Fatal("omitted selection did not reset cache")
	}
	stopped, err := client.StopFollowingWorkspace(ctx, connect.NewRequest(&rementorv1.StopFollowingWorkspaceRequest{WorkspaceId: "receiving", ExpectedRouteVersion: reset.Msg.Workspace.Route.Version.Value}))
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Msg.Status.Status != "independent" || stopped.Msg.Workspace.Inheritance != nil || stopped.Msg.Workspace.Applications[0].Id != "original" {
		t.Fatal("stop failed to restore original state")
	}
}

func TestInheritanceActionsRequireBrowserCSRF(t *testing.T) {
	for _, procedure := range []string{rementorv1connect.ControlPlaneServiceFollowPeerWorkspaceProcedure, rementorv1connect.ControlPlaneServiceSyncPeerWorkspaceProcedure, rementorv1connect.ControlPlaneServiceOverrideInheritedRouteProcedure, rementorv1connect.ControlPlaneServiceStopFollowingWorkspaceProcedure, rementorv1connect.ControlPlaneServiceFollowComputerProcedure, rementorv1connect.ControlPlaneServiceSyncComputerConfigurationProcedure, rementorv1connect.ControlPlaneServiceStopFollowingComputerProcedure, rementorv1connect.ControlPlaneServiceSetEnvironmentFollowingProcedure, rementorv1connect.ControlPlaneServiceEditFollowedConfigurationProcedure} {
		guard := NewCSRFGuard("token")
		header := http.Header{"Origin": []string{"http://rementor.localhost"}}
		if err := guard.validate(procedure, header); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("%s bypassed CSRF", procedure)
		}
		header.Set(CSRFHeader, "token")
		if err := guard.validate(procedure, header); err != nil {
			t.Fatal(err)
		}
	}
}
