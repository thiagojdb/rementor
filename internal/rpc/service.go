package rpc

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"github.com/thiagojdb/rementor/internal/gen/rementor/v1/rementorv1connect"
	"github.com/thiagojdb/rementor/internal/models"
	"github.com/thiagojdb/rementor/internal/services"
	"github.com/thiagojdb/rementor/internal/validation"
	"google.golang.org/protobuf/types/known/timestamppb"
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
	ws := s.registry.GetWorkspaceView(req.Msg.GetWorkspaceId())
	if ws == nil {
		return nil, newRPCError(connect.CodeNotFound, fmt.Errorf("workspace not found"))
	}
	return connect.NewResponse(&rementorv1.GetWorkspaceResponse{Workspace: toProtoWorkspace(ws)}), nil
}

func (s *ControlPlaneService) CreateWorkspace(ctx context.Context, req *connect.Request[rementorv1.CreateWorkspaceRequest]) (*connect.Response[rementorv1.CreateWorkspaceResponse], error) {
	msg := req.Msg
	wsID := strings.TrimSpace(msg.GetId())
	if wsID == "" {
		return nil, newRPCError(connect.CodeInvalidArgument, fmt.Errorf("workspace ID is required"))
	}
	if err := validation.Identifier("workspace ID", wsID); err != nil {
		return nil, newRPCError(connect.CodeInvalidArgument, err)
	}
	if s.registry.FindWorkspace(wsID) != nil {
		return nil, newRPCError(connect.CodeAlreadyExists, fmt.Errorf("workspace ID %q already exists", wsID))
	}

	wsType := strings.TrimSpace(msg.GetType())
	if wsType == "" {
		wsType = models.WorkspaceTypeRouting
	}
	if wsType != models.WorkspaceTypeRouting && wsType != models.WorkspaceTypeLocalApps {
		return nil, newRPCError(connect.CodeInvalidArgument, fmt.Errorf("type must be 'routing' or 'local-apps'"))
	}
	if wsType == models.WorkspaceTypeRouting && strings.TrimSpace(msg.GetLocalDomain()) == "" {
		return nil, newRPCError(connect.CodeInvalidArgument, fmt.Errorf("local domain is required for routing workspaces"))
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
		return nil, newRPCError(connect.CodeInvalidArgument, err)
	}

	ws, operation, err := s.registry.CreateWorkspaceWithMetadata(wsConfig, correlationID(msg.GetCorrelationId(), req.Header()))
	if err != nil {
		log.Printf("Error creating workspace %s: %v", wsID, err)
		return nil, newRPCError(connect.CodeInternal, fmt.Errorf("failed to create workspace: %w", err))
	}

	return connect.NewResponse(&rementorv1.CreateWorkspaceResponse{Workspace: toProtoWorkspace(ws), Operation: toProtoOperation(operation)}), nil
}

func (s *ControlPlaneService) UpdateWorkspace(ctx context.Context, req *connect.Request[rementorv1.UpdateWorkspaceRequest]) (*connect.Response[rementorv1.UpdateWorkspaceResponse], error) {
	wsID := req.Msg.GetWorkspaceId()
	if s.registry.FindWorkspace(wsID) == nil {
		return nil, newRPCError(connect.CodeNotFound, fmt.Errorf("workspace not found"))
	}
	ws := s.registry.FindWorkspace(wsID)
	apps := toApplicationConfigs(req.Msg.GetApplications())
	localDomain := strings.TrimSpace(req.Msg.GetLocalDomain())
	remoteBaseURL := strings.TrimSpace(req.Msg.GetDefaultRemoteBaseUrl())
	if err := validation.Workspace(ws.GetType(), localDomain, remoteBaseURL, apps); err != nil {
		return nil, newRPCError(connect.CodeInvalidArgument, err)
	}
	operation, err := s.registry.UpdateWorkspaceApplicationsWithMetadata(wsID, apps, localDomain, remoteBaseURL, correlationID(req.Msg.GetCorrelationId(), req.Header()))
	if err != nil {
		log.Printf("Error updating workspace %s applications: %v", wsID, err)
		return nil, newRPCError(connect.CodeInternal, fmt.Errorf("failed to update workspace: %w", err))
	}
	return connect.NewResponse(&rementorv1.UpdateWorkspaceResponse{Workspace: toProtoWorkspace(s.registry.GetWorkspaceView(wsID)), Operation: toProtoOperation(operation)}), nil
}

