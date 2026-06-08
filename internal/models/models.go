package models

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Constants for the application
const (
	DefaultHealthEndpoint          = "actuator/health"
	DefaultLoggersEndpoint         = "actuator/loggers"
	FallbackLoggersEndpoint        = "loggers"
	DefaultHealthCheckIntervalSecs = 30
	DefaultHTTPPort                = 80
	DefaultHTTPSPort               = 443
	DefaultRementorPort            = 9300
	DefaultProxyPort               = 8080
	HTTPSuccessMin                 = 200
	HTTPSuccessMax                 = 299
	ProtocolHTTP                   = "http"
	ProtocolHTTPS                  = "https"
	Localhost                      = "localhost"
	HostnameSeparator              = "."
	PathWildcard                   = "/*"
	RouteGroupRemote               = "group2"
	AuthHeaderHost                 = "Host"

	// WorkspaceTypeRouting is the default path-based routing type
	WorkspaceTypeRouting = "routing"
	// WorkspaceTypeLocalApps is the always-on local app proxy type
	WorkspaceTypeLocalApps = "local-apps"
)

// RoutingConfig holds the path-based routing configuration
type RoutingConfig struct {
	Mode                 string `json:"mode"`                 // "path-based"
	LocalDomain          string `json:"localDomain"`          // e.g., "api.localhost"
	DefaultRemoteBaseURL string `json:"defaultRemoteBaseUrl"` // e.g., "https://api.remote.example.test"
}

// AppRuntime holds runtime health status information
type AppRuntime struct {
	sync.RWMutex
	wsID       string
	appID      string
	healthOk   bool
	healthLast *time.Time
	remoteOk   bool
	remoteLast *time.Time
}

// Initialize sets up the runtime with workspace and application IDs
func (ar *AppRuntime) Initialize(wsID, appID string) {
	ar.Lock()
	defer ar.Unlock()
	ar.wsID = wsID
	ar.appID = appID
}

// IsInitialized checks if the runtime has been initialized
func (ar *AppRuntime) IsInitialized() bool {
	ar.RLock()
	defer ar.RUnlock()
	return ar.wsID != "" && ar.appID != ""
}

// GetWsID returns the workspace ID
func (ar *AppRuntime) GetWsID() string {
	ar.RLock()
	defer ar.RUnlock()
	return ar.wsID
}

// GetAppID returns the application ID
func (ar *AppRuntime) GetAppID() string {
	ar.RLock()
	defer ar.RUnlock()
	return ar.appID
}

// GetHealthOk returns the health status
func (ar *AppRuntime) GetHealthOk() bool {
	ar.RLock()
	defer ar.RUnlock()
	return ar.healthOk
}

// SetHealthOk sets the health status
func (ar *AppRuntime) SetHealthOk(value bool) {
	ar.Lock()
	defer ar.Unlock()
	ar.healthOk = value
}

// GetHealthLast returns the last health check time
func (ar *AppRuntime) GetHealthLast() *time.Time {
	ar.RLock()
	defer ar.RUnlock()
	return ar.healthLast
}

// SetHealthLast sets the last health check time
func (ar *AppRuntime) SetHealthLast(value *time.Time) {
	ar.Lock()
	defer ar.Unlock()
	ar.healthLast = value
}

// GetRemoteOk returns the remote status
func (ar *AppRuntime) GetRemoteOk() bool {
	ar.RLock()
	defer ar.RUnlock()
	return ar.remoteOk
}

// SetRemoteOk sets the remote status
func (ar *AppRuntime) SetRemoteOk(value bool) {
	ar.Lock()
	defer ar.Unlock()
	ar.remoteOk = value
}

// GetRemoteLast returns the last remote check time
func (ar *AppRuntime) GetRemoteLast() *time.Time {
	ar.RLock()
	defer ar.RUnlock()
	return ar.remoteLast
}

// SetRemoteLast sets the last remote check time
func (ar *AppRuntime) SetRemoteLast(value *time.Time) {
	ar.Lock()
	defer ar.Unlock()
	ar.remoteLast = value
}

// UpdateBothStatuses updates both health and remote statuses atomically
func (ar *AppRuntime) UpdateBothStatuses(healthOk bool, healthLast *time.Time, remoteOk bool, remoteLast *time.Time) {
	ar.Lock()
	defer ar.Unlock()
	ar.healthOk = healthOk
	ar.healthLast = healthLast
	ar.remoteOk = remoteOk
	ar.remoteLast = remoteLast
}

