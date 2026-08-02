package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"github.com/thiagojdb/rementor/internal/gen/rementor/v1/rementorv1connect"
	"github.com/thiagojdb/rementor/internal/models"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// APIError represents a non-OK response from the RPC API.
type APIError struct {
	StatusCode int
	Message    string
	Code       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
}

// Client is the typed RPC client used by rementorctl.
type Client struct {
	baseURL string
	rpc     rementorv1connect.ControlPlaneServiceClient
}

// NewClient creates a new RPC client for the given server URL.
func NewClient(baseURL string) *Client {
	baseURL = strings.TrimRight(baseURL, "/")
	return &Client{
		baseURL: baseURL,
		rpc:     rementorv1connect.NewControlPlaneServiceClient(http.DefaultClient, rpcBaseURL(baseURL)),
	}
}

func (c *Client) BaseURL() string {
	return c.baseURL
}

func rpcBaseURL(baseURL string) string {
	return strings.TrimRight(baseURL, "/") + "/rpc"
}

func (c *Client) ListWorkspaces(ctx context.Context) ([]WorkspaceDTO, error) {
	res, err := c.rpc.ListWorkspaces(ctx, connect.NewRequest(&rementorv1.ListWorkspacesRequest{}))
	if err != nil {
		return nil, apiError(err)
	}
	return workspacesFromProto(res.Msg.GetWorkspaces()), nil
}

func (c *Client) GetWorkspace(ctx context.Context, workspaceID string) (WorkspaceDTO, error) {
	res, err := c.rpc.GetWorkspace(ctx, connect.NewRequest(&rementorv1.GetWorkspaceRequest{WorkspaceId: workspaceID}))
	if err != nil {
		return WorkspaceDTO{}, apiError(err)
	}
	return workspaceFromProto(res.Msg.GetWorkspace()), nil
}

func (c *Client) CreateWorkspace(ctx context.Context, req CreateWorkspaceRequest) (WorkspaceDTO, error) {
	res, err := c.rpc.CreateWorkspace(ctx, connect.NewRequest(&rementorv1.CreateWorkspaceRequest{
		Id:                   req.ID,
		Type:                 req.Type,
		Name:                 req.Name,
		Color:                req.Color,
		LocalDomain:          req.LocalDomain,
		DefaultRemoteBaseUrl: req.DefaultRemoteBaseURL,
		Applications:         applicationInputsToProto(req.Applications),
		CorrelationId:        req.CorrelationID,
	}))
	if err != nil {
		return WorkspaceDTO{}, apiError(err)
	}
	workspace := workspaceFromProto(res.Msg.GetWorkspace())
	workspace.Operation = operationFromProto(res.Msg.GetOperation())
	return workspace, nil
}

func (c *Client) UpdateWorkspace(ctx context.Context, workspaceID string, req UpdateWorkspaceRequest) (WorkspaceDTO, error) {
	res, err := c.rpc.UpdateWorkspace(ctx, connect.NewRequest(&rementorv1.UpdateWorkspaceRequest{
		WorkspaceId:          workspaceID,
		Applications:         applicationInputsToProto(req.Applications),
		LocalDomain:          req.LocalDomain,
		DefaultRemoteBaseUrl: req.DefaultRemoteBaseURL,
		CorrelationId:        req.CorrelationID,
	}))
	if err != nil {
		return WorkspaceDTO{}, apiError(err)
	}
	workspace := workspaceFromProto(res.Msg.GetWorkspace())
	workspace.Operation = operationFromProto(res.Msg.GetOperation())
	return workspace, nil
}

func (c *Client) DeleteWorkspace(ctx context.Context, workspaceID string) error {
	_, err := c.DeleteWorkspaceWithMetadata(ctx, workspaceID)
	return err
}

func (c *Client) DeleteWorkspaceWithMetadata(ctx context.Context, workspaceID string) (*OperationMetadataDTO, error) {
	res, err := c.rpc.DeleteWorkspace(ctx, connect.NewRequest(&rementorv1.DeleteWorkspaceRequest{WorkspaceId: workspaceID}))
	if err != nil {
		return nil, apiError(err)
	}
	return operationFromProto(res.Msg.GetOperation()), nil
}

func (c *Client) ListApplications(ctx context.Context, workspaceID string) ([]ApplicationDTO, error) {
	res, err := c.rpc.ListApplications(ctx, connect.NewRequest(&rementorv1.ListApplicationsRequest{WorkspaceId: workspaceID}))
	if err != nil {
		return nil, apiError(err)
	}
	return applicationsFromProto(res.Msg.GetApplications()), nil
}

func (c *Client) GetApplication(ctx context.Context, workspaceID, appID string) (ApplicationDTO, error) {
	res, err := c.rpc.GetApplication(ctx, connect.NewRequest(&rementorv1.GetApplicationRequest{WorkspaceId: workspaceID, ApplicationId: appID}))
	if err != nil {
		return ApplicationDTO{}, apiError(err)
	}
	return applicationFromProto(res.Msg.GetApplication()), nil
}

