package config

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thiagojdb/rementor/internal/publication"
)

func TestTailnetPublicationPersistsTargetsAndUsesCAS(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	store := TailnetPublishingStore{}
	c, err := store.Load()
	if err != nil || c.Revision != 0 {
		t.Fatal(c, err)
	}
	c = publication.Configuration{Revision: 1, Enabled: true, Suffix: "rementor.internal", CertificateMode: "private", ListenAddress: "100.64.0.2", Entries: []publication.Entry{{Computer: strings.Repeat("a", 64), Name: "marcus", WorkspaceID: "desenvolvimento", IncludeSessions: true}}}
	if _, err := store.Save(c, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(c, 0); !errors.Is(err, publication.ErrConflict) {
		t.Fatal("stale write accepted", err)
	}
	loaded, err := store.Load()
	if err != nil || loaded.Revision != 1 || len(loaded.Entries) != 1 || !loaded.Entries[0].IncludeSessions {
		t.Fatal("restart lost publication intent", loaded, err)
	}
	db, err := readyDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE tailnet_publishing SET state_json='{"suffix":"https://bad","certificateMode":"private"}' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("invalid persisted publication silently accepted")
	}
}
