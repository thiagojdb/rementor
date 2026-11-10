package config

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/thiagojdb/rementor/internal/tailshare"
)

func TestTailscaleSharingPersistsDesiredStateAndSeparateCASVersion(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	store := TailscaleSharingStore{}
	o := tailshare.Origin{ID: "api", Name: "giss", URL: "https://giss.tail.example.ts.net", Target: "http://127.0.0.1:9302"}
	c := tailshare.Configuration{Owner: "0123456789abcdef0123456789abcdef", Revision: 1, Enabled: true, Origins: []tailshare.Origin{o}}
	saved, err := store.Save(c, 0)
	if err != nil {
		t.Fatal(err)
	}
	c.Owned = []tailshare.Origin{o}
	c.GatewayID = "gateway"
	saved, err = store.Save(c, saved.Version)
	if err != nil || saved.Revision != 1 || saved.Version != 2 {
		t.Fatal("background claim changed user revision", err, saved)
	}
	if _, err := store.Save(c, 1); !errors.Is(err, tailshare.ErrConflict) {
		t.Fatal("accepted stale storage writer", err)
	}
	loaded, err := store.Load()
	if err != nil || len(loaded.Owned) != 1 || !loaded.Enabled || loaded.Owner != c.Owner {
		t.Fatal("restart lost intent/ownership", err)
	}
	c.Owned[0].Target = "http://127.0.0.1:70000"
	if _, err := store.Save(c, 2); err == nil {
		t.Fatal("accepted corrupt target")
	}
}
