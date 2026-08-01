package cli

import "time"

type CanonicalApplicationRefDTO struct {
	AppID      string   `json:"appId"`
	ServiceID  string   `json:"serviceId,omitempty"`
	Repository string   `json:"repository,omitempty"`
	Aliases    []string `json:"aliases,omitempty"`
	LegacyID   string   `json:"legacyId,omitempty"`
}

type WorkspaceEnvironmentRefDTO struct {
	WorkspaceID string `json:"workspaceId"`
	Environment string `json:"environment,omitempty"`
	LegacyID    string `json:"legacyId,omitempty"`
}

type RouteStateDTO struct {
	DesiredMode    string     `json:"desiredMode"`
	EffectiveMode  string     `json:"effectiveMode"`
	Target         string     `json:"target,omitempty"`
	LocalTarget    string     `json:"localTarget,omitempty"`
	RemoteTarget   string     `json:"remoteTarget,omitempty"`
	RemoteFallback bool       `json:"remoteFallback"`
	ProxyHealth    string     `json:"proxyHealth,omitempty"`
	RouteVersion   uint64     `json:"routeVersion"`
	OperationID    string     `json:"operationId,omitempty"`
	VerifiedAt     *time.Time `json:"verifiedAt,omitempty"`
}

type OperationMetadataDTO struct {
	OperationID   string    `json:"operationId"`
	CorrelationID string    `json:"correlationId"`
	RouteVersion  uint64    `json:"routeVersion"`
	CreatedAt     time.Time `json:"createdAt"`
	CompletedAt   time.Time `json:"completedAt"`
	Kind          string    `json:"kind"`
}

// ApplicationDTO is the CLI-friendly view of the protobuf Application message.
type ApplicationDTO struct {
	ID            string                     `json:"id"`
	AppID         string                     `json:"appId,omitempty"`
	ServiceID     string                     `json:"serviceId,omitempty"`
	Repository    string                     `json:"repository,omitempty"`
	Aliases       []string                   `json:"aliases,omitempty"`
	Name          string                     `json:"name"`
	Path          string                     `json:"path"`
	Domain        string                     `json:"domain,omitempty"`
	RemoteBaseUrl string                     `json:"remoteBaseUrl,omitempty"`
	Context       string                     `json:"context,omitempty"`
	Port          int                        `json:"port"`
	Health        string                     `json:"health,omitempty"`
	Active        bool                       `json:"active"`
	HealthStatus  string                     `json:"healthStatus"`
	RemoteStatus  string                     `json:"remoteStatus,omitempty"`
	RoutePattern  *string                    `json:"routePattern,omitempty"`
	Identity      CanonicalApplicationRefDTO `json:"identity"`
	Environment   WorkspaceEnvironmentRefDTO `json:"environment"`
	Route         *RouteStateDTO             `json:"route,omitempty"`
	Operation     *OperationMetadataDTO      `json:"operation,omitempty"`
}

// RoutingDTO is the CLI-friendly view of the protobuf Routing message.
type RoutingDTO struct {
	Mode                 string `json:"mode"`
	LocalDomain          string `json:"localDomain"`
	DefaultRemoteBaseURL string `json:"defaultRemoteBaseUrl"`
}

// WorkspaceDTO is the CLI-friendly view of the protobuf Workspace message.
type WorkspaceDTO struct {
	ID           string                     `json:"id"`
	Type         string                     `json:"type"`
	Name         string                     `json:"name"`
	Color        string                     `json:"color,omitempty"`
	Routing      *RoutingDTO                `json:"routing,omitempty"`
	Applications []ApplicationDTO           `json:"applications"`
	Environment  WorkspaceEnvironmentRefDTO `json:"environment"`
	Route        *RouteStateDTO             `json:"route,omitempty"`
	Operation    *OperationMetadataDTO      `json:"operation,omitempty"`
}

// ErrorResponse is retained for command output compatibility.
type ErrorResponse struct {
	Error string `json:"error"`
}

// CreateWorkspaceRequest is the CLI-friendly create workspace input.
type CreateWorkspaceRequest struct {
	ID                   string                   `json:"id"`
	Type                 string                   `json:"type"`
	Name                 string                   `json:"name"`
	Color                string                   `json:"color"`
	LocalDomain          string                   `json:"localDomain"`
	DefaultRemoteBaseURL string                   `json:"defaultRemoteBaseUrl"`
	Applications         []ApplicationConfigInput `json:"applications"`
	CorrelationID        string                   `json:"-"`
}

// UpdateWorkspaceRequest is the CLI-friendly update workspace input.
type UpdateWorkspaceRequest struct {
	Applications         []ApplicationConfigInput `json:"applications"`
	LocalDomain          string                   `json:"localDomain"`
	DefaultRemoteBaseURL string                   `json:"defaultRemoteBaseUrl"`
	CorrelationID        string                   `json:"-"`
}

// ApplicationConfigInput is the CLI-friendly application config input.
type ApplicationConfigInput struct {
	ID            string   `json:"id"`
	AppID         string   `json:"appId,omitempty"`
	ServiceID     string   `json:"serviceId,omitempty"`
	Repository    string   `json:"repository,omitempty"`
	Aliases       []string `json:"aliases,omitempty"`
	Name          string   `json:"name"`
	Path          string   `json:"path"`
	Domain        string   `json:"domain"`
	RemoteBaseUrl string   `json:"remoteBaseUrl"`
	Port          int      `json:"port"`
	Health        string   `json:"health"`
	Context       string   `json:"context"`
}

type UpsertApplicationResponse struct {
	Application ApplicationDTO        `json:"application"`
	Created     bool                  `json:"created"`
	Operation   *OperationMetadataDTO `json:"operation,omitempty"`
}

// RoutePatternResponse is the CLI-friendly route pattern response.
type RoutePatternResponse struct {
	Pattern *string `json:"pattern"`
}

// ToggleResultResponse is the CLI-friendly toggle-all response.
type ToggleResultResponse struct {
	SuccessCount int                   `json:"successCount"`
	FailureCount int                   `json:"failureCount"`
	Operation    *OperationMetadataDTO `json:"operation,omitempty"`
}

// UpdateRoutePatternRequest is the CLI-friendly route-pattern input.
type UpdateRoutePatternRequest struct {
	Pattern       string `json:"pattern"`
	CorrelationID string `json:"-"`
}
