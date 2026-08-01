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
		DesiredMode: routeModeFromProto(route.GetDesiredMode()), EffectiveMode: routeModeFromProto(route.GetEffectiveMode()), Target: route.GetTarget(), LocalTarget: route.GetLocalTarget(), RemoteTarget: route.GetRemoteTarget(), RemoteFallback: route.GetRemoteFallback(), ProxyHealth: route.GetProxyHealth(), RouteVersion: version, OperationID: route.GetOperationId(), VerifiedAt: verifiedAt,
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