func (c *Client) ResolveApplication(ctx context.Context, workspaceID, applicationRef string) (ApplicationDTO, error) {
	res, err := c.rpc.ResolveApplication(ctx, connect.NewRequest(&rementorv1.ResolveApplicationRequest{WorkspaceId: workspaceID, ApplicationRef: applicationRef}))
	if err != nil {
		return ApplicationDTO{}, apiError(err)
	}
	return applicationFromProto(res.Msg.GetApplication()), nil
}

// ResolveBrowserURL returns the stable public browser entry point for a
// canonical application ID or alias. It is deliberately separate from the
// route target so local/remote toggles do not change the browser URL.
func (c *Client) ResolveBrowserURL(ctx context.Context, workspaceID, applicationRef string) (BrowserURLResolutionDTO, error) {
	res, err := c.rpc.ResolveBrowserURL(ctx, connect.NewRequest(&rementorv1.ResolveBrowserURLRequest{
		WorkspaceId: workspaceID, ApplicationRef: applicationRef,
	}))
	if err != nil {
		return BrowserURLResolutionDTO{}, apiError(err)
	}
	return browserURLResolutionFromProto(res.Msg.GetResolution()), nil
}

// ResolveURL is a concise compatibility alias for ResolveBrowserURL.
func (c *Client) ResolveURL(ctx context.Context, workspaceID, applicationRef string) (BrowserURLResolutionDTO, error) {
	return c.ResolveBrowserURL(ctx, workspaceID, applicationRef)
}

// ResolveApplicationURL names the same stable browser URL operation after the
// application identity for integrations that use that vocabulary.
func (c *Client) ResolveApplicationURL(ctx context.Context, workspaceID, applicationRef string) (BrowserURLResolutionDTO, error) {
	return c.ResolveBrowserURL(ctx, workspaceID, applicationRef)
}

func (c *Client) RegisterApplicationAlias(ctx context.Context, workspaceID, applicationRef, alias string) (ApplicationDTO, error) {
	res, err := c.rpc.RegisterApplicationAlias(ctx, connect.NewRequest(&rementorv1.RegisterApplicationAliasRequest{WorkspaceId: workspaceID, ApplicationRef: applicationRef, Alias: alias}))
	if err != nil {
		return ApplicationDTO{}, apiError(err)
	}
	app := applicationFromProto(res.Msg.GetApplication())
	app.Operation = operationFromProto(res.Msg.GetOperation())
	return app, nil
}

func (c *Client) UpsertApplication(ctx context.Context, workspaceID string, input ApplicationConfigInput) (UpsertApplicationResponse, error) {
	res, err := c.rpc.UpsertApplication(ctx, connect.NewRequest(&rementorv1.UpsertApplicationRequest{
		WorkspaceId: workspaceID,
		Application: applicationInputToProto(input),
	}))
	if err != nil {
		return UpsertApplicationResponse{}, apiError(err)
	}
	operation := operationFromProto(res.Msg.GetOperation())
	app := applicationFromProto(res.Msg.GetApplication())
	app.Operation = operation
	return UpsertApplicationResponse{Application: app, Created: res.Msg.GetCreated(), Operation: operation}, nil
}

func (c *Client) DeleteApplication(ctx context.Context, workspaceID, appID string) error {
	_, err := c.DeleteApplicationWithMetadata(ctx, workspaceID, appID)
	return err
}

func (c *Client) DeleteApplicationWithMetadata(ctx context.Context, workspaceID, appID string) (*OperationMetadataDTO, error) {
	res, err := c.rpc.DeleteApplication(ctx, connect.NewRequest(&rementorv1.DeleteApplicationRequest{
		WorkspaceId: workspaceID, ApplicationId: appID,
	}))
	if err != nil {
		return nil, apiError(err)
	}
	return operationFromProto(res.Msg.GetOperation()), nil
}

func (c *Client) ToggleApplication(ctx context.Context, workspaceID, appID string) (ApplicationDTO, error) {
	res, err := c.rpc.ToggleApplication(ctx, connect.NewRequest(&rementorv1.ToggleApplicationRequest{WorkspaceId: workspaceID, ApplicationId: appID}))
	if err != nil {
		return ApplicationDTO{}, apiError(err)
	}
	app := applicationFromProto(res.Msg.GetApplication())
	app.Operation = operationFromProto(res.Msg.GetOperation())
	return app, nil
}

func (c *Client) ToggleAllToRemote(ctx context.Context, workspaceID string) (ToggleResultResponse, error) {
	res, err := c.rpc.ToggleAllToRemote(ctx, connect.NewRequest(&rementorv1.ToggleAllToRemoteRequest{WorkspaceId: workspaceID}))
	if err != nil {
		return ToggleResultResponse{}, apiError(err)
	}
	return ToggleResultResponse{SuccessCount: int(res.Msg.GetSuccessCount()), FailureCount: int(res.Msg.GetFailureCount()), Operation: operationFromProto(res.Msg.GetOperation())}, nil
}