func (s *ControlPlaneService) DeleteWorkspace(ctx context.Context, req *connect.Request[rementorv1.DeleteWorkspaceRequest]) (*connect.Response[rementorv1.DeleteWorkspaceResponse], error) {
	wsID := req.Msg.GetWorkspaceId()
	if s.registry.FindWorkspace(wsID) == nil {
		return nil, newRPCError(connect.CodeNotFound, fmt.Errorf("workspace not found"))
	}
	operation, err := s.registry.DeleteWorkspaceWithMetadata(wsID, correlationID(req.Msg.GetCorrelationId(), req.Header()))
	if err != nil {
		log.Printf("Error deleting workspace %s: %v", wsID, err)
		return nil, newRPCError(connect.CodeInternal, fmt.Errorf("failed to delete workspace: %w", err))
	}
	return connect.NewResponse(&rementorv1.DeleteWorkspaceResponse{Operation: toProtoOperation(operation)}), nil
}

func (s *ControlPlaneService) ListApplications(ctx context.Context, req *connect.Request[rementorv1.ListApplicationsRequest]) (*connect.Response[rementorv1.ListApplicationsResponse], error) {
	ws := s.registry.GetWorkspaceView(req.Msg.GetWorkspaceId())
	if ws == nil {
		return nil, newRPCError(connect.CodeNotFound, fmt.Errorf("workspace not found"))
	}
	apps := make([]*rementorv1.Application, 0, len(ws.Applications))
	for _, app := range ws.Applications {
		apps = append(apps, toProtoApplicationInWorkspace(ws, app))
	}
	return connect.NewResponse(&rementorv1.ListApplicationsResponse{Applications: apps}), nil
}

func (s *ControlPlaneService) GetApplication(ctx context.Context, req *connect.Request[rementorv1.GetApplicationRequest]) (*connect.Response[rementorv1.GetApplicationResponse], error) {
	ws, app, err := s.registry.GetApplicationView(req.Msg.GetWorkspaceId(), req.Msg.GetApplicationId())
	if err != nil {
		return nil, newRPCError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&rementorv1.GetApplicationResponse{Application: toProtoApplicationInWorkspace(ws, app)}), nil
}

func (s *ControlPlaneService) ResolveApplication(ctx context.Context, req *connect.Request[rementorv1.ResolveApplicationRequest]) (*connect.Response[rementorv1.ResolveApplicationResponse], error) {
	ws, app, err := s.registry.GetApplicationView(req.Msg.GetWorkspaceId(), req.Msg.GetApplicationRef())
	if err != nil {
		if errors.Is(err, models.ErrAmbiguousApplication) {
			return nil, newRPCError(connect.CodeFailedPrecondition, err)
		}
		return nil, newRPCError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&rementorv1.ResolveApplicationResponse{Application: toProtoApplicationInWorkspace(ws, app)}), nil
}

func (s *ControlPlaneService) RegisterApplicationAlias(ctx context.Context, req *connect.Request[rementorv1.RegisterApplicationAliasRequest]) (*connect.Response[rementorv1.RegisterApplicationAliasResponse], error) {
	app, operation, err := s.registry.RegisterApplicationAliasWithMetadata(req.Msg.GetWorkspaceId(), req.Msg.GetApplicationRef(), req.Msg.GetAlias(), correlationID(req.Msg.GetCorrelationId(), req.Header()))
	if err != nil {
		if errors.Is(err, models.ErrAliasConflict) {
			return nil, newRPCError(connect.CodeAlreadyExists, err)
		}
		if errors.Is(err, models.ErrAmbiguousApplication) {
			return nil, newRPCError(connect.CodeFailedPrecondition, err)
		}
		return nil, newRPCError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&rementorv1.RegisterApplicationAliasResponse{Application: toProtoApplicationInWorkspace(s.registry.GetWorkspaceView(req.Msg.GetWorkspaceId()), app), Operation: toProtoOperation(operation)}), nil
}

