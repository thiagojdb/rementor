package rpc

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"github.com/thiagojdb/rementor/internal/gen/rementor/v1/rementorv1connect"
)

func fleetTestTopology() *rementorv1.FleetTopology {
	return &rementorv1.FleetTopology{
		Version: 1, LocalInstance: "marcus-dev", SharedInstance: "brutus-dev",
		Nodes:     []*rementorv1.FleetNode{{Id: "marcus"}, {Id: "brutus", GatewayUrl: "https://brutus.tailnet.example"}},
		Origins:   []*rementorv1.FleetOrigin{{Id: "api", Url: "https://api.example.test"}},
		Targets:   []*rementorv1.FleetTarget{{Id: "remote", Kind: "remote", Url: "https://remote.example.test"}, {Id: "brutus-local", Kind: "node", NodeId: "brutus", Url: "http://127.0.0.1:8080"}, {Id: "marcus-local", Kind: "node", NodeId: "marcus", Url: "http://127.0.0.1:8081"}},
		Routes:    []*rementorv1.FleetRoute{{Id: "orders", OriginId: "api", Path: "/orders", Match: "prefix", AppId: "orders", ServiceId: "orders-api", TargetId: "remote"}},
		Instances: []*rementorv1.FleetInstance{{Id: "brutus-dev", NodeId: "brutus", Overrides: map[string]string{"orders": "brutus-local"}}, {Id: "marcus-dev", NodeId: "marcus", Inherit: "brutus-dev"}},
	}
}

func TestFleetReviewPreservesOwnerAndBindsExactSave(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	ctx := context.Background()
	s := NewControlPlaneService(nil)
	topology := fleetTestTopology()
	review, err := s.ReviewFleetConfiguration(ctx, connect.NewRequest(&rementorv1.ReviewFleetConfigurationRequest{Topology: topology}))
	if err != nil {
		t.Fatal(err)
	}
	inherited := review.Msg.After[1]
	if inherited.Endpoint.NodeId != "brutus" || inherited.Endpoint.Url != "http://127.0.0.1:8080" || inherited.SourceInstance != "brutus-dev" || inherited.SelectionSource != "inherited" {
		t.Fatalf("wrong inherited route: %+v", inherited)
	}
	topology.Nodes[1].GatewayUrl = "https://changed.example.test"
	_, err = s.SaveFleetConfiguration(ctx, connect.NewRequest(&rementorv1.SaveFleetConfigurationRequest{Topology: topology, ReviewToken: review.Msg.ReviewToken}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("accepted changed reviewed payload: %v", err)
	}
	topology.Nodes[1].GatewayUrl = "https://brutus.tailnet.example"
	saved, err := s.SaveFleetConfiguration(ctx, connect.NewRequest(&rementorv1.SaveFleetConfigurationRequest{Topology: topology, ReviewToken: review.Msg.ReviewToken}))
	if err != nil || saved.Msg.Configuration.Revision != 1 {
		t.Fatalf("save: %v %v", saved, err)
	}
	_, err = s.SaveFleetConfiguration(ctx, connect.NewRequest(&rementorv1.SaveFleetConfigurationRequest{Topology: topology, ReviewToken: review.Msg.ReviewToken}))
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("accepted replay over new revision: %v", err)
	}
	// The source changes to remote: Marcus follows. Its own override wins later.
	topology.Instances[0].Overrides["orders"] = "remote"
	review, err = s.ReviewFleetConfiguration(ctx, connect.NewRequest(&rementorv1.ReviewFleetConfigurationRequest{Topology: topology, ExpectedRevision: 1}))
	if err != nil || review.Msg.After[1].Endpoint.Id != "remote" {
		t.Fatalf("inheritance did not follow: %v %v", review, err)
	}
	topology.Instances[1].Overrides = map[string]string{"orders": "marcus-local"}
	review, err = s.ReviewFleetConfiguration(ctx, connect.NewRequest(&rementorv1.ReviewFleetConfigurationRequest{Topology: topology, ExpectedRevision: 1}))
	if err != nil || review.Msg.After[1].Endpoint.NodeId != "marcus" || review.Msg.After[1].SelectionSource != "instance" {
		t.Fatalf("override not selected: %v %v", review, err)
	}
	// A restart invalidates the old review token as well.
	restarted := NewControlPlaneService(nil)
	_, err = restarted.SaveFleetConfiguration(ctx, connect.NewRequest(&rementorv1.SaveFleetConfigurationRequest{Topology: topology, ExpectedRevision: 1, ReviewToken: review.Msg.ReviewToken}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("accepted token from old process: %v", err)
	}
}

func TestFleetReviewRejectsCyclesAndUnsafeGatewayAddresses(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	s := NewControlPlaneService(nil)
	for _, gateway := range []string{"http://brutus.example.test", "https://user:password@brutus.example.test", "https://brutus.example.test/management", "https://brutus.example.test?token=secret", "https://brutus.example.test/#fragment"} {
		topology := fleetTestTopology()
		topology.Nodes[1].GatewayUrl = gateway
		_, err := s.ReviewFleetConfiguration(context.Background(), connect.NewRequest(&rementorv1.ReviewFleetConfigurationRequest{Topology: topology}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("accepted gateway %q: %v", gateway, err)
		}
	}
	topology := fleetTestTopology()
	topology.Instances[0].Inherit = "marcus-dev"
	_, err := s.ReviewFleetConfiguration(context.Background(), connect.NewRequest(&rementorv1.ReviewFleetConfigurationRequest{Topology: topology}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("accepted inheritance cycle: %v", err)
	}
}

func TestFleetSaveRequiresBrowserCSRF(t *testing.T) {
	header := http.Header{"Origin": []string{"http://rementor.localhost"}, "Sec-Fetch-Site": []string{"same-origin"}}
	guard := NewCSRFGuard("review-browser-token")
	procedure := rementorv1connect.ControlPlaneServiceSaveFleetConfigurationProcedure
	if err := guard.validate(procedure, header); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("save without token: %v", err)
	}
	header.Set(CSRFHeader, "review-browser-token")
	if err := guard.validate(procedure, header); err != nil {
		t.Fatal(err)
	}
	header.Set("Origin", "https://foreign.example.test")
	if err := guard.validate(procedure, header); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("foreign origin: %v", err)
	}
}
