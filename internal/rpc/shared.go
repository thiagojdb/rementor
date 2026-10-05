package rpc

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/thiagojdb/rementor/internal/access"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"github.com/thiagojdb/rementor/internal/shared"
)

func (s *ControlPlaneService) SetSharedIngress(authority *shared.Authority) {
	s.sharedIngress = authority
}

func sharedRecordProto(record *shared.Record) *rementorv1.SharedIngressRecord {
	if record == nil {
		return nil
	}
	return &rementorv1.SharedIngressRecord{SchemaVersion: uint32(record.SchemaVersion), GroupId: record.GroupID, Version: record.Version, Epoch: record.Epoch, OwnerFingerprint: record.Owner, RoutingOwnerFingerprint: record.RoutingOwner, Gateways: append([]string(nil), record.Gateways...), Policy: accessProto(access.Configuration{Document: record.Policy}).Document, PreviousOwnerFingerprint: record.PreviousOwner, PredecessorDigest: record.Predecessor, Digest: record.Digest}
}

func (s *ControlPlaneService) sharedStateProto() *rementorv1.SharedIngressState {
	state := s.sharedIngress.State()
	result := &rementorv1.SharedIngressState{Revision: state.Revision, Record: sharedRecordProto(state.Record), SelfFingerprint: s.peers.State().Fingerprint}
	if state.Pending != nil {
		p := state.Pending
		result.Pending = &rementorv1.SharedIngressPending{Before: sharedRecordProto(&p.Before), Next: sharedRecordProto(&p.Next), Outgoing: p.Outgoing, Committed: p.Committed}
	}
	return result
}

func sharedRPCError(err error) error {
	code := connect.CodeFailedPrecondition
	if errors.Is(err, shared.ErrConflict) || errors.Is(err, shared.ErrTransferPending) {
		code = connect.CodeAborted
	} else if errors.Is(err, shared.ErrNotMember) || errors.Is(err, shared.ErrNotOwner) {
		code = connect.CodePermissionDenied
	}
	return newRPCError(code, err)
}

func (s *ControlPlaneService) requireShared() error {
	if s.sharedIngress == nil || s.peers == nil {
		return sharedRPCError(shared.ErrUnavailable)
	}
	return nil
}

// All peer references must already be enrolled. Workspace IDs and host bindings
// are validated on the declared routing computer before publishing a candidate.
func (s *ControlPlaneService) sharedConfiguration(ctx context.Context, input *rementorv1.SharedIngressConfiguration) (access.Document, error) {
	if input == nil {
		return access.Document{}, newRPCError(connect.CodeInvalidArgument, errors.New("shared ingress configuration is required"))
	}
	doc, err := accessDocument(input.Policy)
	if err != nil || len(doc.Origins) == 0 {
		if err == nil {
			err = errors.New("shared ingress requires published origins")
		}
		return doc, newRPCError(connect.CodeInvalidArgument, err)
	}
	state := s.peers.State()
	if !state.Active {
		return doc, sharedRPCError(errors.New("pairing connection is disabled"))
	}
	known := map[string]bool{state.Fingerprint: true}
	for _, peer := range state.Peers {
		known[peer.Fingerprint] = true
	}
	if !known[input.RoutingOwnerFingerprint] {
		return doc, newRPCError(connect.CodeInvalidArgument, errors.New("routing computer is not paired"))
	}
	seen := map[string]bool{}
	for _, id := range input.Gateways {
		if !known[id] || seen[id] {
			return doc, newRPCError(connect.CodeInvalidArgument, errors.New("gateway must be a distinct paired computer"))
		}
		seen[id] = true
	}
	if !seen[state.Fingerprint] || len(seen) > 64 {
		return doc, newRPCError(connect.CodeInvalidArgument, errors.New("include this selector computer in the gateways"))
	}
	if input.RoutingOwnerFingerprint == state.Fingerprint {
		err = s.registry.ValidateSharedPublicPolicy(doc)
	} else {
		check, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = s.peers.ValidateSharedPolicy(check, input.RoutingOwnerFingerprint, doc)
		cancel()
	}
	if err != nil {
		return doc, newRPCError(connect.CodeInvalidArgument, err)
	}
	if s.publicIngress == nil {
		return doc, sharedRPCError(errors.New("public data plane is unavailable"))
	}
	if err := s.publicIngress.Start(); err != nil {
		return doc, sharedRPCError(err)
	}
	return doc, nil
}

func (s *ControlPlaneService) GetSharedIngress(ctx context.Context, req *connect.Request[rementorv1.GetSharedIngressRequest]) (*connect.Response[rementorv1.GetSharedIngressResponse], error) {
	if err := s.requireShared(); err != nil {
		return nil, err
	}
	return connect.NewResponse(&rementorv1.GetSharedIngressResponse{State: s.sharedStateProto()}), nil
}

