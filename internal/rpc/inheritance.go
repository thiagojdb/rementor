package rpc

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/thiagojdb/rementor/internal/fleet"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"github.com/thiagojdb/rementor/internal/models"
	"github.com/thiagojdb/rementor/internal/services"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func peerScopeProto(scope models.PeerScope) *rementorv1.PeerWorkspaceScope {
	return &rementorv1.PeerWorkspaceScope{WorkspaceId: scope.WorkspaceID, EnvironmentId: scope.EnvironmentID, SessionId: scope.SessionID}
}
func peerReferenceProto(ref models.PeerWorkspaceReference) *rementorv1.PeerWorkspaceReference {
	return &rementorv1.PeerWorkspaceReference{OwnerFingerprint: ref.OwnerFingerprint, Scope: peerScopeProto(ref.Scope), Version: ref.Version, Digest: ref.Digest}
}
func peerEndpointProto(ref *models.PeerEndpoint) *rementorv1.PeerEndpoint {
	if ref == nil {
		return nil
	}
	return &rementorv1.PeerEndpoint{OwnerFingerprint: ref.OwnerFingerprint, WorkspaceId: ref.WorkspaceID, ApplicationId: ref.ApplicationID, Version: ref.Version, Digest: ref.Digest}
}
func inheritanceProto(state *models.WorkspaceInheritance) *rementorv1.WorkspaceInheritance {
	if state == nil {
		return nil
	}
	result := &rementorv1.WorkspaceInheritance{Source: peerReferenceProto(state.Source), UpdatedAt: timestamppb.New(state.UpdatedAt), Overrides: map[string]*rementorv1.InheritedRouteSelection{}}
	if len(state.Snapshot) > 0 {
		if snapshot, err := fleet.CachedSnapshot(state); err == nil {
			result.SourceName = snapshot.Intent.Name
		}
	}
	for id, choice := range state.Overrides {
		result.Overrides[id] = &rementorv1.InheritedRouteSelection{Mode: choice.Mode, Port: int32(choice.Port), RemoteBaseUrl: choice.RemoteBaseURL, RoutePattern: clonePattern(choice.RoutePattern)}
	}
	return result
}
func clonePattern(pattern *string) *string {
	if pattern == nil {
		return nil
	}
	value := *pattern
	return &value
}
func (s *ControlPlaneService) inheritanceStatusProto(id string) (*rementorv1.WorkspaceInheritanceStatus, error) {
	status, err := s.registry.InheritanceStatus(id)
	if err != nil {
		return nil, inheritanceRPCError(err)
	}
	result := &rementorv1.WorkspaceInheritanceStatus{Status: status.Status, Error: status.Error, RouteVersion: status.RouteVersion}
	if status.Status != "independent" {
		result.Inheritance = inheritanceProto(&models.WorkspaceInheritance{Source: status.Source, Overrides: status.Overrides, UpdatedAt: status.UpdatedAt})
		result.Inheritance.SourceName = status.SourceName
	}
	if !status.CheckedAt.IsZero() {
		result.CheckedAt = timestamppb.New(status.CheckedAt)
	}
	return result, nil
}
func inheritanceRPCError(err error) error {
	code := connect.CodeInvalidArgument
	switch {
	case errors.Is(err, services.ErrInheritanceConflict), errors.Is(err, fleet.ErrConfigurationConflict):
		code = connect.CodeAborted
	case errors.Is(err, fleet.ErrWorkspaceNotFound):
		code = connect.CodeNotFound
	case errors.Is(err, context.DeadlineExceeded):
		code = connect.CodeDeadlineExceeded
	case errors.Is(err, context.Canceled):
		code = connect.CodeCanceled
	default:
		var transaction *services.RouteTransactionError
		if errors.As(err, &transaction) {
			code = connect.CodeInternal
		}
	}
	return newRPCError(code, err)
}
func (s *ControlPlaneService) ListPeerWorkspaces(ctx context.Context, req *connect.Request[rementorv1.ListPeerWorkspacesRequest]) (*connect.Response[rementorv1.ListPeerWorkspacesResponse], error) {
	manager, err := s.peerManager()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	catalog, err := manager.PeerWorkspaces(ctx, req.Msg.Fingerprint)
	if err != nil {
		return nil, newRPCError(connect.CodeUnavailable, err)
	}
	result := &rementorv1.ListPeerWorkspacesResponse{}
	for _, workspace := range catalog.Workspaces {
		result.Workspaces = append(result.Workspaces, &rementorv1.PeerWorkspaceSummary{Scope: peerScopeProto(workspace.Scope), Name: workspace.Name, Type: workspace.Type, Version: workspace.Version})
	}
	return connect.NewResponse(result), nil
}
func (s *ControlPlaneService) GetWorkspaceInheritance(ctx context.Context, req *connect.Request[rementorv1.GetWorkspaceInheritanceRequest]) (*connect.Response[rementorv1.GetWorkspaceInheritanceResponse], error) {
	status, err := s.inheritanceStatusProto(req.Msg.WorkspaceId)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&rementorv1.GetWorkspaceInheritanceResponse{Status: status}), nil
}
func (s *ControlPlaneService) FollowPeerWorkspace(ctx context.Context, req *connect.Request[rementorv1.FollowPeerWorkspaceRequest]) (*connect.Response[rementorv1.FollowPeerWorkspaceResponse], error) {
	if req.Msg.Scope == nil {
		return nil, newRPCError(connect.CodeInvalidArgument, errors.New("choose an environment or session"))
	}
	scope := req.Msg.Scope
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result, err := s.registry.FollowPeerWorkspace(ctx, req.Msg.WorkspaceId, req.Msg.Fingerprint, models.PeerScope{WorkspaceID: scope.WorkspaceId, EnvironmentID: scope.EnvironmentId, SessionID: scope.SessionId}, req.Msg.ExpectedRouteVersion, correlationID(req.Msg.CorrelationId, req.Header()))
	if err != nil {
		return nil, inheritanceRPCError(err)
	}
	status, err := s.inheritanceStatusProto(req.Msg.WorkspaceId)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&rementorv1.FollowPeerWorkspaceResponse{Workspace: s.workspaceProto(result.Workspace), Operation: toProtoOperation(result.Operation), Changed: result.Changed, Status: status}), nil
}
func (s *ControlPlaneService) SyncPeerWorkspace(ctx context.Context, req *connect.Request[rementorv1.SyncPeerWorkspaceRequest]) (*connect.Response[rementorv1.SyncPeerWorkspaceResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result, err := s.registry.SyncPeerWorkspace(ctx, req.Msg.WorkspaceId)
	if err != nil {
		return nil, inheritanceRPCError(err)
	}
	status, err := s.inheritanceStatusProto(req.Msg.WorkspaceId)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&rementorv1.SyncPeerWorkspaceResponse{Workspace: s.workspaceProto(result.Workspace), Operation: toProtoOperation(result.Operation), Changed: result.Changed, Status: status}), nil
}
func (s *ControlPlaneService) OverrideInheritedRoute(ctx context.Context, req *connect.Request[rementorv1.OverrideInheritedRouteRequest]) (*connect.Response[rementorv1.OverrideInheritedRouteResponse], error) {
	var selection *models.RouteSelection
	if p := req.Msg.Selection; p != nil {
		selection = &models.RouteSelection{Mode: p.Mode, Port: int(p.Port), RemoteBaseURL: p.RemoteBaseUrl, RoutePattern: clonePattern(p.RoutePattern)}
	}
	result, err := s.registry.OverrideInheritedRoute(req.Msg.WorkspaceId, req.Msg.ApplicationId, selection, req.Msg.ExpectedRouteVersion, correlationID(req.Msg.CorrelationId, req.Header()))
	if err != nil {
		return nil, inheritanceRPCError(err)
	}
	status, err := s.inheritanceStatusProto(req.Msg.WorkspaceId)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&rementorv1.OverrideInheritedRouteResponse{Workspace: s.workspaceProto(result.Workspace), Operation: toProtoOperation(result.Operation), Changed: result.Changed, Status: status}), nil
}
func (s *ControlPlaneService) StopFollowingWorkspace(ctx context.Context, req *connect.Request[rementorv1.StopFollowingWorkspaceRequest]) (*connect.Response[rementorv1.StopFollowingWorkspaceResponse], error) {
	result, err := s.registry.StopFollowingWorkspace(req.Msg.WorkspaceId, req.Msg.ExpectedRouteVersion, correlationID(req.Msg.CorrelationId, req.Header()))
	if err != nil {
		return nil, inheritanceRPCError(err)
	}
	status, err := s.inheritanceStatusProto(req.Msg.WorkspaceId)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&rementorv1.StopFollowingWorkspaceResponse{Workspace: s.workspaceProto(result.Workspace), Operation: toProtoOperation(result.Operation), Changed: result.Changed, Status: status}), nil
}