func (s *ControlPlaneService) UpsertApplication(ctx context.Context, req *connect.Request[rementorv1.UpsertApplicationRequest]) (*connect.Response[rementorv1.UpsertApplicationResponse], error) {
	ws := s.registry.FindWorkspace(req.Msg.GetWorkspaceId())
	if ws == nil {
		return nil, newRPCError(connect.CodeNotFound, fmt.Errorf("workspace not found"))
	}
	input := req.Msg.GetApplication()
	if input == nil {
		return nil, newRPCError(connect.CodeInvalidArgument, fmt.Errorf("application is required"))
	}
	appConfig := toApplicationConfig(input)
	if appConfig.AppID == "" {
		if _, existing, err := s.registry.FindApp(ws.WorkspaceID, appConfig.ID); err == nil {
			appConfig.AppID = existing.CanonicalAppID()
			appConfig.ID = existing.CanonicalAppID()
			appConfig.ServiceID = existing.ServiceID
			appConfig.Repository = existing.Repository
			appConfig.Aliases = existing.NormalizedAliases()
		}
	}
	if appConfig.AppID == "" {
		appConfig.AppID = appConfig.ID
	}
	appConfig.ID = appConfig.AppID
	if err := validation.Application(ws.GetType(), appConfig); err != nil {
		return nil, newRPCError(connect.CodeInvalidArgument, err)
	}

	apps := applicationConfigsFromWorkspace(ws)
	created := true
	for i := range apps {
		if apps[i].CanonicalAppID() != appConfig.CanonicalAppID() {
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
	operation, err := s.registry.UpdateWorkspaceApplicationsWithMetadata(
		ws.WorkspaceID, apps, ws.GetLocalDomain(), ws.GetDefaultRemoteBaseURL(), correlationID(req.Msg.GetCorrelationId(), req.Header()),
	)
	if err != nil {
		return nil, newRPCError(connect.CodeInternal, err)
	}
	ws = s.registry.GetWorkspaceView(ws.WorkspaceID)
	_, app, err := s.registry.GetApplicationView(ws.WorkspaceID, appConfig.ID)
	if err != nil {
		return nil, newRPCError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&rementorv1.UpsertApplicationResponse{
		Application: toProtoApplicationInWorkspace(ws, app),
		Created:     created,
		Operation:   toProtoOperation(operation),
	}), nil
}

func (s *ControlPlaneService) DeleteApplication(ctx context.Context, req *connect.Request[rementorv1.DeleteApplicationRequest]) (*connect.Response[rementorv1.DeleteApplicationResponse], error) {
	ws := s.registry.FindWorkspace(req.Msg.GetWorkspaceId())
	if ws == nil {
		return nil, newRPCError(connect.CodeNotFound, fmt.Errorf("workspace not found"))
	}
	apps := applicationConfigsFromWorkspace(ws)
	filtered := make([]models.ApplicationConfig, 0, len(apps))
	found := false
	_, target, err := s.registry.FindApp(ws.WorkspaceID, req.Msg.GetApplicationId())
	if err != nil {
		return nil, newRPCError(connect.CodeNotFound, err)
	}
	for _, app := range apps {
		if app.CanonicalAppID() == target.CanonicalAppID() {
			found = true
			continue
		}
		filtered = append(filtered, app)
	}
	if !found {
		return nil, newRPCError(connect.CodeNotFound, fmt.Errorf("application not found"))
	}
	operation, err := s.registry.UpdateWorkspaceApplicationsWithMetadata(
		ws.WorkspaceID, filtered, ws.GetLocalDomain(), ws.GetDefaultRemoteBaseURL(), correlationID(req.Msg.GetCorrelationId(), req.Header()),
	)
	if err != nil {
		return nil, newRPCError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&rementorv1.DeleteApplicationResponse{Operation: toProtoOperation(operation)}), nil
}

func (s *ControlPlaneService) ToggleApplication(ctx context.Context, req *connect.Request[rementorv1.ToggleApplicationRequest]) (*connect.Response[rementorv1.ToggleApplicationResponse], error) {
	app, operation, err := s.registry.ToggleAppWithMetadata(req.Msg.GetWorkspaceId(), req.Msg.GetApplicationId(), correlationID(req.Msg.GetCorrelationId(), req.Header()))
	if err != nil {
		return nil, newRPCError(classifyRegistryError(err), err)
	}
	return connect.NewResponse(&rementorv1.ToggleApplicationResponse{Application: toProtoApplicationInWorkspace(s.registry.GetWorkspaceView(req.Msg.GetWorkspaceId()), app), Operation: toProtoOperation(operation)}), nil
}

func (s *ControlPlaneService) ToggleAllToRemote(ctx context.Context, req *connect.Request[rementorv1.ToggleAllToRemoteRequest]) (*connect.Response[rementorv1.ToggleAllToRemoteResponse], error) {
	result, operation, err := s.registry.ToggleAllToRemoteWithMetadata(req.Msg.GetWorkspaceId(), correlationID(req.Msg.GetCorrelationId(), req.Header()))
	if err != nil {
		return nil, newRPCError(classifyRegistryError(err), err)
	}
	return connect.NewResponse(&rementorv1.ToggleAllToRemoteResponse{SuccessCount: int32(result.SuccessCount), FailureCount: int32(result.FailureCount), Operation: toProtoOperation(operation)}), nil
}

func (s *ControlPlaneService) ToggleAllToLocal(ctx context.Context, req *connect.Request[rementorv1.ToggleAllToLocalRequest]) (*connect.Response[rementorv1.ToggleAllToLocalResponse], error) {
	result, operation, err := s.registry.ToggleAllToLocalWithMetadata(req.Msg.GetWorkspaceId(), correlationID(req.Msg.GetCorrelationId(), req.Header()))
	if err != nil {
		return nil, newRPCError(classifyRegistryError(err), err)
	}
	return connect.NewResponse(&rementorv1.ToggleAllToLocalResponse{SuccessCount: int32(result.SuccessCount), FailureCount: int32(result.FailureCount), Operation: toProtoOperation(operation)}), nil
}

func (s *ControlPlaneService) SyncWorkspaceRouting(ctx context.Context, req *connect.Request[rementorv1.SyncWorkspaceRoutingRequest]) (*connect.Response[rementorv1.SyncWorkspaceRoutingResponse], error) {
	if s.registry.FindWorkspace(req.Msg.GetWorkspaceId()) == nil {
		return nil, newRPCError(connect.CodeNotFound, fmt.Errorf("workspace not found"))
	}
	result, err := s.registry.SyncRoute(req.Msg.GetWorkspaceId(), correlationID(req.Msg.GetCorrelationId(), req.Header()), true)
	if err != nil {
		return nil, newRPCError(classifyRegistryError(err), err)
	}
	return connect.NewResponse(&rementorv1.SyncWorkspaceRoutingResponse{Status: result.Status, Operation: toProtoOperation(result.Operation)}), nil
}

func (s *ControlPlaneService) GetRoutePattern(ctx context.Context, req *connect.Request[rementorv1.GetRoutePatternRequest]) (*connect.Response[rementorv1.GetRoutePatternResponse], error) {
	_, app, err := s.registry.FindApp(req.Msg.GetWorkspaceId(), req.Msg.GetApplicationId())
	if err != nil {
		return nil, newRPCError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&rementorv1.GetRoutePatternResponse{Pattern: app.RoutePattern}), nil
}

func (s *ControlPlaneService) UpdateRoutePattern(ctx context.Context, req *connect.Request[rementorv1.UpdateRoutePatternRequest]) (*connect.Response[rementorv1.UpdateRoutePatternResponse], error) {
	var patternPtr *string
	if req.Msg.GetPattern() != "" {
		pattern := req.Msg.GetPattern()
		if err := validation.RoutePattern(pattern); err != nil {
			return nil, newRPCError(connect.CodeInvalidArgument, err)
		}
		patternPtr = &pattern
	}
	app, operation, err := s.registry.UpdateRoutePatternWithMetadata(req.Msg.GetWorkspaceId(), req.Msg.GetApplicationId(), patternPtr, correlationID(req.Msg.GetCorrelationId(), req.Header()))
	if err != nil {
		return nil, newRPCError(classifyRegistryError(err), err)
	}
	return connect.NewResponse(&rementorv1.UpdateRoutePatternResponse{Application: toProtoApplicationInWorkspace(s.registry.GetWorkspaceView(req.Msg.GetWorkspaceId()), app), Operation: toProtoOperation(operation)}), nil
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
				if err := stream.Send(toProtoHealthUpdate(update, s.registry.GetWorkspaceView(update.WsID))); err != nil {
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
		ID: strings.TrimSpace(input.GetId()), AppID: strings.TrimSpace(input.GetAppId()), ServiceID: strings.TrimSpace(input.GetServiceId()), Repository: strings.TrimSpace(input.GetRepository()), Aliases: input.GetAliases(), Name: strings.TrimSpace(input.GetName()),
		Path: strings.TrimSpace(input.GetPath()), Domain: strings.TrimSpace(input.GetDomain()),
		RemoteBaseUrl: strings.TrimSpace(input.GetRemoteBaseUrl()), Port: int(input.GetPort()),
		Health: health, Context: strings.TrimSpace(input.GetContext()),
		RouteOverride: input.GetRouteOverride(), RouteOverrideSet: input.RouteOverride != nil,
	}
}

func applicationConfigsFromWorkspace(ws *models.Workspace) []models.ApplicationConfig {
	apps := make([]models.ApplicationConfig, 0, len(ws.Applications))
	for _, app := range ws.Applications {
		apps = append(apps, models.ApplicationConfig{
			ID: app.ID, AppID: app.CanonicalAppID(), ServiceID: app.ServiceID, Repository: app.Repository, Aliases: app.NormalizedAliases(), Name: app.Name, Path: app.Path, Domain: app.Domain,
			RemoteBaseUrl: app.RemoteBaseUrl, Port: app.Port, Health: app.Health,
			Active: app.Active, RoutePattern: app.RoutePattern, Context: app.Context,
			StripOrigin: app.StripOrigin, RouteOverride: app.RouteOverride, RouteOverrideSet: true,
		})
	}
	return apps
}

func toProtoWorkspace(ws *models.Workspace) *rementorv1.Workspace {
	if ws == nil {
		return nil
	}
	apps := make([]*rementorv1.Application, 0, len(ws.Applications))
	for _, app := range ws.Applications {
		apps = append(apps, toProtoApplicationInWorkspace(ws, app))
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
		Environment: &rementorv1.WorkspaceEnvironmentRef{
			WorkspaceId: ws.WorkspaceID,
			Environment: ws.WorkspaceID,
			LegacyId:    ws.WorkspaceID,
		},
		Route: routeStateToProto(ws.Route),
	}
}

func toProtoApplication(app *models.Application) *rementorv1.Application {
	return toProtoApplicationInWorkspace(nil, app)
}

func toProtoApplicationInWorkspace(ws *models.Workspace, app *models.Application) *rementorv1.Application {
	if app == nil {
		return nil
	}
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
	identity := &rementorv1.CanonicalApplicationRef{
		AppId:      app.CanonicalAppID(),
		ServiceId:  app.ServiceID,
		Repository: app.Repository,
		Aliases:    app.NormalizedAliases(),
	}
	if app.ID != app.CanonicalAppID() {
		identity.LegacyId = app.ID
	}
	environment := &rementorv1.WorkspaceEnvironmentRef{}
	if ws != nil {
		environment.WorkspaceId = ws.WorkspaceID
		environment.Environment = ws.WorkspaceID
		environment.LegacyId = ws.WorkspaceID
	}
	state := app.Route
	if state.DesiredMode == "" && state.EffectiveMode == "" {
		state = app.RouteStateFor(ws)
	}
	return &rementorv1.Application{
		Id:            app.ID,
		AppId:         app.CanonicalAppID(),
		ServiceId:     app.ServiceID,
		Repository:    app.Repository,
		Aliases:       app.NormalizedAliases(),
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
		RouteOverride: app.RouteOverride,
		Identity:      identity,
		Environment:   environment,
		Route:         routeStateToProto(state),
	}
}

func routeStateToProto(state models.RouteState) *rementorv1.RouteState {
	if state.DesiredMode == "" && state.EffectiveMode == "" && state.RouteVersion == 0 && state.OperationID == "" && state.VerificationStatus == "" {
		return nil
	}
	return &rementorv1.RouteState{
		DesiredMode:        routeMode(state.DesiredMode),
		EffectiveMode:      routeMode(state.EffectiveMode),
		Target:             state.Target,
		LocalTarget:        state.LocalTarget,
		RemoteTarget:       state.RemoteTarget,
		RemoteFallback:     state.RemoteFallback,
		ProxyHealth:        state.ProxyHealth,
		Version:            &rementorv1.RouteVersion{Value: state.RouteVersion},
		OperationId:        state.OperationID,
		VerifiedAt:         timestampOrNil(state.VerifiedAt),
		VerificationStatus: state.VerificationStatus,
	}
}

func routeMode(mode string) rementorv1.RouteMode {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "local":
		return rementorv1.RouteMode_ROUTE_MODE_LOCAL
	case "remote":
		return rementorv1.RouteMode_ROUTE_MODE_REMOTE
	case "fallback":
		return rementorv1.RouteMode_ROUTE_MODE_FALLBACK
	case "unknown":
		return rementorv1.RouteMode_ROUTE_MODE_UNKNOWN
	case "stale":
		return rementorv1.RouteMode_ROUTE_MODE_STALE
	default:
		return rementorv1.RouteMode_ROUTE_MODE_UNSPECIFIED
	}
}

func operationKind(kind string) rementorv1.RouteOperationKind {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "toggle":
		return rementorv1.RouteOperationKind_ROUTE_OPERATION_KIND_TOGGLE
	case "toggle-all":
		return rementorv1.RouteOperationKind_ROUTE_OPERATION_KIND_TOGGLE_ALL
	case "sync":
		return rementorv1.RouteOperationKind_ROUTE_OPERATION_KIND_SYNC
	case "update-pattern":
		return rementorv1.RouteOperationKind_ROUTE_OPERATION_KIND_UPDATE_PATTERN
	case "upsert":
		return rementorv1.RouteOperationKind_ROUTE_OPERATION_KIND_UPSERT
	case "delete":
		return rementorv1.RouteOperationKind_ROUTE_OPERATION_KIND_DELETE
	case "route-apply":
		return rementorv1.RouteOperationKind_ROUTE_OPERATION_KIND_ROUTE_APPLY
	case "route-sync":
		return rementorv1.RouteOperationKind_ROUTE_OPERATION_KIND_ROUTE_SYNC
	default:
		return rementorv1.RouteOperationKind_ROUTE_OPERATION_KIND_UNSPECIFIED
	}
}

