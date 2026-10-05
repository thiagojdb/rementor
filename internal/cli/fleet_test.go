package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"github.com/thiagojdb/rementor/internal/gen/rementor/v1/rementorv1connect"
	"github.com/thiagojdb/rementor/internal/rpc"
)

func fleetClientServer(t *testing.T, service rementorv1connect.ControlPlaneServiceHandler) *Client {
	t.Helper()
	path, handler := rementorv1connect.NewControlPlaneServiceHandler(service)
	mux := http.NewServeMux()
	mux.Handle("/rpc"+path, http.StripPrefix("/rpc", handler))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return NewClient(server.URL)
}

func TestFleetCLIReviewAndMCPSaveRequireExactCandidate(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	service := rpc.NewControlPlaneService(nil)
	client := fleetClientServer(t, service)
	file := filepath.Join(t.TempDir(), "topology.json")
	const original = `{"version":1,"nodes":[{"id":"marcus"},{"id":"brutus"}],"origins":[{"id":"api","url":"https://api.example.test"}],"targets":[{"id":"remote","kind":"remote","url":"https://remote.example.test"},{"id":"owned","kind":"node","nodeId":"brutus","url":"http://127.0.0.1:8080"}],"routes":[{"id":"orders","originId":"api","path":"/orders","match":"prefix","appId":"orders","serviceId":"orders-api","targetId":"remote"}],"instances":[{"id":"source","nodeId":"brutus","overrides":{"orders":"owned"}},{"id":"receiver","nodeId":"marcus","inherit":"source"}],"localInstance":"receiver","sharedInstance":"source"}`
	if err := os.WriteFile(file, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runFleet(client, &out, []string{"show"}); err != nil {
		t.Fatal(err)
	}
	var initial struct {
		Configuration struct {
			Revision string `json:"revision"`
		} `json:"configuration"`
	}
	if err := json.Unmarshal(out.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	if initial.Configuration.Revision != "0" {
		t.Fatalf("initial revision missing: %s", &out)
	}
	for _, args := range [][]string{
		{"review", "--file", file},
		{"save", "--file", file, "--expected-revision", "0"},
	} {
		if err := runFleet(client, &out, args); err == nil {
			t.Fatalf("accepted incomplete review/save: %v", args)
		}
	}
	out.Reset()
	if err := runFleet(client, &out, []string{"review", "--file", file, "--expected-revision", "0"}); err != nil {
		t.Fatal(err)
	}
	var review struct {
		ReviewToken string `json:"reviewToken"`
		After       []struct {
			InstanceID string `json:"instanceId"`
			Source     string `json:"selectionSource"`
			Endpoint   struct {
				NodeID string `json:"nodeId"`
			} `json:"endpoint"`
		} `json:"after"`
	}
	if err := json.Unmarshal(out.Bytes(), &review); err != nil {
		t.Fatal(err)
	}
	if review.ReviewToken == "" || len(review.After) != 2 || review.After[1].InstanceID != "receiver" || review.After[1].Source != "inherited" || review.After[1].Endpoint.NodeID != "brutus" {
		t.Fatalf("review lost owner/inheritance: %s", &out)
	}
	var topology map[string]any
	if err := json.Unmarshal([]byte(original), &topology); err != nil {
		t.Fatal(err)
	}
	mcp := &mcpServer{client: client}
	call := func(name string, args map[string]any) (any, error) {
		raw, err := json.Marshal(map[string]any{"name": name, "arguments": args})
		if err != nil {
			t.Fatal(err)
		}
		return mcp.handleToolCall(raw)
	}
	args := map[string]any{"expected_revision": "0", "review_token": review.ReviewToken, "topology": topology}
	topology["sharedInstance"] = "receiver"
	if _, err := call("rementor.fleet_save", args); err == nil {
		t.Fatal("MCP accepted a changed reviewed topology")
	}
	topology["sharedInstance"] = "source"
	if _, err := call("rementor.fleet_save", args); err != nil {
		t.Fatal(err)
	}
	if _, err := call("rementor.fleet_save", args); err == nil {
		t.Fatal("MCP accepted stale revision replay")
	}
	// A new service reads the persisted plan but must not accept the old review.
	restarted := fleetClientServer(t, rpc.NewControlPlaneService(nil))
	out.Reset()
	if err := runFleet(restarted, &out, []string{"show"}); err != nil {
		t.Fatal(err)
	}
	var loaded struct {
		Configuration struct {
			Revision string `json:"revision"`
			Topology struct {
				Instances []struct {
					Inherit string `json:"inherit"`
				} `json:"instances"`
			} `json:"topology"`
		} `json:"configuration"`
	}
	if err := json.Unmarshal(out.Bytes(), &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.Configuration.Revision != "1" || len(loaded.Configuration.Topology.Instances) != 2 || loaded.Configuration.Topology.Instances[1].Inherit != "source" {
		t.Fatalf("restart lost saved plan: %s", &out)
	}
	if err := runFleet(restarted, &out, []string{"save", "--file", file, "--expected-revision", "0", "--review-token", review.ReviewToken}); err == nil {
		t.Fatal("restart accepted an old process review")
	}
	args["expected_revision"] = "1"
	result, err := call("rementor.fleet_review", args)
	if err != nil {
		t.Fatal(err)
	}
	args["review_token"] = result.(map[string]any)["reviewToken"]
	if _, err := call("rementor.fleet_save", args); err != nil {
		t.Fatal(err)
	}
}

type fleetPrecisionWire struct {
	rementorv1connect.UnimplementedControlPlaneServiceHandler
	request *rementorv1.ReviewFleetConfigurationRequest
}

func (s *fleetPrecisionWire) ReviewFleetConfiguration(ctx context.Context, req *connect.Request[rementorv1.ReviewFleetConfigurationRequest]) (*connect.Response[rementorv1.ReviewFleetConfigurationResponse], error) {
	s.request = req.Msg
	return connect.NewResponse(&rementorv1.ReviewFleetConfigurationResponse{ReviewToken: "reviewed"}), nil
}

func (s *fleetPrecisionWire) GetFleetConfiguration(ctx context.Context, req *connect.Request[rementorv1.GetFleetConfigurationRequest]) (*connect.Response[rementorv1.GetFleetConfigurationResponse], error) {
	return connect.NewResponse(&rementorv1.GetFleetConfigurationResponse{Configuration: &rementorv1.FleetConfiguration{Revision: 18446744073709551614}}), nil
}

func TestFleetControlsKeepRevisionPrecisionAndRejectInvalidInput(t *testing.T) {
	wire := &fleetPrecisionWire{}
	client := fleetClientServer(t, wire)
	file := filepath.Join(t.TempDir(), "topology.json")
	if err := os.WriteFile(file, []byte(`{"version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runFleet(client, &out, []string{"review", "--file", file, "--expected-revision", "18446744073709551614"}); err != nil {
		t.Fatal(err)
	}
	if wire.request.ExpectedRevision != 18446744073709551614 {
		t.Fatal("CLI lost revision precision")
	}
	mcp := &mcpServer{client: client}
	if _, err := mcp.toolFleet("rementor.fleet_review", map[string]any{"expected_revision": "18446744073709551614", "topology": map[string]any{"version": 1}}); err != nil {
		t.Fatal(err)
	}
	if wire.request.ExpectedRevision != 18446744073709551614 {
		t.Fatal("MCP lost revision precision")
	}
	shown, err := mcp.toolFleet("rementor.fleet_show", nil)
	if err != nil {
		t.Fatal(err)
	}
	if shown.(map[string]any)["configuration"].(map[string]any)["revision"] != "18446744073709551614" {
		t.Fatal("MCP response lost revision precision")
	}
	for _, revision := range []any{nil, float64(1), "-1", "18446744073709551616"} {
		if _, err := mcp.toolFleet("rementor.fleet_review", map[string]any{"expected_revision": revision, "topology": map[string]any{"version": 1}}); err == nil {
			t.Fatalf("accepted invalid revision %v", revision)
		}
	}
	previous := wire.request
	if err := os.WriteFile(file, []byte(`{"version":1,"typo":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runFleet(client, &out, []string{"review", "--file", file, "--expected-revision", "0"}); err == nil || wire.request != previous {
		t.Fatal("sent malformed topology")
	}
	if err := os.WriteFile(file, []byte(strings.Repeat(" ", (1<<20)+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runFleet(client, &out, []string{"review", "--file", file, "--expected-revision", "0"}); err == nil || wire.request != previous {
		t.Fatal("sent oversized topology")
	}
	found := map[string]bool{}
	for _, tool := range mcpToolList() {
		found[tool["name"].(string)] = true
	}
	for _, name := range []string{"rementor.fleet_show", "rementor.fleet_review", "rementor.fleet_save"} {
		if !found[name] {
			t.Fatalf("tool missing from discovery: %s", name)
		}
	}
}
