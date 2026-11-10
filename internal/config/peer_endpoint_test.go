package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/thiagojdb/rementor/internal/models"
)

func TestPeerEndpointSurvivesReloadReplacementAndExport(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	endpoint := models.PeerEndpoint{OwnerFingerprint: strings.Repeat("a", 64), WorkspaceID: "source", ApplicationID: "orders", Version: 7, Digest: strings.Repeat("b", 64)}
	if err := AppendWorkspace(models.WorkspaceConfig{ID: "following", Type: models.WorkspaceTypeRouting, Routing: models.RoutingConfig{LocalDomain: "follow.localhost"}, Applications: []models.ApplicationConfig{{ID: "orders", PublicPath: "/orders", Active: true, PeerEndpoint: &endpoint}}}); err != nil {
		t.Fatal(err)
	}
	workspaces, err := LoadWorkspaces()
	if err != nil {
		t.Fatal(err)
	}
	app := workspaces[0].Applications[0]
	if app.PeerEndpoint == nil || *app.PeerEndpoint != endpoint || app.Port != 0 || !app.HasLocal() || app.LocalHealthURL() != "" {
		t.Fatal("reload lost ownership or turned a peer into receiver loopback")
	}
	exported := workspaceConfigFromWorkspace(workspaces[0])
	if exported.Applications[0].PeerEndpoint == nil || *exported.Applications[0].PeerEndpoint != endpoint {
		t.Fatal("export lost endpoint ownership")
	}
	exported.Applications[0].PeerEndpoint.Version++
	if app.PeerEndpoint.Version != endpoint.Version {
		t.Fatal("export shares endpoint metadata")
	}
	if err := ReplaceWorkspaces(workspaces); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadWorkspaces()
	if err != nil {
		t.Fatal(err)
	}
	if loaded[0].Applications[0].PeerEndpoint == nil || *loaded[0].Applications[0].PeerEndpoint != endpoint {
		t.Fatal("replacement lost endpoint metadata")
	}
}
