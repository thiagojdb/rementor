package rpc

import (
	"context"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"github.com/thiagojdb/rementor/internal/config"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"github.com/thiagojdb/rementor/internal/models"
	"github.com/thiagojdb/rementor/internal/services"
)

func TestWorkspaceAndSessionLinksUseTheFrontendBinding(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	if err := config.AppendWorkspace(models.WorkspaceConfig{ID: "dev", Type: models.WorkspaceTypeRouting, Routing: models.RoutingConfig{LocalDomain: "desenvolvimento.giss.localhost", DefaultRemoteBaseURL: "https://example.com"}, Applications: []models.ApplicationConfig{{ID: "frontend", PublicPath: "/", Domain: "dev.giss.localhost"}, {ID: "orders", PublicPath: "/orders"}}}); err != nil {
		t.Fatal(err)
	}
	r := &services.Registry{}
	r.SetRoutingProvider(inheritanceProxy{})
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	s := NewControlPlaneService(r)
	ctx := context.Background()
	created, err := s.CreateRoutingSession(ctx, connect.NewRequest(&rementorv1.CreateRoutingSessionRequest{EnvironmentId: "dev", Name: "feature"}))
	if err != nil {
		t.Fatal(err)
	}
	if created.Msg.Workspace.BrowserUrl != "http://feature.dev.giss.localhost/" {
		t.Fatalf("created session URL = %q", created.Msg.Workspace.BrowserUrl)
	}
	list, err := s.ListWorkspaces(ctx, connect.NewRequest(&rementorv1.ListWorkspacesRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, ws := range list.Msg.Workspaces {
		want := "http://dev.giss.localhost/"
		if ws.Session != nil {
			want = "http://feature.dev.giss.localhost/"
		}
		if ws.BrowserUrl != want {
			t.Fatalf("listed %s URL = %q, want %q", ws.Id, ws.BrowserUrl, want)
		}
		get, err := s.GetWorkspace(ctx, connect.NewRequest(&rementorv1.GetWorkspaceRequest{WorkspaceId: ws.Id}))
		if err != nil {
			t.Fatal(err)
		}
		if get.Msg.Workspace.BrowserUrl != want {
			t.Fatalf("get %s URL = %q", ws.Id, get.Msg.Workspace.BrowserUrl)
		}
	}
}
