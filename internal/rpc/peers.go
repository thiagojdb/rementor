package rpc

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"github.com/thiagojdb/rementor/internal/peers"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *ControlPlaneService) SetPeerManager(manager *peers.Manager) { s.peers = manager }
func (s *ControlPlaneService) peerManager() (*peers.Manager, error) {
	if s.peers == nil {
		return nil, newRPCError(connect.CodeFailedPrecondition, errors.New("peer connections are unavailable in this server"))
	}
	return s.peers, nil
}
func peerProto(p peers.Peer) *rementorv1.PairedComputer {
	return &rementorv1.PairedComputer{Fingerprint: p.Fingerprint, Name: p.Name, Url: p.URL}
}
func peerStateProto(state peers.State) *rementorv1.GetPeerStateResponse {
	result := &rementorv1.GetPeerStateResponse{Name: state.Name, Fingerprint: state.Fingerprint, ListenAddress: state.Listen, Url: state.URL, Active: state.Active, ListenerError: state.Error, PrivateAddresses: state.Addresses}
	for _, p := range state.Peers {
		result.Computers = append(result.Computers, peerProto(p))
	}
	return result
}
func (s *ControlPlaneService) GetPeerState(ctx context.Context, req *connect.Request[rementorv1.GetPeerStateRequest]) (*connect.Response[rementorv1.GetPeerStateResponse], error) {
	manager, err := s.peerManager()
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(peerStateProto(manager.State())), nil
}
func (s *ControlPlaneService) ConfigurePeerConnection(ctx context.Context, req *connect.Request[rementorv1.ConfigurePeerConnectionRequest]) (*connect.Response[rementorv1.ConfigurePeerConnectionResponse], error) {
	manager, err := s.peerManager()
	if err != nil {
		return nil, err
	}
	if err = manager.Configure(req.Msg.ListenAddress, req.Msg.Url); err != nil {
		return nil, newRPCError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&rementorv1.ConfigurePeerConnectionResponse{State: peerStateProto(manager.State())}), nil
}
func (s *ControlPlaneService) CreatePeerInvitation(ctx context.Context, req *connect.Request[rementorv1.CreatePeerInvitationRequest]) (*connect.Response[rementorv1.CreatePeerInvitationResponse], error) {
	manager, err := s.peerManager()
	if err != nil {
		return nil, err
	}
	code, invitation, err := manager.CreateInvitation()
	if err != nil {
		return nil, newRPCError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&rementorv1.CreatePeerInvitationResponse{Invitation: code, Fingerprint: invitation.Fingerprint, ExpiresAt: timestamppb.New(invitation.ExpiresAt)}), nil
}
func (s *ControlPlaneService) CancelPeerInvitation(ctx context.Context, req *connect.Request[rementorv1.CancelPeerInvitationRequest]) (*connect.Response[rementorv1.CancelPeerInvitationResponse], error) {
	manager, err := s.peerManager()
	if err != nil {
		return nil, err
	}
	manager.CancelInvitation()
	return connect.NewResponse(&rementorv1.CancelPeerInvitationResponse{}), nil
}
func (s *ControlPlaneService) InspectPeerInvitation(ctx context.Context, req *connect.Request[rementorv1.InspectPeerInvitationRequest]) (*connect.Response[rementorv1.InspectPeerInvitationResponse], error) {
	invitation, err := peers.ParseInvitation(req.Msg.Invitation, time.Now())
	if err != nil {
		return nil, newRPCError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&rementorv1.InspectPeerInvitationResponse{Name: invitation.Name, Url: invitation.URL, Fingerprint: invitation.Fingerprint, ExpiresAt: timestamppb.New(invitation.ExpiresAt)}), nil
}
func (s *ControlPlaneService) PairComputer(ctx context.Context, req *connect.Request[rementorv1.PairComputerRequest]) (*connect.Response[rementorv1.PairComputerResponse], error) {
	manager, err := s.peerManager()
	if err != nil {
		return nil, err
	}
	p, err := manager.Pair(ctx, req.Msg.Invitation, req.Msg.ConfirmedFingerprint)
	if err != nil {
		return nil, newRPCError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&rementorv1.PairComputerResponse{Computer: peerProto(p)}), nil
}
func (s *ControlPlaneService) UnpairComputer(ctx context.Context, req *connect.Request[rementorv1.UnpairComputerRequest]) (*connect.Response[rementorv1.UnpairComputerResponse], error) {
	manager, err := s.peerManager()
	if err != nil {
		return nil, err
	}
	if err = manager.Revoke(req.Msg.Fingerprint); err != nil {
		return nil, newRPCError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&rementorv1.UnpairComputerResponse{State: peerStateProto(manager.State())}), nil
}
func (s *ControlPlaneService) CheckPeerConnection(ctx context.Context, req *connect.Request[rementorv1.CheckPeerConnectionRequest]) (*connect.Response[rementorv1.CheckPeerConnectionResponse], error) {
	manager, err := s.peerManager()
	if err != nil {
		return nil, err
	}
	if err = manager.Check(ctx, req.Msg.Fingerprint); err != nil {
		return nil, newRPCError(connect.CodeUnavailable, err)
	}
	return connect.NewResponse(&rementorv1.CheckPeerConnectionResponse{}), nil
}
