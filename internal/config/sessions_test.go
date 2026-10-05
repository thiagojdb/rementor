package config

import (
	"github.com/thiagojdb/rementor/internal/models"
	"testing"
	"time"
)

func TestSessionSnapshotAndRegistrationsSurviveSQLiteReload(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	baseline := []models.ApplicationConfig{{ID: "orders", AppID: "orders", ServiceID: "orders", PublicPath: "/orders", Path: "/orders", UpstreamContext: "/orders", Context: "/orders", Health: "health"}}
	session := &models.RoutingSession{EnvironmentID: "dev", HostPrefix: "feature-x", BaselineVersion: 7, CreatedAt: time.Now().UTC(), BaselineRemoteURL: "http://127.0.0.1:19001", Baseline: baseline}
	ws := &models.Workspace{WorkspaceID: "feature-x", Type: "routing", RoutingConfig: &models.RoutingConfig{LocalDomain: "feature-x.localhost", DefaultRemoteBaseURL: session.BaselineRemoteURL}, Session: session, Applications: []*models.Application{{ID: "orders", PublicPath: "/orders", UpstreamContext: "/orders", Port: 24001, Active: true, Health: "custom-health"}}}
	ws.SetDefaults()
	if err := ReplaceWorkspaces([]*models.Workspace{ws}); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadWorkspaces()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Session == nil {
		t.Fatal("missing durable session")
	}
	got := loaded[0]
	if got.EnvironmentID() != "dev" || got.Session.HostPrefix != "feature-x" || got.Session.BaselineVersion != 7 || got.Session.Baseline[0].Port != 0 || got.Applications[0].Port != 24001 || !got.Applications[0].Active {
		t.Fatalf("lost snapshot/registration: %+v", got)
	}
	got.Applications[0].Active = false
	if err := SaveState(loaded); err != nil {
		t.Fatal(err)
	}
	loaded, err = LoadWorkspaces()
	if err != nil {
		t.Fatal(err)
	}
	if loaded[0].Session == nil || loaded[0].Applications[0].Active {
		t.Fatal("state save damaged session metadata")
	}
}

func TestSessionSnapshotDoesNotRestoreOldGlobalRepository(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	base := &models.Workspace{WorkspaceID: "dev", Type: "routing", RoutingConfig: &models.RoutingConfig{LocalDomain: "api.localhost"}, Applications: []*models.Application{{ID: "orders", Repository: "new-repo", PublicPath: "/orders"}}}
	session := &models.Workspace{WorkspaceID: "feature-x", Type: "routing", Session: &models.RoutingSession{EnvironmentID: "dev", Baseline: []models.ApplicationConfig{{ID: "orders", Repository: "old-repo"}}}, RoutingConfig: &models.RoutingConfig{LocalDomain: "feature-x.localhost"}, Applications: []*models.Application{{ID: "orders", Repository: "old-repo", PublicPath: "/orders"}}}
	base.SetDefaults()
	session.SetDefaults()
	if err := ReplaceWorkspaces([]*models.Workspace{base, session}); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadWorkspaces()
	if err != nil {
		t.Fatal(err)
	}
	if loaded[0].Applications[0].Repository != "new-repo" {
		t.Fatal("snapshot reverted global repository")
	}
}

func TestSessionOnlyApplicationRepositoryCanChange(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	ws := &models.Workspace{WorkspaceID: "feature-x", Type: "routing", Session: &models.RoutingSession{EnvironmentID: "dev"}, RoutingConfig: &models.RoutingConfig{LocalDomain: "feature-x.localhost"}, Applications: []*models.Application{{ID: "session-only", Repository: "old-repo", PublicPath: "/app"}}}
	ws.SetDefaults()
	if err := ReplaceWorkspaces([]*models.Workspace{ws}); err != nil {
		t.Fatal(err)
	}
	ws.Applications[0].Repository = "new-repo"
	if err := ReplaceWorkspaces([]*models.Workspace{ws}); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadWorkspaces()
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded[0].Applications[0].Repository; got != "new-repo" {
		t.Fatalf("repository = %q", got)
	}
}
