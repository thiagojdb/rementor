package rpc

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"github.com/thiagojdb/rementor/internal/models"
	"github.com/thiagojdb/rementor/internal/services"
)

func (s *ControlPlaneService) ResolveBrowserURL(ctx context.Context, req *connect.Request[rementorv1.ResolveBrowserURLRequest]) (*connect.Response[rementorv1.ResolveBrowserURLResponse], error) {
	return s.resolveBrowserURL(req)
}

func (s *ControlPlaneService) resolveBrowserURL(req *connect.Request[rementorv1.ResolveBrowserURLRequest]) (*connect.Response[rementorv1.ResolveBrowserURLResponse], error) {
	resolution, err := s.registry.ResolveBrowserURL(req.Msg.GetWorkspaceId(), req.Msg.GetApplicationRef())
	if err != nil {
		code := classifyRegistryError(err)
		if errors.Is(err, models.ErrAmbiguousApplication) {
			code = connect.CodeFailedPrecondition
		}
		return nil, newRPCErrorWithStructured(code, err, structuredErrorCode(code))
	}
	if resolution.CorrelationID == "" {
		resolution.CorrelationID = correlationID(req.Msg.GetCorrelationId(), req.Header())
	}
	return connect.NewResponse(&rementorv1.ResolveBrowserURLResponse{Resolution: browserURLResolutionToProto(resolution)}), nil
}

func browserURLResolutionToProto(res services.BrowserURLResolution) *rementorv1.BrowserURLResolution {
	result := &rementorv1.BrowserURLResolution{
		WorkspaceId:     res.WorkspaceID,
		Environment:     res.Environment,
		ApplicationRef:  res.ApplicationRef,
		CanonicalAppId:  res.CanonicalAppID,
		ServiceId:       res.ServiceID,
		Repository:      res.Repository,
		PublicHost:      res.PublicHost,
		PublicPath:      res.PublicPath,
		Url:             res.URL,
		BrowserUrl:      res.BrowserURL,
		Target:          res.Target,
		LocalTarget:     res.LocalTarget,
		RemoteTarget:    res.RemoteTarget,
		DesiredMode:     routeMode(res.DesiredMode),
		EffectiveMode:   routeMode(res.EffectiveMode),
		RouteVersion:    &rementorv1.RouteVersion{Value: res.RouteVersion},
		OperationId:     res.OperationID,
		CorrelationId:   res.CorrelationID,
		Identity:        &rementorv1.CanonicalApplicationRef{AppId: res.Identity.AppID, ServiceId: res.Identity.ServiceID, Repository: res.Identity.Repository, Aliases: append([]string(nil), res.Identity.Aliases...), LegacyId: res.Identity.LegacyID},
		EnvironmentRef:  &rementorv1.WorkspaceEnvironmentRef{WorkspaceId: res.EnvironmentRef.WorkspaceID, Environment: res.EnvironmentRef.Environment, LegacyId: res.EnvironmentRef.LegacyID},
		Precedence:      models.ClampInt32(res.Precedence),
		MatchingPattern: res.MatchingPattern,
		Route:           routeStateToProto(res.RouteState),
		Operation:       toProtoOperation(res.Operation),
	}
	return result
}