func (c *Client) ToggleAllToLocal(ctx context.Context, workspaceID string) (ToggleResultResponse, error) {
	res, err := c.rpc.ToggleAllToLocal(ctx, connect.NewRequest(&rementorv1.ToggleAllToLocalRequest{WorkspaceId: workspaceID}))
	if err != nil {
		return ToggleResultResponse{}, apiError(err)
	}
	return ToggleResultResponse{SuccessCount: int(res.Msg.GetSuccessCount()), FailureCount: int(res.Msg.GetFailureCount()), Operation: operationFromProto(res.Msg.GetOperation())}, nil
}

func (c *Client) SyncWorkspaceRouting(ctx context.Context, workspaceID string) (map[string]string, error) {
	res, err := c.rpc.SyncWorkspaceRouting(ctx, connect.NewRequest(&rementorv1.SyncWorkspaceRoutingRequest{WorkspaceId: workspaceID}))
	if err != nil {
		return nil, apiError(err)
	}
	result := map[string]string{"status": res.Msg.GetStatus()}
	if operation := operationFromProto(res.Msg.GetOperation()); operation != nil {
		result["operationId"] = operation.OperationID
		result["correlationId"] = operation.CorrelationID
		result["routeVersion"] = strconv.FormatUint(operation.RouteVersion, 10)
	}
	return result, nil
}

func (c *Client) GetRoutePattern(ctx context.Context, workspaceID, appID string) (RoutePatternResponse, error) {
	res, err := c.rpc.GetRoutePattern(ctx, connect.NewRequest(&rementorv1.GetRoutePatternRequest{WorkspaceId: workspaceID, ApplicationId: appID}))
	if err != nil {
		return RoutePatternResponse{}, apiError(err)
	}
	return RoutePatternResponse{Pattern: res.Msg.Pattern}, nil
}

func (c *Client) UpdateRoutePattern(ctx context.Context, workspaceID, appID string, req UpdateRoutePatternRequest) (ApplicationDTO, error) {
	res, err := c.rpc.UpdateRoutePattern(ctx, connect.NewRequest(&rementorv1.UpdateRoutePatternRequest{
		WorkspaceId:   workspaceID,
		ApplicationId: appID,
		Pattern:       req.Pattern,
		CorrelationId: req.CorrelationID,
	}))
	if err != nil {
		return ApplicationDTO{}, apiError(err)
	}
	app := applicationFromProto(res.Msg.GetApplication())
	app.Operation = operationFromProto(res.Msg.GetOperation())
	return app, nil
}

func (c *Client) GetRoutes(ctx context.Context, workspaceID string) (RouteGetResponse, error) {
	res, err := c.rpc.GetRoute(ctx, connect.NewRequest(&rementorv1.GetRouteRequest{WorkspaceId: workspaceID}))
	if err != nil {
		return RouteGetResponse{}, apiError(err)
	}
	var version uint64
	if res.Msg.GetRouteVersion() != nil {
		version = res.Msg.GetRouteVersion().GetValue()
	}
	return RouteGetResponse{WorkspaceID: res.Msg.GetWorkspaceId(), Environment: res.Msg.GetEnvironment(), RouteVersion: version, Routes: normalizedRoutesFromProto(res.Msg.GetRoutes()), Warnings: routeWarningsFromProto(res.Msg.GetWarnings()), Conflicts: routeConflictsFromProto(res.Msg.GetConflicts())}, nil
}

func (c *Client) ResolveRoute(ctx context.Context, workspaceID, host, path string) (RouteResolutionDTO, error) {
	res, err := c.rpc.ResolveRoute(ctx, connect.NewRequest(&rementorv1.ResolveRouteRequest{WorkspaceId: workspaceID, Host: host, Path: path}))
	if err != nil {
		return RouteResolutionDTO{}, apiError(err)
	}
	return routeResolutionFromProto(res.Msg.GetResolution()), nil
}

func (c *Client) PlanRoute(ctx context.Context, req PlanRouteRequest) (RoutePlanDTO, error) {
	message := &rementorv1.PlanRouteRequest{WorkspaceId: req.WorkspaceID, ApplicationRef: req.ApplicationRef, DesiredMode: routeModeToProto(req.DesiredMode), CorrelationId: req.CorrelationID, ExpectedVersion: req.ExpectedVersion}
	if req.ExpectedVersion != 0 {
		message.ExpectedRouteVersion = &rementorv1.RouteVersion{Value: req.ExpectedVersion}
	}
	if req.RoutePattern != nil {
		value := *req.RoutePattern
		message.RoutePattern = &value
	}
	res, err := c.rpc.PlanRoute(ctx, connect.NewRequest(message))
	if err != nil {
		return RoutePlanDTO{}, apiError(err)
	}
	return routePlanFromProto(res.Msg.GetPlan()), nil
}

