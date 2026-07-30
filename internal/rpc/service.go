package rpc

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"connectrpc.com/connect"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"github.com/thiagojdb/rementor/internal/gen/rementor/v1/rementorv1connect"
	"github.com/thiagojdb/rementor/internal/models"
	"github.com/thiagojdb/rementor/internal/services"
	"github.com/thiagojdb/rementor/internal/validation"
)

type ControlPlaneService struct {
	registry *services.Registry
}

func NewControlPlaneService(registry *services.Registry) *ControlPlaneService {
	return &ControlPlaneService{registry: registry}
}

var _ rementorv1connect.ControlPlaneServiceHandler = (*ControlPlaneService)(nil)

func (s *ControlPlaneService) ListWorkspaces(ctx context.Context, req *connect.Request[rementorv1.ListWorkspacesRequest]) (*connect.Response[rementorv1.ListWorkspacesResponse], error) {
	workspaces := s.registry.GetWorkspaces()
	out := make([]*rementorv1.Workspace, 0, len(workspaces))
	for _, ws := range workspaces {
		out = append(out, toProtoWorkspace(ws))
	}
	return connect.NewResponse(&rementorv1.ListWorkspacesResponse{Workspaces: out}), nil
}

func (s *ControlPlaneService) GetWorkspace(ctx context.Context, req *connect.Request[rementorv1.GetWorkspaceRequest]) (*connect.Response[rementorv1.GetWorkspaceResponse], error) {
	ws := s.registry.FindWorkspace(req.Msg.GetWorkspaceId())
	if ws == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("workspace not found"))
	}
	return connect.NewResponse(&rementorv1.GetWorkspaceResponse{Workspace: toProtoWorkspace(ws)}), nil
}

func (s *ControlPlaneService) CreateWorkspace(ctx context.Context, req *connect.Request[rementorv1.CreateWorkspaceRequest]) (*connect.Response[rementorv1.CreateWorkspaceResponse], error) {
	msg := req.Msg
	wsID := strings.TrimSpace(msg.GetId())
	if wsID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("workspace ID is required"))
	}
	if err := validation.Identifier("workspace ID", wsID); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if s.registry.FindWorkspace(wsID) != nil {
		return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("workspace ID %q already exists", wsID))
	}

	wsType := strings.TrimSpace(msg.GetType())
	if wsType == "" {
		wsType = models.WorkspaceTypeRouting
	}
	if wsType != models.WorkspaceTypeRouting && wsType != models.WorkspaceTypeLocalApps {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("type must be 'routing' or 'local-apps'"))
	}
	if wsType == models.WorkspaceTypeRouting && strings.TrimSpace(msg.GetLocalDomain()) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("local domain is required for routing workspaces"))
	}

	wsColor := msg.GetColor()
	if wsColor == "" {
		wsColor = "bg-blue-500"
	}

	wsConfig := models.WorkspaceConfig{
		ID:    wsID,
		Type:  wsType,
		Name:  strings.TrimSpace(msg.GetName()),
		Color: wsColor,
		Routing: models.RoutingConfig{
			Mode:                 "path-based",
			LocalDomain:          strings.TrimSpace(msg.GetLocalDomain()),
			DefaultRemoteBaseURL: strings.TrimSpace(msg.GetDefaultRemoteBaseUrl()),
		},
		Applications: toApplicationConfigs(msg.GetApplications()),
	}
	if err := validation.Workspace(wsConfig.Type, wsConfig.Routing.LocalDomain, wsConfig.Routing.DefaultRemoteBaseURL, wsConfig.Applications); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	ws, err := s.registry.CreateWorkspace(wsConfig)
	if err != nil {
		log.Printf("Error creating workspace %s: %v", wsID, err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create workspace: %w", err))
	}

	return connect.NewResponse(&rementorv1.CreateWorkspaceResponse{Workspace: toProtoWorkspace(ws)}), nil
}

