package config

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thiagojdb/rementor/internal/fleet"
	"github.com/thiagojdb/rementor/internal/models"
)

func TestInheritanceCacheValidationAndExport(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	snapshot, err := fleet.NewSnapshot(strings.Repeat("a", 64), 7, fleet.WorkspaceIntent{Scope: fleet.Scope{WorkspaceID: "source", EnvironmentID: "source"}, Type: models.WorkspaceTypeRouting, Routing: models.RoutingConfig{LocalDomain: "source.localhost"}, Applications: []models.ApplicationConfig{{ID: "orders", PublicPath: "/orders", Port: 8080, Active: true}}})
	if err != nil {
		t.Fatal(err)
	}
	base := models.WorkspaceConfig{ID: "receiving", Type: models.WorkspaceTypeRouting, Routing: models.RoutingConfig{LocalDomain: "receiver.localhost"}, Applications: []models.ApplicationConfig{{ID: "orders", PublicPath: "/orders", Port: 9090}}}
	projected, err := fleet.ProjectInheritance(strings.Repeat("b", 64), fleet.Scope{WorkspaceID: "receiving", EnvironmentID: "receiving"}, base, snapshot, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := AppendWorkspace(projected); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadWorkspaces()
	if err != nil {
		t.Fatal(err)
	}
	exported := workspaceConfigFromWorkspace(loaded[0])
	if exported.Inheritance == nil || exported.Inheritance.Source != fleet.SnapshotReference(snapshot) || exported.Inheritance.OriginalApplications[0].Port != 9090 {
		t.Fatal("export lost relationship or original registrations")
	}
	exported.Inheritance.OriginalApplications[0].Port++
	if loaded[0].Inheritance.OriginalApplications[0].Port != 9090 {
		t.Fatal("export shares restore state")
	}
	corrupt := loaded[0].Inheritance.Clone()
	corrupt.Source.Version++
	raw, _ := json.Marshal(corrupt)
	db, err := readyDB()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE workspaces SET inheritance_json = ? WHERE id = ?", string(raw), "receiving"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := LoadWorkspaces(); err == nil {
		t.Fatal("loaded cache whose source metadata disagrees with its validated snapshot")
	}
}
