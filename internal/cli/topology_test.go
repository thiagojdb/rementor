package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/thiagojdb/rementor/internal/routing"
)

func TestTopologyCLIInspectAndUnavailableExit(t *testing.T) {
	args := []string{"resolve", "--file", "../../examples/fleet/topology.json", "--instance", "marcus-dev", "--url", "https://services.rementor.example.test/orders"}
	var output bytes.Buffer
	if err := runTopology(&output, true, args); err != nil {
		t.Fatal(err)
	}
	var response struct {
		Status     string             `json:"status"`
		Resolution routing.Resolution `json:"resolution"`
		Error      string             `json:"error"`
	}
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "selected" || response.Resolution.Target.NodeID != "brutus" || response.Resolution.SourceInstance != "brutus-dev" {
		t.Fatalf("unexpected output: %s", output.String())
	}
	output.Reset()
	if err := runTopology(&output, true, append(args, "--unavailable-nodes", "brutus")); !errors.Is(err, routing.ErrTargetUnavailable) {
		t.Fatalf("expected failure, got %v", err)
	}
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "unavailable" || response.Error == "" || response.Resolution.Target.ID != "orders-brutus" {
		t.Fatalf("failure omitted selected target: %s", output.String())
	}
}