func (s *ControlPlaneService) UpdateWorkspace(ctx context.Context, req *connect.Request[rementorv1.UpdateWorkspaceRequest]) (*connect.Response[rementorv1.UpdateWorkspaceResponse], error) {
	wsID := req.Msg.GetWorkspaceId()
	if s.registry.FindWorkspace(wsID) == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("workspace not found"))
	}
	ws := s.registry.FindWorkspace(wsID)
	apps := toApplicationConfigs(req.Msg.GetApplications())
	localDomain := strings.TrimSpace(req.Msg.GetLocalDomain())
	remoteBaseURL := strings.TrimSpace(req.Msg.GetDefaultRemoteBaseUrl())
	if err := validation.Workspace(ws.GetType(), localDomain, remoteBaseURL, apps); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := s.registry.UpdateWorkspaceApplications(wsID, apps, localDomain, remoteBaseURL); err != nil {
		log.Printf("Error updating workspace %s applications: %v", wsID, err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to update workspace: %w", err))
	}
	return connect.NewResponse(&rementorv1.UpdateWorkspaceResponse{Workspace: toProtoWorkspace(s.registry.FindWorkspace(wsID))}), nil
}

func (s *ControlPlaneService) DeleteWorkspace(ctx context.Context, req *connect.Request[rementorv1.DeleteWorkspaceRequest]) (*connect.Response[rementorv1.DeleteWorkspaceResponse], error) {
	wsID := req.Msg.GetWorkspaceId()
	if s.registry.FindWorkspace(wsID) == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("workspace not found"))
	}
	if err := s.registry.DeleteWorkspace(wsID); err != nil {
		log.Printf("Error deleting workspace %s: %v", wsID, err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to delete workspace: %w", err))
	}
	return connect.NewResponse(&rementorv1.DeleteWorkspaceResponse{}), nil
}

func (s *ControlPlaneService) ListApplications(ctx context.Context, req *connect.Request[rementorv1.ListApplicationsRequest]) (*connect.Response[rementorv1.ListApplicationsResponse], error) {
	ws := s.registry.FindWorkspace(req.Msg.GetWorkspaceId())
	if ws == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("workspace not found"))
	}
	apps := make([]*rementorv1.Application, 0, len(ws.Applications))
	for _, app := range ws.Applications {
		apps = append(apps, toProtoApplication(app))
	}
	return connect.NewResponse(&rementorv1.ListApplicationsResponse{Applications: apps}), nil
}

func (s *ControlPlaneService) GetApplication(ctx context.Context, req *connect.Request[rementorv1.GetApplicationRequest]) (*connect.Response[rementorv1.GetApplicationResponse], error) {
	_, app, err := s.registry.FindApp(req.Msg.GetWorkspaceId(), req.Msg.GetApplicationId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&rementorv1.GetApplicationResponse{Application: toProtoApplication(app)}), nil
}

func (s *ControlPlaneService) UpsertApplication(ctx context.Context, req *connect.Request[rementorv1.UpsertApplicationRequest]) (*connect.Response[rementorv1.UpsertApplicationResponse], error) {
	ws := s.registry.FindWorkspace(req.Msg.GetWorkspaceId())
	if ws == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("workspace not found"))
	}
	input := req.Msg.GetApplication()
	if input == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("application is required"))
	}
	appConfig := toApplicationConfig(input)
	if err := validation.Application(ws.GetType(), appConfig); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	apps := applicationConfigsFromWorkspace(ws)
	created := true
	for i := range apps {
		if apps[i].ID != appConfig.ID {
			continue
		}
		created = false
		appConfig.Active = apps[i].Active
		appConfig.RoutePattern = apps[i].RoutePattern
		appConfig.StripOrigin = apps[i].StripOrigin
		apps[i] = appConfig
		break
	}
	if created {
		apps = append(apps, appConfig)
	}
	if err := s.registry.UpdateWorkspaceApplications(
		ws.WorkspaceID, apps, ws.GetLocalDomain(), ws.GetDefaultRemoteBaseURL(),
	); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	_, app, err := s.registry.FindApp(ws.WorkspaceID, appConfig.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&rementorv1.UpsertApplicationResponse{
		Application: toProtoApplication(app),
		Created:     created,
	}), nil
}

func (s *ControlPlaneService) DeleteApplication(ctx context.Context, req *connect.Request[rementorv1.DeleteApplicationRequest]) (*connect.Response[rementorv1.DeleteApplicationResponse], error) {
	ws := s.registry.FindWorkspace(req.Msg.GetWorkspaceId())
	if ws == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("workspace not found"))
	}
	apps := applicationConfigsFromWorkspace(ws)
	filtered := make([]models.ApplicationConfig, 0, len(apps))
	found := false
	for _, app := range apps {
		if app.ID == req.Msg.GetApplicationId() {
			found = true
			continue
		}
		filtered = append(filtered, app)
	}
	if !found {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("application not found"))
	}
	if err := s.registry.UpdateWorkspaceApplications(
		ws.WorkspaceID, filtered, ws.GetLocalDomain(), ws.GetDefaultRemoteBaseURL(),
	); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&rementorv1.DeleteApplicationResponse{}), nil
}