func timestampOrNil(value *time.Time) *timestamppb.Timestamp {
	if value == nil || value.IsZero() {
		return nil
	}
	return timestamppb.New(value.UTC())
}

func toProtoOperation(operation *models.OperationMetadata) *rementorv1.OperationMetadata {
	if operation == nil {
		return nil
	}
	var createdAt, completedAt *timestamppb.Timestamp
	if !operation.CreatedAt.IsZero() {
		createdAt = timestamppb.New(operation.CreatedAt.UTC())
	}
	if !operation.CompletedAt.IsZero() {
		completedAt = timestamppb.New(operation.CompletedAt.UTC())
	}
	return &rementorv1.OperationMetadata{
		OperationId:   operation.OperationID,
		CorrelationId: operation.CorrelationID,
		RouteVersion:  &rementorv1.RouteVersion{Value: operation.RouteVersion},
		CreatedAt:     createdAt,
		CompletedAt:   completedAt,
		Kind:          operationKind(operation.Kind),
	}
}

func toProtoHealthUpdate(update models.HealthUpdate, workspace *models.Workspace) *rementorv1.WatchHealthResponse {
	response := &rementorv1.WatchHealthResponse{
		WorkspaceId:     update.WsID,
		ApplicationName: update.AppName,
		LocalOk:         update.LocalOk,
		RemoteOk:        update.RemoteOk,
		LocalChecked:    update.LocalChecked.Format(time.RFC3339),
		RemoteChecked:   update.RemoteChecked.Format(time.RFC3339),
		LocalCheckedAt:  timestamppb.New(update.LocalChecked),
		RemoteCheckedAt: timestamppb.New(update.RemoteChecked),
	}
	if workspace != nil {
		response.Environment = &rementorv1.WorkspaceEnvironmentRef{WorkspaceId: workspace.WorkspaceID, Environment: workspace.WorkspaceID, LegacyId: workspace.WorkspaceID}
		for _, app := range workspace.Applications {
			if app.ID == update.AppName || app.CanonicalAppID() == update.AppName {
				response.Identity = &rementorv1.CanonicalApplicationRef{AppId: app.CanonicalAppID(), ServiceId: app.ServiceID, Repository: app.Repository, Aliases: app.NormalizedAliases()}
				break
			}
		}
	}
	return response
}

