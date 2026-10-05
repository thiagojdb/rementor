package config

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/thiagojdb/rementor/internal/routing"
)

func TestFleetConfigurationPersistsAndRejectsStaleWrites(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	current, err := LoadFleetConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	if current.Revision != 0 || current.Topology.LocalInstance != "local" {
		t.Fatalf("unexpected initial config: %+v", current)
	}
	candidate := current.Topology
	candidate.Nodes[0].GatewayURL = "https://marcus.tailnet.example"
	saved, err := SaveFleetConfiguration(candidate, 0)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFleetConfiguration()
	if err != nil || loaded.Revision != 1 || loaded.Topology.Nodes[0].GatewayURL != candidate.Nodes[0].GatewayURL {
		t.Fatalf("save not persisted: %+v %v", loaded, err)
	}
	if _, err := SaveFleetConfiguration(candidate, 0); !errors.Is(err, ErrFleetConflict) {
		t.Fatalf("initial stale save: %v", err)
	}
	// Two independent writers reviewed revision 1; only one may commit.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := SaveFleetConfiguration(saved.Topology, 1); results <- err }()
	}
	wg.Wait()
	close(results)
	var successes, conflicts int
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrFleetConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("writes: %d successes %d conflicts", successes, conflicts)
	}
	loaded, err = LoadFleetConfiguration()
	if err != nil || loaded.Revision != 2 {
		t.Fatalf("revision: %+v %v", loaded, err)
	}
}

func TestInvalidFleetTopologyCannotReplaceSavedConfiguration(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	topology := initialFleetTopology()
	if _, err := SaveFleetConfiguration(topology, 0); err != nil {
		t.Fatal(err)
	}
	topology.Instances = append(topology.Instances, routing.Instance{ID: "other", NodeID: "this-device", Inherit: "local"})
	topology.Instances[0].Inherit = "other"
	if _, err := SaveFleetConfiguration(topology, 1); err == nil {
		t.Fatal("accepted inheritance cycle")
	}
	loaded, err := LoadFleetConfiguration()
	if err != nil || loaded.Revision != 1 || loaded.Topology.Instances[0].Inherit != "" {
		t.Fatalf("invalid write affected saved config: %+v %v", loaded, err)
	}
}
