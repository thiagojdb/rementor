package cli

// ApplicationDTO is the CLI-friendly view of the protobuf Application message.
type ApplicationDTO struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Path          string  `json:"path"`
	Domain        string  `json:"domain,omitempty"`
	RemoteBaseUrl string  `json:"remoteBaseUrl,omitempty"`
	Context       string  `json:"context,omitempty"`
	Port          int     `json:"port"`
	Health        string  `json:"health,omitempty"`
	Active        bool    `json:"active"`
	HealthStatus  string  `json:"healthStatus"`
	RemoteStatus  string  `json:"remoteStatus,omitempty"`
	RoutePattern  *string `json:"routePattern,omitempty"`
}

// RoutingDTO is the CLI-friendly view of the protobuf Routing message.
type RoutingDTO struct {
	Mode                 string `json:"mode"`
	LocalDomain          string `json:"localDomain"`
	DefaultRemoteBaseURL string `json:"defaultRemoteBaseUrl"`
}

// WorkspaceDTO is the CLI-friendly view of the protobuf Workspace message.
type WorkspaceDTO struct {
	ID           string           `json:"id"`
	Type         string           `json:"type"`
	Name         string           `json:"name"`
	Color        string           `json:"color,omitempty"`
	Routing      *RoutingDTO      `json:"routing,omitempty"`
	Applications []ApplicationDTO `json:"applications"`
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
}

// UpdateWorkspaceRequest is the CLI-friendly update workspace input.
type UpdateWorkspaceRequest struct {
	Applications         []ApplicationConfigInput `json:"applications"`
	LocalDomain          string                   `json:"localDomain"`
	DefaultRemoteBaseURL string                   `json:"defaultRemoteBaseUrl"`
}

// ApplicationConfigInput is the CLI-friendly application config input.
type ApplicationConfigInput struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Path          string `json:"path"`
	Domain        string `json:"domain"`
	RemoteBaseUrl string `json:"remoteBaseUrl"`
	Port          int    `json:"port"`
	Health        string `json:"health"`
	Context       string `json:"context"`
}

type UpsertApplicationResponse struct {
	Application ApplicationDTO `json:"application"`
	Created     bool           `json:"created"`
}

// RoutePatternResponse is the CLI-friendly route pattern response.
type RoutePatternResponse struct {
	Pattern *string `json:"pattern"`
}

// ToggleResultResponse is the CLI-friendly toggle-all response.
type ToggleResultResponse struct {
	SuccessCount int `json:"successCount"`
	FailureCount int `json:"failureCount"`
}

// LoggerDTO is the CLI-friendly logger view.
type LoggerDTO struct {
	Name            string `json:"name"`
	EffectiveLevel  string `json:"effectiveLevel"`
	ConfiguredLevel string `json:"configuredLevel,omitempty"`
}

// LoggersResponse is the CLI-friendly loggers response.
type LoggersResponse struct {
	Levels  []string    `json:"levels"`
	Loggers []LoggerDTO `json:"loggers"`
}

// SetLoggerLevelRequest is the CLI-friendly set-level input.
type SetLoggerLevelRequest struct {
	Level string `json:"level"`
}

// UpdateRoutePatternRequest is the CLI-friendly route-pattern input.
type UpdateRoutePatternRequest struct {
	Pattern string `json:"pattern"`
}