// Application represents an application in a workspace
type Application struct {
	ID            string        `json:"id"`
	Name          string        `json:"name,omitempty"`          // Display name
	Path          string        `json:"path"`                    // URL path for routing (e.g., "/users")
	Domain        string        `json:"domain,omitempty"`        // Per-app hostname for local-apps type
	RemoteBaseUrl string        `json:"remoteBaseUrl,omitempty"` // Per-app remote base URL override
	Context       string        `json:"context,omitempty"`       // Optional context path
	Health        string        `json:"health"`
	Port          int           `json:"port"`
	Active        bool          `json:"active"`
	RoutePattern  *string       `json:"routePattern,omitempty"`
	LoggerConfig  *LoggerConfig `json:"loggerConfig,omitempty"`
	StripOrigin   bool          `json:"stripOrigin,omitempty"` // Strip Origin header for local proxy (Quarkus Dev UI fix)
	Runtime       AppRuntime    `json:"-"`
	wsID          string        `json:"-"`
}

// SetWsID sets the workspace ID for this application
func (a *Application) SetWsID(wsID string) {
	a.wsID = wsID
}

// GetWsID returns the workspace ID
func (a *Application) GetWsID() string {
	return a.wsID
}

// InitializeRuntime initializes the runtime with the workspace reference
func (a *Application) InitializeRuntime() {
	if a.wsID != "" {
		a.Runtime.Initialize(a.wsID, a.ID)
	}
}

// HasLocal checks if the application has a local port configured
func (a *Application) HasLocal() bool {
	return a.Port > 0
}

// HealthURL returns the local health check URL
func (a *Application) HealthURL() string {
	return fmt.Sprintf("%s://%s:%d%s/%s", ProtocolHTTP, Localhost, a.Port, a.Context, a.Health)
}

// LocalHealthURL returns the local health check URL (path-based)
func (a *Application) LocalHealthURL() string {
	return fmt.Sprintf("%s://%s:%d%s/%s", ProtocolHTTP, Localhost, a.Port, a.Context, a.Health)
}

// RemoteHealthURL returns the remote health check URL (path-based)
func (a *Application) RemoteHealthURL(defaultRemoteBaseUrl string) string {
	context := a.Context
	if context == "" {
		context = a.Path
	}
	context = strings.Trim(context, "/")
	if context == "" {
		return fmt.Sprintf("%s/%s", strings.TrimRight(defaultRemoteBaseUrl, "/"), strings.TrimLeft(a.Health, "/"))
	}
	return fmt.Sprintf("%s/%s/%s", strings.TrimRight(defaultRemoteBaseUrl, "/"), context, strings.TrimLeft(a.Health, "/"))
}

// LoggerConfig represents the logger configuration for an application
type LoggerConfig struct {
	Enabled          bool   `json:"enabled"`
	Endpoint         string `json:"endpoint,omitempty"`
	AuthType         string `json:"authType,omitempty"` // "basic", "bearer", "none"
	AuthUsername     string `json:"authUsername,omitempty"`
	AuthPassword     string `json:"authPassword,omitempty"`
	AuthToken        string `json:"authToken,omitempty"`
	UseProjectConfig bool   `json:"useProjectConfig"`
}

// GetLoggersEndpoint returns the appropriate loggers endpoint
func (a *Application) GetLoggersEndpoint() string {
	if a.LoggerConfig != nil && a.LoggerConfig.Endpoint != "" {
		return a.LoggerConfig.Endpoint
	}
	if a.Health == DefaultHealthEndpoint || (len(a.Health) >= 9 && a.Health[:9] == "actuator/") {
		return DefaultLoggersEndpoint
	}
	return FallbackLoggersEndpoint
}

// LoggerAuthConfig holds the global logger configuration
type LoggerAuthConfig struct {
	Type     string `json:"type"` // "basic", "bearer", "none"
	Username string `json:"username"`
	Password string `json:"password"`
	Token    string `json:"token"`
}

