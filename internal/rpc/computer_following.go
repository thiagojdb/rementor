package rpc

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/thiagojdb/rementor/internal/fleet"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"sort"
	"time"
)

func (s *ControlPlaneService) computerFollowingProto() *rementorv1.ComputerFollowingStatus {
	state, check := s.registry.ComputerFollowing()
	result := &rementorv1.ComputerFollowingStatus{Revision: state.Revision, OwnerFingerprint: state.OwnerFingerprint, Status: check.Status, Error: check.Error}
	if state.OwnerFingerprint != "" && check.Status == "" {
		result.Status = "stale"
	}
	if !check.CheckedAt.IsZero() {
		result.CheckedAt = timestamppb.New(check.CheckedAt)
	}
	var snapshot fleet.ConfigurationSnapshot
	if json.Unmarshal(state.Snapshot, &snapshot) == nil {
		for _, environment := range snapshot.Environments {
			id := environment.Snapshot.Intent.Scope.WorkspaceID
			if !state.Excluded[id] {
				result.EnvironmentIds = append(result.EnvironmentIds, id)
			}
		}
	}
	for id := range state.Excluded {
		result.ExcludedEnvironmentIds = append(result.ExcludedEnvironmentIds, id)
	}
	sort.Strings(result.ExcludedEnvironmentIds)
	return result
}

func (s *ControlPlaneService) SetEnvironmentFollowing(ctx context.Context, req *connect.Request[rementorv1.SetEnvironmentFollowingRequest]) (*connect.Response[rementorv1.SetEnvironmentFollowingResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := s.registry.SetEnvironmentFollowing(ctx, req.Msg.WorkspaceId, req.Msg.Following, req.Msg.ExpectedRevision, req.Msg.ExpectedWorkspaceVersion); err != nil {
		return nil, inheritanceRPCError(err)
	}
	return connect.NewResponse(&rementorv1.SetEnvironmentFollowingResponse{State: s.computerFollowingProto()}), nil
}
func (s *ControlPlaneService) GetComputerFollowing(ctx context.Context, req *connect.Request[rementorv1.GetComputerFollowingRequest]) (*connect.Response[rementorv1.GetComputerFollowingResponse], error) {
	return connect.NewResponse(&rementorv1.GetComputerFollowingResponse{State: s.computerFollowingProto()}), nil
}
func (s *ControlPlaneService) FollowComputer(ctx context.Context, req *connect.Request[rementorv1.FollowComputerRequest]) (*connect.Response[rementorv1.FollowComputerResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := s.registry.FollowComputer(ctx, req.Msg.Fingerprint, req.Msg.ExpectedRevision); err != nil {
		return nil, inheritanceRPCError(err)
	}
	return connect.NewResponse(&rementorv1.FollowComputerResponse{State: s.computerFollowingProto()}), nil
}
func (s *ControlPlaneService) SyncComputerConfiguration(ctx context.Context, req *connect.Request[rementorv1.SyncComputerConfigurationRequest]) (*connect.Response[rementorv1.SyncComputerConfigurationResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := s.registry.SyncComputerConfiguration(ctx); err != nil {
		return nil, inheritanceRPCError(err)
	}
	return connect.NewResponse(&rementorv1.SyncComputerConfigurationResponse{State: s.computerFollowingProto()}), nil
}
func (s *ControlPlaneService) StopFollowingComputer(ctx context.Context, req *connect.Request[rementorv1.StopFollowingComputerRequest]) (*connect.Response[rementorv1.StopFollowingComputerResponse], error) {
	if err := s.registry.StopFollowingComputer(req.Msg.ExpectedRevision); err != nil {
		return nil, inheritanceRPCError(err)
	}
	return connect.NewResponse(&rementorv1.StopFollowingComputerResponse{State: s.computerFollowingProto()}), nil
}