func (s *ControlPlaneService) ToggleApplication(ctx context.Context, req *connect.Request[rementorv1.ToggleApplicationRequest]) (*connect.Response[rementorv1.ToggleApplicationResponse], error) {
	app, err := s.registry.ToggleApp(req.Msg.GetWorkspaceId(), req.Msg.GetApplicationId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&rementorv1.ToggleApplicationResponse{Application: toProtoApplication(app)}), nil
}

func (s *ControlPlaneService) ToggleAllToRemote(ctx context.Context, req *connect.Request[rementorv1.ToggleAllToRemoteRequest]) (*connect.Response[rementorv1.ToggleAllToRemoteResponse], error) {
	result, err := s.registry.ToggleAllToRemote(req.Msg.GetWorkspaceId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&rementorv1.ToggleAllToRemoteResponse{SuccessCount: int32(result.SuccessCount), FailureCount: int32(result.FailureCount)}), nil
}

func (s *ControlPlaneService) ToggleAllToLocal(ctx context.Context, req *connect.Request[rementorv1.ToggleAllToLocalRequest]) (*connect.Response[rementorv1.ToggleAllToLocalResponse], error) {
	result, err := s.registry.ToggleAllToLocal(req.Msg.GetWorkspaceId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&rementorv1.ToggleAllToLocalResponse{SuccessCount: int32(result.SuccessCount), FailureCount: int32(result.FailureCount)}), nil
}

func (s *ControlPlaneService) SyncWorkspaceRouting(ctx context.Context, req *connect.Request[rementorv1.SyncWorkspaceRoutingRequest]) (*connect.Response[rementorv1.SyncWorkspaceRoutingResponse], error) {
	if s.registry.FindWorkspace(req.Msg.GetWorkspaceId()) == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("workspace not found"))
	}
	if err := s.registry.SyncRouting(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&rementorv1.SyncWorkspaceRoutingResponse{Status: "ok"}), nil
}

func (s *ControlPlaneService) GetRoutePattern(ctx context.Context, req *connect.Request[rementorv1.GetRoutePatternRequest]) (*connect.Response[rementorv1.GetRoutePatternResponse], error) {
	_, app, err := s.registry.FindApp(req.Msg.GetWorkspaceId(), req.Msg.GetApplicationId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&rementorv1.GetRoutePatternResponse{Pattern: app.RoutePattern}), nil
}

func (s *ControlPlaneService) UpdateRoutePattern(ctx context.Context, req *connect.Request[rementorv1.UpdateRoutePatternRequest]) (*connect.Response[rementorv1.UpdateRoutePatternResponse], error) {
	var patternPtr *string
	if req.Msg.GetPattern() != "" {
		pattern := req.Msg.GetPattern()
		if err := validation.RoutePattern(pattern); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		patternPtr = &pattern
	}
	app, err := s.registry.UpdateRoutePattern(req.Msg.GetWorkspaceId(), req.Msg.GetApplicationId(), patternPtr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&rementorv1.UpdateRoutePatternResponse{Application: toProtoApplication(app)}), nil
}

func (s *ControlPlaneService) WatchHealth(ctx context.Context, req *connect.Request[rementorv1.WatchHealthRequest], stream *connect.ServerStream[rementorv1.WatchHealthResponse]) error {
	wsID := req.Msg.GetWorkspaceId()
	if wsID != "" {
		s.registry.SubscribeWorkspace(wsID)
		defer s.registry.UnsubscribeWorkspace(wsID)
	}
	streamID, updateChan := s.registry.SubscribeHealth(wsID)
	defer s.registry.UnsubscribeHealth(streamID)
	if err := stream.Send(&rementorv1.WatchHealthResponse{Type: "connected"}); err != nil {
		return err
	}
	for {
		select {
		case update := <-updateChan:
			if wsID == "" || update.WsID == wsID {
				if err := stream.Send(toProtoHealthUpdate(update)); err != nil {
					return err
				}
			}
		case <-ctx.Done():
			return nil
		}
	}
}

