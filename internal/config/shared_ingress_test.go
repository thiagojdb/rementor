package config

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/thiagojdb/rementor/internal/access"
	"github.com/thiagojdb/rementor/internal/shared"
)

func sharedPolicy() access.Document {
	return access.Document{SchemaVersion: 1, Origins: []access.Origin{{ID: "frontend", URL: "https://shared.example.test"}}, Bindings: []access.Binding{{WorkspaceID: "dev", OriginID: "frontend", LocalHost: "dev.giss.localhost"}}, AllowedUserIDs: []uint64{99}, SharedWorkspaceID: "dev"}
}
func TestSharedIngressSQLiteCASAndCapturedInstancePath(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "first"))
	first, err := NewSharedIngressStore()
	if err != nil {
		t.Fatal(err)
	}
	self := strings.Repeat("a", 64)
	a, err := shared.Open(self, first)
	if err != nil {
		t.Fatal(err)
	}
	record, err := a.Create(sharedPolicy(), self, []string{self}, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "second"))
	second, err := NewSharedIngressStore()
	if err != nil {
		t.Fatal(err)
	}
	if state, err := second.Load(); err != nil || state.Record != nil {
		t.Fatalf("new instance inherited another DB: %+v %v", state, err)
	}
	// Opening a fresh authority on the original store still reads the first
	// instance even after the process environment points at the second.
	restarted, err := shared.Open(self, first)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.State().Record.Digest != record.Digest {
		t.Fatal("restart lost the selected policy or identity")
	}
	state := restarted.State()
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := first.CompareAndSwap(state, state.Revision); results <- err }()
	}
	wg.Wait()
	close(results)
	winners, conflicts := 0, 0
	for err := range results {
		if err == nil {
			winners++
		} else if errors.Is(err, shared.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("CAS: %d winners, %d conflicts", winners, conflicts)
	}
	loaded, err := first.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Revision != state.Revision+1 || loaded.Record.Digest != record.Digest {
		t.Fatal("CAS altered selector content")
	}
}

func TestSharedIngressRestoreRejectsCorruptDurableRecord(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	store, err := NewSharedIngressStore()
	if err != nil {
		t.Fatal(err)
	}
	self := strings.Repeat("a", 64)
	a, err := shared.Open(self, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Create(sharedPolicy(), self, []string{self}, 0); err != nil {
		t.Fatal(err)
	}
	db, err := store.open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var raw string
	if err := db.QueryRow("SELECT state_json FROM shared_ingress WHERE id=1").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	for _, corrupt := range []string{strings.Replace(raw, "dev.giss.localhost", "changed.giss.localhost", 1), raw + " {}", strings.Replace(raw, `"revision":"1"`, `"revision":"0"`, 1)} {
		if _, err := db.Exec("UPDATE shared_ingress SET state_json=? WHERE id=1", corrupt); err != nil {
			t.Fatal(err)
		}
		if _, err := shared.Open(self, store); err == nil {
			t.Fatal("restored corrupt ownership or policy")
		}
	}
}
