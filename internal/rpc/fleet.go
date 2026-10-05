package rpc

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/thiagojdb/rementor/internal/config"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"github.com/thiagojdb/rementor/internal/routing"
)

func initializeFleetReviewKey(s *ControlPlaneService) {
	if _, err := rand.Read(s.fleetReviewKey[:]); err != nil {
		panic(fmt.Sprintf("initialize fleet review signing key: %v", err))
	}
}

func fleetFromProto(p *rementorv1.FleetTopology) (routing.Topology, error) {
	if p == nil {
		return routing.Topology{}, errors.New("fleet topology is required")
	}
	if len(p.Nodes) > 64 || len(p.Instances) > 128 || len(p.Origins) > 128 || len(p.Targets) > 2048 || len(p.Routes) > 512 || len(p.Instances)*len(p.Routes) > 32768 {
		return routing.Topology{}, errors.New("fleet plan is too large to review (64 computers, 128 instances, 128 origins, 2048 endpoints, 512 routes and 32768 selections maximum)")
	}
	t := routing.Topology{Version: int(p.Version), SharedInstance: p.SharedInstance, LocalInstance: p.LocalInstance}
	for _, n := range p.Nodes {
		t.Nodes = append(t.Nodes, routing.Node{ID: n.GetId(), GatewayURL: n.GetGatewayUrl()})
	}
	for _, o := range p.Origins {
		t.Origins = append(t.Origins, routing.Origin{ID: o.GetId(), URL: o.GetUrl()})
	}
	for _, v := range p.Targets {
		t.Targets = append(t.Targets, routing.Target{ID: v.GetId(), Kind: v.GetKind(), URL: v.GetUrl(), NodeID: v.GetNodeId(), RouteID: v.GetRouteId()})
	}
	for _, v := range p.Routes {
		t.Routes = append(t.Routes, routing.Route{ID: v.GetId(), OriginID: v.GetOriginId(), Path: v.GetPath(), Match: v.GetMatch(), AppID: v.GetAppId(), ServiceID: v.GetServiceId(), TargetID: v.GetTargetId()})
	}
	for _, v := range p.Instances {
		t.Instances = append(t.Instances, routing.Instance{ID: v.GetId(), NodeID: v.GetNodeId(), Inherit: v.GetInherit(), Overrides: v.GetOverrides()})
	}
	if _, err := routing.Compile(t); err != nil {
		return t, err
	}
	if t.LocalInstance == "" {
		return t, errors.New("choose the local instance")
	}
	// Bound the reviewed document independently of the transport message limit.
	raw, _ := json.Marshal(t)
	if len(raw) > 1024*1024 {
		return t, errors.New("fleet configuration exceeds 1 MiB")
	}
	return t, nil
}

func fleetTargetProto(t routing.Target) *rementorv1.FleetTarget {
	return &rementorv1.FleetTarget{Id: t.ID, Kind: t.Kind, Url: t.URL, NodeId: t.NodeID, RouteId: t.RouteID}
}
func fleetToProto(c config.FleetConfiguration) *rementorv1.FleetConfiguration {
	t := c.Topology
	p := &rementorv1.FleetTopology{Version: int32(t.Version), SharedInstance: t.SharedInstance, LocalInstance: t.LocalInstance}
	for _, n := range t.Nodes {
		p.Nodes = append(p.Nodes, &rementorv1.FleetNode{Id: n.ID, GatewayUrl: n.GatewayURL})
	}
	for _, o := range t.Origins {
		p.Origins = append(p.Origins, &rementorv1.FleetOrigin{Id: o.ID, Url: o.URL})
	}
	for _, v := range t.Targets {
		p.Targets = append(p.Targets, fleetTargetProto(v))
	}
	for _, v := range t.Routes {
		p.Routes = append(p.Routes, &rementorv1.FleetRoute{Id: v.ID, OriginId: v.OriginID, Path: v.Path, Match: v.Match, AppId: v.AppID, ServiceId: v.ServiceID, TargetId: v.TargetID})
	}
	for _, v := range t.Instances {
		p.Instances = append(p.Instances, &rementorv1.FleetInstance{Id: v.ID, NodeId: v.NodeID, Inherit: v.Inherit, Overrides: v.Overrides})
	}
	return &rementorv1.FleetConfiguration{Revision: c.Revision, Topology: p}
}

