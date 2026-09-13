package rpc

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"github.com/thiagojdb/rementor/internal/models"
	"github.com/thiagojdb/rementor/internal/validation"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func sessionToProto(session *models.RoutingSession) *rementorv1.RoutingSession {
	if session == nil {
		return nil
	}
	return &rementorv1.RoutingSession{EnvironmentId: session.EnvironmentID, BaselineVersion: session.BaselineVersion, CreatedAt: timestamppb.New(session.CreatedAt)}
}

// Requests carry an explicit session selector; existing workspace IDs remain
// usable as routing scope IDs for old clients and the UI's independent tabs.
func (s *ControlPlaneService) selectSession(message proto.Message) error {
	m := message.ProtoReflect()
	fields := m.Descriptor().Fields()
	sf, wf := fields.ByName("session_id"), fields.ByName("workspace_id")
	if sf == nil || wf == nil || m.Get(sf).String() == "" {
		return nil
	}
	id, err := s.registry.ResolveSession(m.Get(wf).String(), m.Get(sf).String())
	if err != nil {
		return newRPCError(connect.CodeFailedPrecondition, err)
	}
	m.Set(wf, protoreflect.ValueOfString(id))
	return nil
}

func (s *ControlPlaneService) CreateRoutingSession(ctx context.Context, req *connect.Request[rementorv1.CreateRoutingSessionRequest]) (*connect.Response[rementorv1.CreateRoutingSessionResponse], error) {
	ws, err := s.registry.CreateRoutingSession(req.Msg.EnvironmentId, req.Msg.Name, correlationID(req.Msg.CorrelationId, req.Header()))
	if err != nil {
		return nil, newRPCError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&rementorv1.CreateRoutingSessionResponse{Workspace: toProtoWorkspace(ws)}), nil
}
func (s *ControlPlaneService) RefreshRoutingSession(ctx context.Context, req *connect.Request[rementorv1.RefreshRoutingSessionRequest]) (*connect.Response[rementorv1.RefreshRoutingSessionResponse], error) {
	result, err := s.registry.RefreshRoutingSession(req.Msg.Id, req.Msg.PreviewToken, correlationID(req.Msg.CorrelationId, req.Header()), req.Msg.Apply)
	if err != nil {
		return nil, newRPCError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&rementorv1.RefreshRoutingSessionResponse{Workspace: toProtoWorkspace(result.Workspace), Changes: result.Changes, Conflicts: result.Conflicts, PreviewToken: result.Token}), nil
}

func (s *ControlPlaneService) upsertSession(req *connect.Request[rementorv1.UpsertApplicationRequest]) (*connect.Response[rementorv1.UpsertApplicationResponse], error) {
	input := req.Msg.Application
	if input == nil {
		return nil, newRPCError(connect.CodeInvalidArgument, fmt.Errorf("application is required"))
	}
	appRef := input.Id
	if input.AppId != "" {
		appRef = input.AppId
	}
	created := false
	ws, err := s.registry.EditSessionApplication(req.Msg.WorkspaceId, appRef, correlationID(req.Msg.CorrelationId, req.Header()), func(a *models.ApplicationConfig) error {
		created = a.Path == "" && a.PublicPath == ""
		// Registration is a sparse edit for inherited session apps. Explicit metadata
		// pairs replace their legacy aliases together, preventing contradictory paths.
		if input.Name != "" {
			a.Name = input.Name
		}
		if input.ServiceId != "" {
			a.ServiceID = input.ServiceId
		}
		if input.Repository != "" {
			a.Repository = input.Repository
		}
		if len(input.Aliases) > 0 {
			a.Aliases = input.Aliases
		}
		if input.Port != 0 {
			a.Port = int(input.Port)
		}
		if input.Health != "" {
			a.Health = input.Health
		}
		if input.Domain != "" {
			a.Domain = input.Domain
		}
		if input.RemoteBaseUrl != "" {
			a.RemoteBaseUrl = input.RemoteBaseUrl
		}
		if input.Path != "" || input.PublicPath != "" {
			a.Path = input.Path
			a.PublicPath = input.PublicPath
			a.LegacyPublicPath = false
		}
		if input.Context != "" || input.UpstreamContext != "" {
			a.Context = input.Context
			a.UpstreamContext = input.UpstreamContext
			a.LegacyUpstreamContext = false
		}
		if input.FrontendRoot != "" {
			a.FrontendRoot = input.FrontendRoot
		}
		if input.FrontendRootSource != "" {
			a.FrontendRootSource = input.FrontendRootSource
		}
		if input.RouteOverride != nil {
			a.RouteOverride = *input.RouteOverride
			a.RouteOverrideSet = true
		}
		a.NormalizeRouteMetadata()
		_, err := validation.ApplicationWithOptions(models.WorkspaceTypeRouting, *a, validation.MetadataValidationOptions{Strict: req.Msg.StrictMetadata})
		return err
	}, false)
	if err != nil {
		return nil, newRPCError(connect.CodeInvalidArgument, err)
	}
	_, a, err := s.registry.GetApplicationView(ws.WorkspaceID, appRef)
	if err != nil {
		return nil, newRPCError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&rementorv1.UpsertApplicationResponse{Application: toProtoApplicationInWorkspace(ws, a), Created: created, Operation: toProtoOperation(ws.LastOperation)}), nil
}
