package rpc

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/thiagojdb/rementor/internal/fleet"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"github.com/thiagojdb/rementor/internal/models"
)

func (s *ControlPlaneService) EditFollowedConfiguration(ctx context.Context, req *connect.Request[rementorv1.EditFollowedConfigurationRequest]) (*connect.Response[rementorv1.EditFollowedConfigurationResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	input := req.Msg
	edit := fleet.ConfigurationEdit{WorkspaceID: input.WorkspaceId, ApplicationID: input.ApplicationId, Action: input.Action}
	if input.Application != nil {
		app := toApplicationConfig(input.Application)
		edit.Application = &app
	}
	if input.Routing != nil {
		edit.Routing = &models.RoutingConfig{Mode: input.Routing.Mode, LocalDomain: input.Routing.LocalDomain, DefaultRemoteBaseURL: input.Routing.DefaultRemoteBaseUrl}
	}
	if input.RegistrationPort != nil {
		port := int(*input.RegistrationPort)
		edit.RegistrationPort = &port
	}
	if err := edit.Validate(); err != nil {
		return nil, newRPCError(connect.CodeInvalidArgument, err)
	}
	if err := s.registry.EditFollowedRoute(ctx, input.WorkspaceId, input.ApplicationId, input.ExpectedVersion, edit); err != nil {
		return nil, inheritanceRPCError(err)
	}
	return connect.NewResponse(&rementorv1.EditFollowedConfigurationResponse{Workspace: s.workspaceProto(s.registry.GetWorkspaceView(input.WorkspaceId))}), nil
}