func fleetSelections(t routing.Topology) ([]*rementorv1.FleetRouteSelection, error) {
	resolver, err := routing.Compile(t)
	if err != nil {
		return nil, err
	}
	var selections []*rementorv1.FleetRouteSelection
	for _, instance := range t.Instances {
		for _, route := range t.Routes {
			result, err := resolver.InspectRoute(route.ID, instance.ID)
			if err != nil {
				return nil, err
			}
			selections = append(selections, &rementorv1.FleetRouteSelection{InstanceId: result.InstanceID, RouteId: result.RouteID, SelectionSource: result.SelectionSource, SourceInstance: result.SourceInstance, InstanceChain: result.InstanceChain, SelectedTargetId: result.SelectedTargetID, Endpoint: fleetTargetProto(result.Target), RouteChain: result.RouteChain})
		}
	}
	return selections, nil
}

func (s *ControlPlaneService) fleetReviewToken(t routing.Topology, revision uint64) string {
	payload, _ := json.Marshal(struct {
		Revision uint64
		Topology routing.Topology
	}{revision, t})
	digest := hmac.New(sha256.New, s.fleetReviewKey[:])
	_, _ = digest.Write(payload)
	return hex.EncodeToString(digest.Sum(nil))
}

func (s *ControlPlaneService) GetFleetConfiguration(ctx context.Context, req *connect.Request[rementorv1.GetFleetConfigurationRequest]) (*connect.Response[rementorv1.GetFleetConfigurationResponse], error) {
	current, err := config.LoadFleetConfiguration()
	if err != nil {
		return nil, newRPCError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&rementorv1.GetFleetConfigurationResponse{Configuration: fleetToProto(current)}), nil
}

func (s *ControlPlaneService) ReviewFleetConfiguration(ctx context.Context, req *connect.Request[rementorv1.ReviewFleetConfigurationRequest]) (*connect.Response[rementorv1.ReviewFleetConfigurationResponse], error) {
	topology, err := fleetFromProto(req.Msg.Topology)
	if err != nil {
		return nil, newRPCError(connect.CodeInvalidArgument, err)
	}
	current, err := config.LoadFleetConfiguration()
	if err != nil {
		return nil, newRPCError(connect.CodeInternal, err)
	}
	if current.Revision != req.Msg.ExpectedRevision {
		return nil, newRPCError(connect.CodeAborted, config.ErrFleetConflict)
	}
	before, err := fleetSelections(current.Topology)
	if err != nil {
		return nil, newRPCError(connect.CodeInternal, err)
	}
	after, err := fleetSelections(topology)
	if err != nil {
		return nil, newRPCError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&rementorv1.ReviewFleetConfigurationResponse{
		ReviewToken: s.fleetReviewToken(topology, current.Revision), Before: before, After: after,
		Warnings: []string{
			"Saving this topology stores fleet intent. Use workspace inheritance controls to change active routes.",
		},
	}), nil
}

func (s *ControlPlaneService) SaveFleetConfiguration(ctx context.Context, req *connect.Request[rementorv1.SaveFleetConfigurationRequest]) (*connect.Response[rementorv1.SaveFleetConfigurationResponse], error) {
	topology, err := fleetFromProto(req.Msg.Topology)
	if err != nil {
		return nil, newRPCError(connect.CodeInvalidArgument, err)
	}
	expected := s.fleetReviewToken(topology, req.Msg.ExpectedRevision)
	if !hmac.Equal([]byte(req.Msg.ReviewToken), []byte(expected)) {
		return nil, newRPCError(connect.CodeFailedPrecondition, errors.New("review these exact changes before saving"))
	}
	saved, err := config.SaveFleetConfiguration(topology, req.Msg.ExpectedRevision)
	if errors.Is(err, config.ErrFleetConflict) {
		return nil, newRPCError(connect.CodeAborted, err)
	}
	if err != nil {
		return nil, newRPCError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&rementorv1.SaveFleetConfigurationResponse{Configuration: fleetToProto(saved)}), nil
}