func (c *Client) ApplyRoute(ctx context.Context, req ApplyRouteRequest) (RouteApplyResponse, error) {
	message := &rementorv1.ApplyRouteRequest{WorkspaceId: req.WorkspaceID, ApplicationRef: req.ApplicationRef, DesiredMode: routeModeToProto(req.DesiredMode), ExpectedVersion: req.ExpectedVersion, IdempotencyKey: req.IdempotencyKey, CorrelationId: req.CorrelationID}
	if req.ExpectedVersion != 0 {
		message.ExpectedRouteVersion = &rementorv1.RouteVersion{Value: req.ExpectedVersion}
	}
	if req.Plan != nil {
		message.Plan = routePlanToProto(*req.Plan)
	}
	if req.RoutePattern != nil {
		value := *req.RoutePattern
		message.RoutePattern = &value
	}
	res, err := c.rpc.ApplyRoute(ctx, connect.NewRequest(message))
	if err != nil {
		return RouteApplyResponse{}, apiError(err)
	}
	return RouteApplyResponse{Changed: res.Msg.GetChanged(), Plan: routePlanFromProto(res.Msg.GetPlan()), Routes: normalizedRoutesFromProto(res.Msg.GetRoutes()), Operation: operationFromProto(res.Msg.GetOperation()), Verified: res.Msg.GetVerified(), VerificationStatus: res.Msg.GetVerificationStatus(), Status: res.Msg.GetStatus(), Degraded: res.Msg.GetDegraded(), Rollback: res.Msg.GetRollbackStatus()}, nil
}

func (c *Client) SyncRoute(ctx context.Context, workspaceID string, repair bool, correlationID string) (RouteSyncResponse, error) {
	res, err := c.rpc.SyncRoute(ctx, connect.NewRequest(&rementorv1.SyncRouteRequest{WorkspaceId: workspaceID, Repair: &repair, CorrelationId: correlationID}))
	if err != nil {
		return RouteSyncResponse{}, apiError(err)
	}
	var desiredVersion, effectiveVersion uint64
	if res.Msg.GetDesiredRouteVersion() != nil {
		desiredVersion = res.Msg.GetDesiredRouteVersion().GetValue()
	}
	if res.Msg.GetEffectiveRouteVersion() != nil {
		effectiveVersion = res.Msg.GetEffectiveRouteVersion().GetValue()
	}
	return RouteSyncResponse{WorkspaceID: res.Msg.GetWorkspaceId(), Changed: res.Msg.GetChanged(), Verified: res.Msg.GetVerified(), Status: res.Msg.GetStatus(), DesiredRouteVersion: desiredVersion, EffectiveRouteVersion: effectiveVersion, Routes: normalizedRoutesFromProto(res.Msg.GetRoutes()), Warnings: routeWarningsFromProto(res.Msg.GetWarnings()), Operation: operationFromProto(res.Msg.GetOperation()), Degraded: res.Msg.GetDegraded(), Rollback: res.Msg.GetRollbackStatus()}, nil
}

func apiError(err error) error {
	if err == nil {
		return nil
	}
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		apiErr := &APIError{StatusCode: httpStatusFromCode(connectErr.Code()), Message: connectErr.Message()}
		for _, detail := range connectErr.Details() {
			message, detailErr := detail.Value()
			if detailErr != nil {
				continue
			}
			if structured, ok := message.(*rementorv1.StructuredError); ok {
				apiErr.Code = structured.GetCode().String()
				if structured.GetMessage() != "" {
					apiErr.Message = structured.GetMessage()
				}
				break
			}
		}
		return apiErr
	}
	return &APIError{StatusCode: http.StatusInternalServerError, Message: err.Error()}
}