func (s *ControlPlaneService) CreateSharedIngress(ctx context.Context, req *connect.Request[rementorv1.CreateSharedIngressRequest]) (*connect.Response[rementorv1.CreateSharedIngressResponse], error) {
	if err := s.requireShared(); err != nil {
		return nil, err
	}
	doc, err := s.sharedConfiguration(ctx, req.Msg.Configuration)
	if err != nil {
		return nil, err
	}
	_, err = s.sharedIngress.Create(doc, req.Msg.Configuration.RoutingOwnerFingerprint, req.Msg.Configuration.Gateways, req.Msg.ExpectedRevision)
	if err != nil {
		return nil, sharedRPCError(err)
	}
	return connect.NewResponse(&rementorv1.CreateSharedIngressResponse{State: s.sharedStateProto()}), nil
}

func (s *ControlPlaneService) SaveSharedIngress(ctx context.Context, req *connect.Request[rementorv1.SaveSharedIngressRequest]) (*connect.Response[rementorv1.SaveSharedIngressResponse], error) {
	if err := s.requireShared(); err != nil {
		return nil, err
	}
	state := s.sharedIngress.State()
	if state.Record == nil || state.Record.Owner != s.peers.State().Fingerprint {
		return nil, sharedRPCError(shared.ErrNotOwner)
	}
	doc, err := s.sharedConfiguration(ctx, req.Msg.Configuration)
	if err != nil {
		return nil, err
	}
	_, err = s.sharedIngress.Update(doc, req.Msg.Configuration.RoutingOwnerFingerprint, req.Msg.Configuration.Gateways, req.Msg.ExpectedVersion)
	if err != nil {
		return nil, sharedRPCError(err)
	}
	return connect.NewResponse(&rementorv1.SaveSharedIngressResponse{State: s.sharedStateProto()}), nil
}

func (s *ControlPlaneService) JoinSharedIngress(ctx context.Context, req *connect.Request[rementorv1.JoinSharedIngressRequest]) (*connect.Response[rementorv1.JoinSharedIngressResponse], error) {
	if err := s.requireShared(); err != nil {
		return nil, err
	}
	if _, err := s.sharedIngress.Join(ctx, req.Msg.OwnerFingerprint, req.Msg.ExpectedRevision, s.peers); err != nil {
		return nil, sharedRPCError(err)
	}
	return connect.NewResponse(&rementorv1.JoinSharedIngressResponse{State: s.sharedStateProto()}), nil
}

func (s *ControlPlaneService) TransferSharedIngress(ctx context.Context, req *connect.Request[rementorv1.TransferSharedIngressRequest]) (*connect.Response[rementorv1.TransferSharedIngressResponse], error) {
	if err := s.requireShared(); err != nil {
		return nil, err
	}
	if _, err := s.sharedIngress.Transfer(ctx, req.Msg.DestinationFingerprint, req.Msg.ExpectedVersion, s.peers); err != nil {
		return nil, sharedRPCError(err)
	}
	return connect.NewResponse(&rementorv1.TransferSharedIngressResponse{State: s.sharedStateProto()}), nil
}

func (s *ControlPlaneService) RecoverSharedIngress(ctx context.Context, req *connect.Request[rementorv1.RecoverSharedIngressRequest]) (*connect.Response[rementorv1.RecoverSharedIngressResponse], error) {
	if err := s.requireShared(); err != nil {
		return nil, err
	}
	if _, err := s.sharedIngress.RecoverAt(ctx, req.Msg.ExpectedRevision, s.peers); err != nil {
		return nil, sharedRPCError(err)
	}
	return connect.NewResponse(&rementorv1.RecoverSharedIngressResponse{State: s.sharedStateProto()}), nil
}

func (s *ControlPlaneService) CancelSharedIngress(ctx context.Context, req *connect.Request[rementorv1.CancelSharedIngressRequest]) (*connect.Response[rementorv1.CancelSharedIngressResponse], error) {
	if err := s.requireShared(); err != nil {
		return nil, err
	}
	if err := s.sharedIngress.CancelAt(ctx, req.Msg.ExpectedRevision, s.peers); err != nil {
		return nil, sharedRPCError(err)
	}
	return connect.NewResponse(&rementorv1.CancelSharedIngressResponse{State: s.sharedStateProto()}), nil
}

func (s *ControlPlaneService) SyncSharedIngress(ctx context.Context, req *connect.Request[rementorv1.SyncSharedIngressRequest]) (*connect.Response[rementorv1.SyncSharedIngressResponse], error) {
	if err := s.requireShared(); err != nil {
		return nil, err
	}
	if _, err := s.sharedIngress.Sync(ctx, s.peers); err != nil {
		return nil, sharedRPCError(err)
	}
	return connect.NewResponse(&rementorv1.SyncSharedIngressResponse{State: s.sharedStateProto()}), nil
}