// AppLoggerState represents the persisted state of a logger for an application
type AppLoggerState struct {
	Level     string    `json:"level"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// LoggersURL returns the local loggers URL
func (a *Application) LoggersURL() string {
	return fmt.Sprintf("%s://%s:%d%s/%s", ProtocolHTTP, Localhost, a.Port, a.Path, a.GetLoggersEndpoint())
}

// LoggersURLWithHost returns the loggers URL with the given host (path-based routing)
func (a *Application) LoggersURLWithHost(defaultRemoteBaseUrl string) (string, error) {
	if a.Active && a.HasLocal() {
		return a.LoggersURL(), nil
	}
	if defaultRemoteBaseUrl != "" {
		return fmt.Sprintf("%s%s/%s", defaultRemoteBaseUrl, a.Path, a.GetLoggersEndpoint()), nil
	}
	return "", fmt.Errorf("default remote base URL not available when application is not routed locally")
}

// LoggerURL returns the URL for a specific logger
func (a *Application) LoggerURL(loggerName string) string {
	return fmt.Sprintf("%s/%s", a.LoggersURL(), loggerName)
}

// LoggerURLWithHost returns the URL for a specific logger with the given host (path-based routing)
func (a *Application) LoggerURLWithHost(loggerName string, defaultRemoteBaseUrl string) (string, error) {
	baseURL, err := a.LoggersURLWithHost(defaultRemoteBaseUrl)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/%s", baseURL, loggerName), nil
}

// Workspace represents a workspace with its applications
type Workspace struct {
	WorkspaceID   string         `json:"workspaceId"`
	Type          string         `json:"type,omitempty"`
	Name          *string        `json:"name,omitempty"`
	Color         *string        `json:"color,omitempty"`
	RoutingConfig *RoutingConfig `json:"routingConfig,omitempty"`
	Applications  []*Application `json:"applications"`
}

// IsLocalApps returns true if this workspace is of type local-apps
func (w *Workspace) IsLocalApps() bool {
	return w.Type == WorkspaceTypeLocalApps
}

// GetType returns the workspace type, defaulting to "routing" when empty.
func (w *Workspace) GetType() string {
	if w.Type == "" {
		return WorkspaceTypeRouting
	}
	return w.Type
}

// GetDefaultRemoteBaseURL returns the default remote base URL for this workspace
func (w *Workspace) GetDefaultRemoteBaseURL() string {
	if w.RoutingConfig != nil {
		return w.RoutingConfig.DefaultRemoteBaseURL
	}
	return ""
}

// GetRemoteBaseUrl returns the remote base URL for this workspace
func (w *Workspace) GetRemoteBaseUrl() string {
	if w.RoutingConfig != nil {
		return w.RoutingConfig.DefaultRemoteBaseURL
	}
	return ""
}

// GetRemoteBaseUrl returns the remote base URL for this application
// Returns app's RemoteBaseUrl if set, otherwise falls back to workspace's remote base URL
func (a *Application) GetRemoteBaseUrl(ws *Workspace) string {
	if a.RemoteBaseUrl != "" {
		return a.RemoteBaseUrl
	}
	return ws.GetRemoteBaseUrl()
}

// GetLocalDomain returns the local API domain for this workspace
func (w *Workspace) GetLocalDomain() string {
	if w.RoutingConfig != nil {
		return w.RoutingConfig.LocalDomain
	}
	return "api.localhost" // Default
}

// NameOrID returns the workspace name if set, otherwise the ID
func (w *Workspace) NameOrID() string {
	if w.Name != nil {
		return *w.Name
	}
	return w.WorkspaceID
}

// SetDefaults sets default values for the workspace
func (w *Workspace) SetDefaults() {
	if w.Color == nil {
		defaultColor := "bg-blue-500"
		w.Color = &defaultColor
	}
	// Set workspace reference on all applications
	for _, app := range w.Applications {
		app.SetWsID(w.WorkspaceID)
	}
}

// HealthUpdate represents a health status update
type HealthUpdate struct {
	WsID          string    `json:"wsId"`
	AppName       string    `json:"appName"`
	LocalOk       bool      `json:"localOk"`
	RemoteOk      bool      `json:"remoteOk"`
	LocalChecked  time.Time `json:"localChecked"`
	RemoteChecked time.Time `json:"remoteChecked"`
}

// ApplicationConfig represents a persisted application definition.
type ApplicationConfig struct {
	ID            string        `json:"id"`
	Name          string        `json:"name,omitempty"`
	Path          string        `json:"path"`
	Domain        string        `json:"domain,omitempty"`
	RemoteBaseUrl string        `json:"remoteBaseUrl,omitempty"` // Per-app remote base URL override
	Port          int           `json:"port"`
	Health        string        `json:"health,omitempty"`
	Active        bool          `json:"active"`
	RoutePattern  *string       `json:"routePattern,omitempty"`
	Context       string        `json:"context,omitempty"`
	LoggerConfig  *LoggerConfig `json:"loggerConfig,omitempty"`
	StripOrigin   bool          `json:"stripOrigin,omitempty"` // Strip Origin header for local proxy (Quarkus Dev UI fix)
}

// WorkspaceConfig represents a persisted workspace definition.
type WorkspaceConfig struct {
	ID           string              `json:"id"`
	Type         string              `json:"type,omitempty"`
	Name         string              `json:"name,omitempty"`
	Color        string              `json:"color,omitempty"`
	Routing      RoutingConfig       `json:"routing"`
	Applications []ApplicationConfig `json:"applications"`
}