func httpStatusFromCode(code connect.Code) int {
	switch code {
	case connect.CodeInvalidArgument:
		return http.StatusBadRequest
	case connect.CodeNotFound:
		return http.StatusNotFound
	case connect.CodeAlreadyExists:
		return http.StatusConflict
	case connect.CodeFailedPrecondition:
		return http.StatusPreconditionFailed
	case connect.CodePermissionDenied, connect.CodeUnauthenticated:
		return http.StatusForbidden
	case connect.CodeUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func workspacesFromProto(workspaces []*rementorv1.Workspace) []WorkspaceDTO {
	result := make([]WorkspaceDTO, 0, len(workspaces))
	for _, workspace := range workspaces {
		result = append(result, workspaceFromProto(workspace))
	}
	return result
}

func workspaceFromProto(workspace *rementorv1.Workspace) WorkspaceDTO {
	if workspace == nil {
		return WorkspaceDTO{}
	}
	apps := applicationsFromProto(workspace.GetApplications())
	var routing *RoutingDTO
	if workspace.GetRouting() != nil {
		routing = &RoutingDTO{
			Mode:                 workspace.GetRouting().GetMode(),
			LocalDomain:          workspace.GetRouting().GetLocalDomain(),
			DefaultRemoteBaseURL: workspace.GetRouting().GetDefaultRemoteBaseUrl(),
		}
	}
	environment := environmentFromProto(workspace.GetEnvironment())
	if environment.WorkspaceID == "" {
		environment.WorkspaceID = workspace.GetId()
		environment.Environment = workspace.GetId()
		environment.LegacyID = workspace.GetId()
	}
	return WorkspaceDTO{
		ID:           workspace.GetId(),
		Type:         workspace.GetType(),
		Name:         workspace.GetName(),
		Color:        workspace.GetColor(),
		Routing:      routing,
		Applications: apps,
		Environment:  environment,
		Route:        routeFromProto(workspace.GetRoute()),
	}
}

func applicationsFromProto(apps []*rementorv1.Application) []ApplicationDTO {
	result := make([]ApplicationDTO, 0, len(apps))
	for _, app := range apps {
		result = append(result, applicationFromProto(app))
	}
	return result
}

func applicationFromProto(app *rementorv1.Application) ApplicationDTO {
	if app == nil {
		return ApplicationDTO{}
	}
	identity := identityFromProto(app.GetIdentity())
	if identity.AppID == "" {
		identity.AppID = app.GetAppId()
		identity.ServiceID = app.GetServiceId()
		identity.Repository = app.GetRepository()
		if app.GetId() != app.GetAppId() {
			identity.LegacyID = app.GetId()
		}
	}
	return ApplicationDTO{
		ID:            app.GetId(),
		AppID:         app.GetAppId(),
		ServiceID:     app.GetServiceId(),
		Repository:    app.GetRepository(),
		Aliases:       append([]string(nil), app.GetAliases()...),
		Name:          app.GetName(),
		Path:          app.GetPath(),
		Domain:        app.GetDomain(),
		RemoteBaseUrl: app.GetRemoteBaseUrl(),
		Context:       app.GetContext(),
		Port:          int(app.GetPort()),
		Health:        app.GetHealth(),
		Active:        app.GetActive(),
		HealthStatus:  app.GetHealthStatus(),
		RemoteStatus:  app.GetRemoteStatus(),
		RoutePattern:  app.RoutePattern,
		Identity:      identity,
		Environment:   environmentFromProto(app.GetEnvironment()),
		Route:         routeFromProto(app.GetRoute()),
	}
}

func identityFromProto(identity *rementorv1.CanonicalApplicationRef) CanonicalApplicationRefDTO {
	if identity == nil {
		return CanonicalApplicationRefDTO{}
	}
	return CanonicalApplicationRefDTO{AppID: identity.GetAppId(), ServiceID: identity.GetServiceId(), Repository: identity.GetRepository(), Aliases: append([]string(nil), identity.GetAliases()...), LegacyID: identity.GetLegacyId()}
}

func environmentFromProto(environment *rementorv1.WorkspaceEnvironmentRef) WorkspaceEnvironmentRefDTO {
	if environment == nil {
		return WorkspaceEnvironmentRefDTO{}
	}
	return WorkspaceEnvironmentRefDTO{WorkspaceID: environment.GetWorkspaceId(), Environment: environment.GetEnvironment(), LegacyID: environment.GetLegacyId()}
}

func routeFromProto(route *rementorv1.RouteState) *RouteStateDTO {
	if route == nil {
		return nil
	}
	var version uint64
	if route.GetVersion() != nil {
		version = route.GetVersion().GetValue()
	}
	var verifiedAt *time.Time
	if timestamp := route.GetVerifiedAt(); timestamp != nil {
		value := timestamp.AsTime()
		verifiedAt = &value
	}
	return &RouteStateDTO{
		DesiredMode: routeModeFromProto(route.GetDesiredMode()), EffectiveMode: routeModeFromProto(route.GetEffectiveMode()), Target: route.GetTarget(), LocalTarget: route.GetLocalTarget(), RemoteTarget: route.GetRemoteTarget(), RemoteFallback: route.GetRemoteFallback(), ProxyHealth: route.GetProxyHealth(), RouteVersion: version, OperationID: route.GetOperationId(), VerifiedAt: verifiedAt, VerificationStatus: route.GetVerificationStatus(),
	}
}

func routeModeFromProto(mode rementorv1.RouteMode) string {
	switch mode {
	case rementorv1.RouteMode_ROUTE_MODE_LOCAL:
		return "local"
	case rementorv1.RouteMode_ROUTE_MODE_REMOTE:
		return "remote"
	case rementorv1.RouteMode_ROUTE_MODE_FALLBACK:
		return "fallback"
	case rementorv1.RouteMode_ROUTE_MODE_UNKNOWN:
		return "unknown"
	case rementorv1.RouteMode_ROUTE_MODE_STALE:
		return "stale"
	default:
		return ""
	}
}

func operationFromProto(operation *rementorv1.OperationMetadata) *OperationMetadataDTO {
	if operation == nil {
		return nil
	}
	var routeVersion uint64
	if operation.GetRouteVersion() != nil {
		routeVersion = operation.GetRouteVersion().GetValue()
	}
	var createdAt, completedAt time.Time
	if operation.GetCreatedAt() != nil {
		createdAt = operation.GetCreatedAt().AsTime()
	}
	if operation.GetCompletedAt() != nil {
		completedAt = operation.GetCompletedAt().AsTime()
	}
	return &OperationMetadataDTO{OperationID: operation.GetOperationId(), CorrelationID: operation.GetCorrelationId(), RouteVersion: routeVersion, CreatedAt: createdAt, CompletedAt: completedAt, Kind: operationKindFromProto(operation.GetKind())}
}

func operationKindFromProto(kind rementorv1.RouteOperationKind) string {
	switch kind {
	case rementorv1.RouteOperationKind_ROUTE_OPERATION_KIND_TOGGLE:
		return "toggle"
	case rementorv1.RouteOperationKind_ROUTE_OPERATION_KIND_TOGGLE_ALL:
		return "toggle-all"
	case rementorv1.RouteOperationKind_ROUTE_OPERATION_KIND_SYNC:
		return "sync"
	case rementorv1.RouteOperationKind_ROUTE_OPERATION_KIND_UPDATE_PATTERN:
		return "update-pattern"
	case rementorv1.RouteOperationKind_ROUTE_OPERATION_KIND_UPSERT:
		return "upsert"
	case rementorv1.RouteOperationKind_ROUTE_OPERATION_KIND_DELETE:
		return "delete"
	case rementorv1.RouteOperationKind_ROUTE_OPERATION_KIND_ROUTE_APPLY:
		return "route-apply"
	case rementorv1.RouteOperationKind_ROUTE_OPERATION_KIND_ROUTE_SYNC:
		return "route-sync"
	default:
		return ""
	}
}

func applicationInputsToProto(inputs []ApplicationConfigInput) []*rementorv1.ApplicationConfigInput {
	result := make([]*rementorv1.ApplicationConfigInput, 0, len(inputs))
	for _, input := range inputs {
		result = append(result, applicationInputToProto(input))
	}
	return result
}

func applicationInputToProto(input ApplicationConfigInput) *rementorv1.ApplicationConfigInput {
	return &rementorv1.ApplicationConfigInput{
		Id: input.ID, AppId: input.AppID, ServiceId: input.ServiceID, Repository: input.Repository, Aliases: input.Aliases, Name: input.Name, Path: input.Path, Domain: input.Domain,
		RemoteBaseUrl: input.RemoteBaseUrl, Port: int32(input.Port),
		Health: input.Health, Context: input.Context,
	}
}

func routeModeToProto(mode string) rementorv1.RouteMode {
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

func routeModeFromProtoValue(mode rementorv1.RouteMode) string {
	switch mode {
	case rementorv1.RouteMode_ROUTE_MODE_LOCAL:
		return "local"
	case rementorv1.RouteMode_ROUTE_MODE_REMOTE:
		return "remote"
	case rementorv1.RouteMode_ROUTE_MODE_FALLBACK:
		return "fallback"
	case rementorv1.RouteMode_ROUTE_MODE_UNKNOWN:
		return "unknown"
	case rementorv1.RouteMode_ROUTE_MODE_STALE:
		return "stale"
	default:
		return ""
	}
}

func normalizedRouteFromProto(route *rementorv1.NormalizedRoute) NormalizedRouteDTO {
	if route == nil {
		return NormalizedRouteDTO{}
	}
	var version uint64
	if route.GetVersion() != nil {
		version = route.GetVersion().GetValue()
	}
	var verifiedAt *time.Time
	if timestamp := route.GetVerifiedAt(); timestamp != nil {
		value := timestamp.AsTime()
		verifiedAt = &value
	}
	return NormalizedRouteDTO{WorkspaceID: route.GetWorkspaceId(), Environment: route.GetEnvironment(), PublicHost: route.GetPublicHost(), Pattern: route.GetPattern(), CanonicalAppID: route.GetCanonicalAppId(), ServiceID: route.GetServiceId(), Repository: route.GetRepository(), DesiredMode: routeModeFromProtoValue(route.GetDesiredMode()), EffectiveMode: routeModeFromProtoValue(route.GetEffectiveMode()), Target: route.GetTarget(), LocalTarget: route.GetLocalTarget(), RemoteTarget: route.GetRemoteTarget(), RemoteFallback: route.GetRemoteFallback(), UpstreamContext: route.GetUpstreamContext(), Precedence: int(route.GetPrecedence()), PrecedenceReason: route.GetPrecedenceReason(), Exact: route.GetExact(), ProxyHealth: route.GetProxyHealth(), VerificationStatus: route.GetVerificationStatus(), RouteVersion: version, OperationID: route.GetOperationId(), VerifiedAt: verifiedAt}
}

func normalizedRoutesFromProto(routes []*rementorv1.NormalizedRoute) []NormalizedRouteDTO {
	result := make([]NormalizedRouteDTO, 0, len(routes))
	for _, route := range routes {
		result = append(result, normalizedRouteFromProto(route))
	}
	return result
}

func routeWarningFromProto(warning *rementorv1.RouteWarning) RouteWarningDTO {
	if warning == nil {
		return RouteWarningDTO{}
	}
	return RouteWarningDTO{Code: warning.GetCode(), Message: warning.GetMessage()}
}

func routeWarningsFromProto(warnings []*rementorv1.RouteWarning) []RouteWarningDTO {
	result := make([]RouteWarningDTO, 0, len(warnings))
	for _, warning := range warnings {
		result = append(result, routeWarningFromProto(warning))
	}
	return result
}

func routeConflictFromProto(conflict *rementorv1.RouteConflict) RouteConflictDTO {
	if conflict == nil {
		return RouteConflictDTO{}
	}
	return RouteConflictDTO{WorkspaceID: conflict.GetWorkspaceId(), Environment: conflict.GetEnvironment(), PublicHost: conflict.GetPublicHost(), Pattern: conflict.GetPattern(), AppID: conflict.GetAppId(), ConflictingAppID: conflict.GetConflictingAppId(), WinningAppID: conflict.GetWinningAppId(), Reason: conflict.GetReason()}
}

func routeConflictsFromProto(conflicts []*rementorv1.RouteConflict) []RouteConflictDTO {
	result := make([]RouteConflictDTO, 0, len(conflicts))
	for _, conflict := range conflicts {
		result = append(result, routeConflictFromProto(conflict))
	}
	return result
}

func normalizedRouteToProto(route NormalizedRouteDTO) *rementorv1.NormalizedRoute {
	result := &rementorv1.NormalizedRoute{WorkspaceId: route.WorkspaceID, Environment: route.Environment, PublicHost: route.PublicHost, Pattern: route.Pattern, CanonicalAppId: route.CanonicalAppID, ServiceId: route.ServiceID, Repository: route.Repository, DesiredMode: routeModeToProto(route.DesiredMode), EffectiveMode: routeModeToProto(route.EffectiveMode), Target: route.Target, LocalTarget: route.LocalTarget, RemoteTarget: route.RemoteTarget, RemoteFallback: route.RemoteFallback, UpstreamContext: route.UpstreamContext, Precedence: models.ClampInt32(route.Precedence), PrecedenceReason: route.PrecedenceReason, Exact: route.Exact, ProxyHealth: route.ProxyHealth, VerificationStatus: route.VerificationStatus, Version: &rementorv1.RouteVersion{Value: route.RouteVersion}, OperationId: route.OperationID}
	if route.VerifiedAt != nil && !route.VerifiedAt.IsZero() {
		result.VerifiedAt = timestamppb.New(route.VerifiedAt.UTC())
	}
	return result
}

func routePlanToProto(plan RoutePlanDTO) *rementorv1.RoutePlan {
	result := &rementorv1.RoutePlan{WorkspaceId: plan.WorkspaceID, Environment: plan.Environment, BaseRouteVersion: &rementorv1.RouteVersion{Value: plan.BaseRouteVersion}, BaseVersion: plan.BaseRouteVersion, ApplicationId: plan.ApplicationID, DesiredMode: routeModeToProto(plan.DesiredMode), Fingerprint: plan.Fingerprint}
	if plan.RoutePattern != nil {
		value := *plan.RoutePattern
		result.RoutePattern = &value
	}
	for _, route := range plan.BeforeRoutes {
		result.BeforeRoutes = append(result.BeforeRoutes, normalizedRouteToProto(route))
	}
	for _, route := range plan.AfterRoutes {
		result.AfterRoutes = append(result.AfterRoutes, normalizedRouteToProto(route))
	}
	for _, warning := range plan.Warnings {
		result.Warnings = append(result.Warnings, &rementorv1.RouteWarning{Code: warning.Code, Message: warning.Message})
	}
	for _, conflict := range plan.Conflicts {
		result.Conflicts = append(result.Conflicts, &rementorv1.RouteConflict{WorkspaceId: conflict.WorkspaceID, Environment: conflict.Environment, PublicHost: conflict.PublicHost, Pattern: conflict.Pattern, AppId: conflict.AppID, ConflictingAppId: conflict.ConflictingAppID, WinningAppId: conflict.WinningAppID, Reason: conflict.Reason})
	}
	for _, change := range plan.Changes {
		protoChange := &rementorv1.RouteChange{ApplicationId: change.ApplicationID}
		if change.Before != nil {
			protoChange.Before = normalizedRouteToProto(*change.Before)
		}
		if change.After != nil {
			protoChange.After = normalizedRouteToProto(*change.After)
		}
		result.Changes = append(result.Changes, protoChange)
	}
	return result
}

func routePlanFromProto(plan *rementorv1.RoutePlan) RoutePlanDTO {
	if plan == nil {
		return RoutePlanDTO{}
	}
	var version uint64
	if plan.GetBaseRouteVersion() != nil {
		version = plan.GetBaseRouteVersion().GetValue()
	} else {
		version = plan.GetBaseVersion()
	}
	result := RoutePlanDTO{WorkspaceID: plan.GetWorkspaceId(), Environment: plan.GetEnvironment(), BaseRouteVersion: version, ApplicationID: plan.GetApplicationId(), DesiredMode: routeModeFromProtoValue(plan.GetDesiredMode()), Fingerprint: plan.GetFingerprint(), BeforeRoutes: normalizedRoutesFromProto(plan.GetBeforeRoutes()), AfterRoutes: normalizedRoutesFromProto(plan.GetAfterRoutes()), Warnings: routeWarningsFromProto(plan.GetWarnings()), Conflicts: routeConflictsFromProto(plan.GetConflicts())}
	if plan.RoutePattern != nil {
		value := plan.GetRoutePattern()
		result.RoutePattern = &value
	}
	for _, change := range plan.GetChanges() {
		item := RouteChangeDTO{ApplicationID: change.GetApplicationId()}
		if change.GetBefore() != nil {
			value := normalizedRouteFromProto(change.GetBefore())
			item.Before = &value
		}
		if change.GetAfter() != nil {
			value := normalizedRouteFromProto(change.GetAfter())
			item.After = &value
		}
		result.Changes = append(result.Changes, item)
	}
	return result
}

func routeResolutionFromProto(resolution *rementorv1.RouteResolution) RouteResolutionDTO {
	if resolution == nil {
		return RouteResolutionDTO{}
	}
	result := RouteResolutionDTO{WorkspaceID: resolution.GetWorkspaceId(), Environment: resolution.GetEnvironment(), Host: resolution.GetHost(), Path: resolution.GetPath(), MatchingPattern: resolution.GetMatchingPattern(), CanonicalAppID: resolution.GetCanonicalAppId(), ServiceID: resolution.GetServiceId(), Target: resolution.GetTarget(), Precedence: int(resolution.GetPrecedence()), PrecedenceReason: resolution.GetPrecedenceReason()}
	if resolution.GetRoute() != nil {
		value := normalizedRouteFromProto(resolution.GetRoute())
		result.Route = &value
	}
	return result
}

func browserURLResolutionFromProto(resolution *rementorv1.BrowserURLResolution) BrowserURLResolutionDTO {
	if resolution == nil {
		return BrowserURLResolutionDTO{}
	}
	var routeVersion uint64
	if resolution.GetRouteVersion() != nil {
		routeVersion = resolution.GetRouteVersion().GetValue()
	}
	result := BrowserURLResolutionDTO{
		WorkspaceID: resolution.GetWorkspaceId(), Environment: resolution.GetEnvironment(), ApplicationRef: resolution.GetApplicationRef(),
		CanonicalAppID: resolution.GetCanonicalAppId(), ServiceID: resolution.GetServiceId(), Repository: resolution.GetRepository(),
		PublicHost: resolution.GetPublicHost(), PublicPath: resolution.GetPublicPath(), URL: resolution.GetUrl(), BrowserURL: resolution.GetBrowserUrl(),
		Target: resolution.GetTarget(), LocalTarget: resolution.GetLocalTarget(), RemoteTarget: resolution.GetRemoteTarget(),
		DesiredMode: routeModeFromProto(resolution.GetDesiredMode()), EffectiveMode: routeModeFromProto(resolution.GetEffectiveMode()),
		RouteVersion: routeVersion, OperationID: resolution.GetOperationId(), CorrelationID: resolution.GetCorrelationId(),
		Identity: identityFromProto(resolution.GetIdentity()), EnvironmentRef: environmentFromProto(resolution.GetEnvironmentRef()),
		Operation: operationFromProto(resolution.GetOperation()), Precedence: int(resolution.GetPrecedence()), MatchingPattern: resolution.GetMatchingPattern(),
		RouteState: routeFromProto(resolution.GetRoute()),
	}
	if resolution.GetRoute() != nil {
		// NormalizedRouteDTO is the richer route projection used by the CLI. The
		// browser URL contract carries RouteState for wire compatibility, so
		// retain the target/mode fields in a compact normalized entry here.
		route := resolution.GetRoute()
		result.Route = &NormalizedRouteDTO{
			WorkspaceID: result.WorkspaceID, Environment: result.Environment, PublicHost: result.PublicHost,
			Pattern: resolution.GetMatchingPattern(), CanonicalAppID: result.CanonicalAppID, ServiceID: result.ServiceID,
			Repository: result.Repository, DesiredMode: result.DesiredMode, EffectiveMode: result.EffectiveMode,
			Target: route.GetTarget(), LocalTarget: route.GetLocalTarget(), RemoteTarget: route.GetRemoteTarget(),
			RemoteFallback: route.GetRemoteFallback(), Precedence: result.Precedence, PrecedenceReason: "browser URL entry",
		}
	}
	return result
}