func toApplicationConfigs(inputs []*rementorv1.ApplicationConfigInput) []models.ApplicationConfig {
	apps := make([]models.ApplicationConfig, 0, len(inputs))
	for _, input := range inputs {
		apps = append(apps, toApplicationConfig(input))
	}
	return apps
}

func toApplicationConfig(input *rementorv1.ApplicationConfigInput) models.ApplicationConfig {
	health := strings.TrimSpace(input.GetHealth())
	if health == "" {
		health = models.DefaultHealthEndpoint
	}
	return models.ApplicationConfig{
		ID: strings.TrimSpace(input.GetId()), Name: strings.TrimSpace(input.GetName()),
		Path: strings.TrimSpace(input.GetPath()), Domain: strings.TrimSpace(input.GetDomain()),
		RemoteBaseUrl: strings.TrimSpace(input.GetRemoteBaseUrl()), Port: int(input.GetPort()),
		Health: health, Context: strings.TrimSpace(input.GetContext()),
	}
}

func applicationConfigsFromWorkspace(ws *models.Workspace) []models.ApplicationConfig {
	apps := make([]models.ApplicationConfig, 0, len(ws.Applications))
	for _, app := range ws.Applications {
		apps = append(apps, models.ApplicationConfig{
			ID: app.ID, Name: app.Name, Path: app.Path, Domain: app.Domain,
			RemoteBaseUrl: app.RemoteBaseUrl, Port: app.Port, Health: app.Health,
			Active: app.Active, RoutePattern: app.RoutePattern, Context: app.Context,
			StripOrigin: app.StripOrigin,
		})
	}
	return apps
}

func toProtoWorkspace(ws *models.Workspace) *rementorv1.Workspace {
	apps := make([]*rementorv1.Application, 0, len(ws.Applications))
	for _, app := range ws.Applications {
		apps = append(apps, toProtoApplication(app))
	}
	name := ws.WorkspaceID
	if ws.Name != nil {
		name = *ws.Name
	}
	color := ""
	if ws.Color != nil {
		color = *ws.Color
	}
	var routing *rementorv1.Routing
	if !ws.IsLocalApps() && ws.RoutingConfig != nil {
		routing = &rementorv1.Routing{
			Mode:                 ws.RoutingConfig.Mode,
			LocalDomain:          ws.RoutingConfig.LocalDomain,
			DefaultRemoteBaseUrl: ws.RoutingConfig.DefaultRemoteBaseURL,
		}
	}
	return &rementorv1.Workspace{
		Id:           ws.WorkspaceID,
		Type:         ws.GetType(),
		Name:         name,
		Color:        color,
		Routing:      routing,
		Applications: apps,
	}
}

func toProtoApplication(app *models.Application) *rementorv1.Application {
	healthStatus := "unknown"
	if app.Runtime.GetHealthLast() != nil {
		if app.Runtime.GetHealthOk() {
			healthStatus = "healthy"
		} else {
			healthStatus = "unhealthy"
		}
	}
	remoteStatus := "unknown"
	if app.Runtime.GetRemoteLast() != nil {
		if app.Runtime.GetRemoteOk() {
			remoteStatus = "healthy"
		} else {
			remoteStatus = "unhealthy"
		}
	}
	name := app.ID
	if app.Name != "" {
		name = app.Name
	}
	return &rementorv1.Application{
		Id:            app.ID,
		Name:          name,
		Path:          app.Path,
		Domain:        app.Domain,
		RemoteBaseUrl: app.RemoteBaseUrl,
		Context:       app.Context,
		Port:          int32(app.Port),
		Health:        app.Health,
		Active:        app.Active,
		HealthStatus:  healthStatus,
		RemoteStatus:  remoteStatus,
		RoutePattern:  app.RoutePattern,
	}
}

func toProtoHealthUpdate(update models.HealthUpdate) *rementorv1.WatchHealthResponse {
	return &rementorv1.WatchHealthResponse{
		WorkspaceId:     update.WsID,
		ApplicationName: update.AppName,
		LocalOk:         update.LocalOk,
		RemoteOk:        update.RemoteOk,
		LocalChecked:    update.LocalChecked.Format(time.RFC3339),
		RemoteChecked:   update.RemoteChecked.Format(time.RFC3339),
	}
}
