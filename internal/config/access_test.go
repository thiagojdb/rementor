package config

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/thiagojdb/rementor/internal/access"
)

func TestPublicAccessPersistenceUsesCASAndStrictRestartValidation(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	initial, err := LoadPublicAccessConfiguration()
	if err != nil || initial.Revision != 0 || initial.Document.SchemaVersion != 1 {
		t.Fatal("new store did not return empty configuration", err)
	}
	doc := access.Document{SchemaVersion: 1, Origins: []access.Origin{{ID: "api", URL: "https://api.example.test"}}, Bindings: []access.Binding{{WorkspaceID: "dev", OriginID: "api", LocalHost: "dev.localhost"}}, SharedWorkspaceID: "dev", AllowedUserIDs: []uint64{99}}
	saved, err := SavePublicAccessConfiguration(doc, 0)
	if err != nil || saved.Revision != 1 {
		t.Fatal("save failed", err)
	}
	doc.Origins[0].URL = "https://changed.example.test"
	if _, err := SavePublicAccessConfiguration(doc, 0); !errors.Is(err, ErrPublicAccessConflict) {
		t.Fatal("stale writer overwrote configuration", err)
	}
	loaded, err := LoadPublicAccessConfiguration()
	if err != nil || loaded.Document.Origins[0].URL != saved.Document.Origins[0].URL {
		t.Fatal("restart lost published origins", err)
	}
	db, err := readyDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE public_access_configuration SET document_json='{"schemaVersion":1,"unknown":true}' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPublicAccessConfiguration(); err == nil {
		t.Fatal("restart silently ignored corrupted public access metadata")
	}
}