func newRPCError(code connect.Code, err error) *connect.Error {
	if err == nil {
		err = fmt.Errorf("request failed")
	}
	rpcErr := connect.NewError(code, err)
	detail, detailErr := connect.NewErrorDetail(&rementorv1.StructuredError{
		Code:    structuredErrorCode(code),
		Message: err.Error(),
	})
	if detailErr == nil {
		rpcErr.AddDetail(detail)
	}
	return rpcErr
}

func structuredErrorCode(code connect.Code) rementorv1.ErrorCode {
	switch code {
	case connect.CodeInvalidArgument:
		return rementorv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT
	case connect.CodeNotFound:
		return rementorv1.ErrorCode_ERROR_CODE_NOT_FOUND
	case connect.CodeAlreadyExists:
		return rementorv1.ErrorCode_ERROR_CODE_ALREADY_EXISTS
	case connect.CodeFailedPrecondition:
		return rementorv1.ErrorCode_ERROR_CODE_FAILED_PRECONDITION
	case connect.CodePermissionDenied:
		return rementorv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED
	case connect.CodeUnauthenticated:
		return rementorv1.ErrorCode_ERROR_CODE_UNAUTHENTICATED
	case connect.CodeUnavailable:
		return rementorv1.ErrorCode_ERROR_CODE_UNAVAILABLE
	default:
		return rementorv1.ErrorCode_ERROR_CODE_INTERNAL
	}
}

func classifyRegistryError(err error) connect.Code {
	if err == nil {
		return connect.CodeUnknown
	}
	if errors.Is(err, models.ErrAliasConflict) {
		return connect.CodeAlreadyExists
	}
	if errors.Is(err, models.ErrAmbiguousApplication) {
		return connect.CodeFailedPrecondition
	}
	if errors.Is(err, services.ErrBrowserURLBinding) {
		return connect.CodeFailedPrecondition
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "workspace not found") || strings.Contains(message, "application not found") {
		return connect.CodeNotFound
	}
	if strings.Contains(message, "route conflict") {
		return connect.CodeFailedPrecondition
	}
	return connect.CodeInternal
}

func correlationID(requested string, header http.Header) string {
	if value := candidateCorrelationID(requested, false); value != "" {
		return value
	}
	for _, candidate := range []struct {
		value       string
		traceparent bool
	}{
		{header.Get(CorrelationHeader), false},
		{header.Get("X-Correlation-ID"), false},
		{header.Get("X-Request-ID"), false},
		{header.Get("Traceparent"), true},
	} {
		if value := candidateCorrelationID(candidate.value, candidate.traceparent); value != "" {
			return value
		}
	}
	return generatedCorrelationID()
}
