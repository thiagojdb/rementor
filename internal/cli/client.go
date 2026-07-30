package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	rementorv1 "github.com/thiagojdb/rementor/internal/gen/rementor/v1"
	"github.com/thiagojdb/rementor/internal/gen/rementor/v1/rementorv1connect"
)

// APIError represents a non-OK response from the RPC API.
type APIError struct {
	StatusCode int
	Message    string
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
	}))
	if err != nil {
		return WorkspaceDTO{}, apiError(err)
	}
	return workspaceFromProto(res.Msg.GetWorkspace()), nil
}

func (c *Client) UpdateWorkspace(ctx context.Context, workspaceID string, req UpdateWorkspaceRequest) (WorkspaceDTO, error) {
	res, err := c.rpc.UpdateWorkspace(ctx, connect.NewRequest(&rementorv1.UpdateWorkspaceRequest{
		WorkspaceId:          workspaceID,
		Applications:         applicationInputsToProto(req.Applications),
		LocalDomain:          req.LocalDomain,
		DefaultRemoteBaseUrl: req.DefaultRemoteBaseURL,
	}))
	if err != nil {
		return WorkspaceDTO{}, apiError(err)
	}
	return workspaceFromProto(res.Msg.GetWorkspace()), nil
}

func (c *Client) DeleteWorkspace(ctx context.Context, workspaceID string) error {
	_, err := c.rpc.DeleteWorkspace(ctx, connect.NewRequest(&rementorv1.DeleteWorkspaceRequest{WorkspaceId: workspaceID}))
	return apiError(err)
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

func (c *Client) UpsertApplication(ctx context.Context, workspaceID string, input ApplicationConfigInput) (UpsertApplicationResponse, error) {
	res, err := c.rpc.UpsertApplication(ctx, connect.NewRequest(&rementorv1.UpsertApplicationRequest{
		WorkspaceId: workspaceID,
		Application: applicationInputToProto(input),
	}))
	if err != nil {
		return UpsertApplicationResponse{}, apiError(err)
	}
	return UpsertApplicationResponse{
		Application: applicationFromProto(res.Msg.GetApplication()),
		Created:     res.Msg.GetCreated(),
	}, nil
}

func (c *Client) DeleteApplication(ctx context.Context, workspaceID, appID string) error {
	_, err := c.rpc.DeleteApplication(ctx, connect.NewRequest(&rementorv1.DeleteApplicationRequest{
		WorkspaceId: workspaceID, ApplicationId: appID,
	}))
	return apiError(err)
}

func (c *Client) ToggleApplication(ctx context.Context, workspaceID, appID string) (ApplicationDTO, error) {
	res, err := c.rpc.ToggleApplication(ctx, connect.NewRequest(&rementorv1.ToggleApplicationRequest{WorkspaceId: workspaceID, ApplicationId: appID}))
	if err != nil {
		return ApplicationDTO{}, apiError(err)
	}
	return applicationFromProto(res.Msg.GetApplication()), nil
}

func (c *Client) ToggleAllToRemote(ctx context.Context, workspaceID string) (ToggleResultResponse, error) {
	res, err := c.rpc.ToggleAllToRemote(ctx, connect.NewRequest(&rementorv1.ToggleAllToRemoteRequest{WorkspaceId: workspaceID}))
	if err != nil {
		return ToggleResultResponse{}, apiError(err)
	}
	return ToggleResultResponse{SuccessCount: int(res.Msg.GetSuccessCount()), FailureCount: int(res.Msg.GetFailureCount())}, nil
}

func (c *Client) ToggleAllToLocal(ctx context.Context, workspaceID string) (ToggleResultResponse, error) {
	res, err := c.rpc.ToggleAllToLocal(ctx, connect.NewRequest(&rementorv1.ToggleAllToLocalRequest{WorkspaceId: workspaceID}))
	if err != nil {
		return ToggleResultResponse{}, apiError(err)
	}
	return ToggleResultResponse{SuccessCount: int(res.Msg.GetSuccessCount()), FailureCount: int(res.Msg.GetFailureCount())}, nil
}

func (c *Client) SyncWorkspaceRouting(ctx context.Context, workspaceID string) (map[string]string, error) {
	res, err := c.rpc.SyncWorkspaceRouting(ctx, connect.NewRequest(&rementorv1.SyncWorkspaceRoutingRequest{WorkspaceId: workspaceID}))
	if err != nil {
		return nil, apiError(err)
	}
	return map[string]string{"status": res.Msg.GetStatus()}, nil
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
	}))
	if err != nil {
		return ApplicationDTO{}, apiError(err)
	}
	return applicationFromProto(res.Msg.GetApplication()), nil
}

func apiError(err error) error {
	if err == nil {
		return nil
	}
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return &APIError{StatusCode: httpStatusFromCode(connectErr.Code()), Message: connectErr.Message()}
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
	return WorkspaceDTO{
		ID:           workspace.GetId(),
		Type:         workspace.GetType(),
		Name:         workspace.GetName(),
		Color:        workspace.GetColor(),
		Routing:      routing,
		Applications: apps,
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
	return ApplicationDTO{
		ID:            app.GetId(),
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
		Id: input.ID, Name: input.Name, Path: input.Path, Domain: input.Domain,
		RemoteBaseUrl: input.RemoteBaseUrl, Port: int32(input.Port),
		Health: input.Health, Context: input.Context,
	}
}
