package services

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/thiagojdb/rementor/internal/config"
	"github.com/thiagojdb/rementor/internal/models"
)

func TestDeferredPublishingStateDoesNotBlockFleetRouting(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	if err := config.AppendWorkspace(models.WorkspaceConfig{ID: "dev", Type: "routing", Routing: models.RoutingConfig{LocalDomain: "dev.localhost"}, Applications: []models.ApplicationConfig{{ID: "app", Path: "/", Port: 8080}}}); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", config.GetDBFile())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// An older build may have publication settings that reference removed
	// workspaces, or state this release cannot parse. Keep it for the later
	// publishing release without reading it during ordinary fleet operations.
	for _, table := range []string{"public_access_configuration", "tailnet_publishing", "tailscale_sharing", "shared_ingress"} {
		if _, err := db.Exec("CREATE TABLE IF NOT EXISTS " + table + " (id INTEGER PRIMARY KEY, state_json TEXT)"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT INTO "+table+" VALUES (1,?)", "deferred-state-from-a-newer-build"); err != nil {
			t.Fatal(err)
		}
	}
	r := &Registry{}
	r.SetRoutingProvider(&mockRoutingProvider{})
	if err := r.Load(); err != nil {
		t.Fatalf("publishing state blocked startup: %v", err)
	}
	if err := r.DeleteWorkspace("dev"); err != nil {
		t.Fatalf("publishing state blocked a route edit: %v", err)
	}
	for _, table := range []string{"public_access_configuration", "tailnet_publishing", "tailscale_sharing", "shared_ingress"} {
		var raw string
		if err := db.QueryRow("SELECT state_json FROM " + table + " WHERE id=1").Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if raw != "deferred-state-from-a-newer-build" {
			t.Fatalf("rewrote deferred settings in %s", table)
		}
	}
}
