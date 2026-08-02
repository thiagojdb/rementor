package models

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Constants for the application
const (
	DefaultHealthEndpoint          = "actuator/health"
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

	// Route modes describe intent and the route that was actually loaded by
	// the proxy. Unknown is intentionally not inferred from application health.
	RouteModeLocal    = "local"
	RouteModeRemote   = "remote"
	RouteModeFallback = "fallback"
	RouteModeUnknown  = "unknown"
	RouteModeStale    = "stale"

	RouteVerificationVerified            = "verified"
	RouteVerificationStale               = "stale"
	RouteVerificationUnknown             = "unknown"
	RouteVerificationProviderUnavailable = "provider-unavailable"

	ProxyHealthUp          = "up"
	ProxyHealthUnknown     = "unknown"
	ProxyHealthUnavailable = "unavailable"
	ProxyHealthStale       = "stale"

	// WorkspaceTypeRouting is the default path-based routing type
	WorkspaceTypeRouting = "routing"
	// WorkspaceTypeLocalApps is the always-on local app proxy type
	WorkspaceTypeLocalApps = "local-apps"
)

// ClampInt32 bounds an integer before it crosses a protobuf int32 boundary.
func ClampInt32(value int) int32 {
	if value > math.MaxInt32 {
		return math.MaxInt32
	}
	if value < math.MinInt32 {
		return math.MinInt32
	}
	return int32(value)
}

// RoutingConfig holds the path-based routing configuration
type RoutingConfig struct {
	Mode                 string `json:"mode"`                 // "path-based"
	LocalDomain          string `json:"localDomain"`          // e.g., "api.localhost"
	DefaultRemoteBaseURL string `json:"defaultRemoteBaseUrl"` // e.g., "https://api.remote.example.test"
}

// ApplicationIdentity is the environment-independent identity shared by all
// workspace bindings of a service.
type ApplicationIdentity struct {
	AppID      string   `json:"appId"`
	ServiceID  string   `json:"serviceId"`
	Repository string   `json:"repository,omitempty"`
	Aliases    []string `json:"aliases,omitempty"`
	LegacyID   string   `json:"legacyId,omitempty"`
}

// WorkspaceEnvironmentRef makes the environment boundary explicit in domain
// responses while retaining the legacy workspace identifier.
type WorkspaceEnvironmentRef struct {
	WorkspaceID string `json:"workspaceId"`
	Environment string `json:"environment,omitempty"`
	LegacyID    string `json:"legacyId,omitempty"`
}

// ApplicationBinding is the environment-specific route configuration for an
// application identity. WorkspaceID acts as the environment boundary while
// path/domain/context remain binding metadata rather than identity fields.
type ApplicationBinding struct {
	WorkspaceID        string `json:"workspaceId"`
	AppID              string `json:"appId"`
	PublicHost         string `json:"publicHost,omitempty"`
	PublicPath         string `json:"publicPath,omitempty"`
	UpstreamContext    string `json:"upstreamContext,omitempty"`
	FrontendRoot       string `json:"frontendRoot,omitempty"`
	FrontendRootSource string `json:"frontendRootSource,omitempty"`
}

// OperationMetadata identifies a successful control-plane mutation. The
// generated protobuf contract mirrors these fields for RPC/CLI/MCP/browser
// consumers while the model keeps time values native to Go.
type OperationMetadata struct {
	OperationID   string    `json:"operationId"`
	CorrelationID string    `json:"correlationId"`
	RouteVersion  uint64    `json:"routeVersion"`
	Kind          string    `json:"kind"`
	CreatedAt     time.Time `json:"createdAt"`
	CompletedAt   time.Time `json:"completedAt"`
}

// RouteOperationJournal is the durable write-ahead record for a route
// operation. A route change crosses the proxy and SQLite stores, so the
// journal lets recovery distinguish an operation that never reached the
// proxy from one that reached it but did not commit desired state.
type RouteOperationJournal struct {
	OperationID     string       `json:"operationId"`
	WorkspaceID     string       `json:"workspaceId"`
	IdempotencyKey  string       `json:"idempotencyKey,omitempty"`
	Fingerprint     string       `json:"fingerprint"`
	CorrelationID   string       `json:"correlationId"`
	ExpectedVersion uint64       `json:"expectedVersion"`
	RouteVersion    uint64       `json:"routeVersion"`
	Phase           string       `json:"phase"`
	Status          string       `json:"status"`
	Error           string       `json:"error,omitempty"`
	Degraded        bool         `json:"degraded,omitempty"`
	RollbackStatus  string       `json:"rollbackStatus,omitempty"`
	CreatedAt       time.Time    `json:"createdAt"`
	UpdatedAt       time.Time    `json:"updatedAt"`
	PriorState      []*Workspace `json:"priorState,omitempty"`
	CandidateState  []*Workspace `json:"candidateState,omitempty"`
	Result          []byte       `json:"result,omitempty"`
}

// RouteState is the normalized route projection exposed by every control
// surface. DesiredMode and EffectiveMode intentionally remain strings in the
// domain model; the RPC adapter maps them to the typed protobuf enum.
type RouteState struct {
	DesiredMode        string     `json:"desiredMode"`
	EffectiveMode      string     `json:"effectiveMode"`
	Target             string     `json:"target,omitempty"`
	LocalTarget        string     `json:"localTarget,omitempty"`
	RemoteTarget       string     `json:"remoteTarget,omitempty"`
	RemoteFallback     bool       `json:"remoteFallback"`
	ProxyHealth        string     `json:"proxyHealth,omitempty"`
	RouteVersion       uint64     `json:"routeVersion"`
	OperationID        string     `json:"operationId,omitempty"`
	VerifiedAt         *time.Time `json:"verifiedAt,omitempty"`
	VerificationStatus string     `json:"verificationStatus,omitempty"`
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
	// ID is retained as the wire-compatible canonical application identifier.
	// AppID is the explicit identity field used by new callers; both values are
	// kept in sync when configurations are loaded or registered.
	ID         string   `json:"id"`
	AppID      string   `json:"appId,omitempty"`
	ServiceID  string   `json:"serviceId,omitempty"`
	Repository string   `json:"repository,omitempty"`
	Aliases    []string `json:"aliases,omitempty"`
	Name       string   `json:"name,omitempty"` // Display name
	// Path and Context are retained as legacy wire/config names. New callers
	// should use PublicPath and UpstreamContext to keep browser routing
	// separate from the path expected by the upstream service.
	Path               string `json:"path"`                         // legacy public path (e.g., "/users")
	PublicPath         string `json:"publicPath,omitempty"`         // public/browser route
	Domain             string `json:"domain,omitempty"`             // Per-app hostname for local-apps type
	RemoteBaseUrl      string `json:"remoteBaseUrl,omitempty"`      // Per-app remote base URL override
	Context            string `json:"context,omitempty"`            // legacy upstream context path
	UpstreamContext    string `json:"upstreamContext,omitempty"`    // upstream/backend context path
	FrontendRoot       string `json:"frontendRoot,omitempty"`       // explicit frontend base/root, when known
	FrontendRootSource string `json:"frontendRootSource,omitempty"` // manifest, registration, or other source
	// Legacy* records whether the explicit aliases were synthesized from the
	// old path/context fields. They are intentionally not part of the JSON/API
	// contract; the renderer uses them to preserve legacy ingress semantics.
	LegacyPublicPath      bool    `json:"-"`
	LegacyUpstreamContext bool    `json:"-"`
	Health                string  `json:"health"`
	Port                  int     `json:"port"`
	Active                bool    `json:"active"`
	RoutePattern          *string `json:"routePattern,omitempty"`
	// RouteOverride explicitly marks intentional overlapping route ownership.
	RouteOverride bool               `json:"routeOverride,omitempty"`
	StripOrigin   bool               `json:"stripOrigin,omitempty"` // Strip Origin header for local proxy (Quarkus Dev UI fix)
	Route         RouteState         `json:"route"`
	LastOperation *OperationMetadata `json:"lastOperation,omitempty"`
	Runtime       AppRuntime         `json:"-"`
	wsID          string             `json:"-"`
}

// PublicRoutePath returns the canonical public path while accepting legacy
// persisted configurations. PublicPath wins when both fields are present.
func (a *Application) PublicRoutePath() string {
	if a == nil {
		return ""
	}
	if strings.TrimSpace(a.PublicPath) != "" {
		return strings.TrimSpace(a.PublicPath)
	}
	return strings.TrimSpace(a.Path)
}

// IngressPath is the path used for incoming requests. Legacy registrations
// historically treated context as the public route; preserve that behavior
// only when no explicit public/upstream pair is present. New metadata always
// uses PublicPath.
func (a *Application) IngressPath() string {
	if a == nil {
		return ""
	}
	if a.LegacyPublicPath && a.LegacyUpstreamContext && strings.TrimSpace(a.Context) != "" {
		return strings.TrimSpace(a.Context)
	}
	if strings.TrimSpace(a.PublicPath) != "" {
		return strings.TrimSpace(a.PublicPath)
	}
	if strings.TrimSpace(a.PublicPath) == "" && strings.TrimSpace(a.UpstreamContext) == "" && strings.TrimSpace(a.Context) != "" {
		return strings.TrimSpace(a.Context)
	}
	return strings.TrimSpace(a.Path)
}

// BackendContextPath returns the canonical upstream context while accepting
// legacy persisted configurations.
func (a *Application) BackendContextPath() string {
	if a == nil {
		return ""
	}
	if strings.TrimSpace(a.UpstreamContext) != "" {
		return strings.TrimSpace(a.UpstreamContext)
	}
	return strings.TrimSpace(a.Context)
}

// NormalizeRouteMetadata resolves legacy path/context values into the
// explicit fields and mirrors them back to legacy names for old clients.
// Syntax normalization belongs to validation; this method only resolves
// field precedence and migration.
func (a *Application) NormalizeRouteMetadata() {
	if a == nil {
		return
	}
	if strings.TrimSpace(a.PublicPath) == "" && strings.TrimSpace(a.Path) != "" {
		a.LegacyPublicPath = true
		a.PublicPath = strings.TrimSpace(a.Path)
	}
	if strings.TrimSpace(a.Path) == "" {
		a.Path = strings.TrimSpace(a.PublicPath)
	}
	if strings.TrimSpace(a.UpstreamContext) == "" && strings.TrimSpace(a.Context) != "" {
		a.LegacyUpstreamContext = true
		a.UpstreamContext = strings.TrimSpace(a.Context)
	}
	if strings.TrimSpace(a.Context) == "" {
		a.Context = strings.TrimSpace(a.UpstreamContext)
	}
	a.Path = normalizeMetadataPath(a.Path)
	a.PublicPath = normalizeMetadataPath(a.PublicPath)
	a.Context = normalizeMetadataPath(a.Context)
	a.UpstreamContext = normalizeMetadataPath(a.UpstreamContext)
	a.FrontendRoot = normalizeMetadataPath(a.FrontendRoot)
}

// CanonicalAppID returns the stable identity key while preserving the legacy
// ID field for clients that still use it.
func (a *Application) CanonicalAppID() string {
	if a == nil {
		return ""
	}
	if strings.TrimSpace(a.AppID) != "" {
		return strings.TrimSpace(a.AppID)
	}
	return strings.TrimSpace(a.ID)
}

// RouteStateFor derives the typed route projection from legacy fields without
// mutating the application. This is useful to adapters that need to project a
// route while the application contains runtime state protected by a mutex.
func (a *Application) RouteStateFor(workspace *Workspace) RouteState {
	if a == nil {
		return RouteState{}
	}
	state := a.Route
	mode := RouteModeRemote
	if a.Active || (workspace != nil && workspace.IsLocalApps()) {
		mode = RouteModeLocal
	}
	state.DesiredMode = mode
	state.EffectiveMode = RouteModeUnknown
	state.LocalTarget = ""
	state.RemoteTarget = ""
	if a.Port > 0 {
		state.LocalTarget = fmt.Sprintf("%s://%s:%d", ProtocolHTTP, Localhost, a.Port)
	}
	if workspace != nil && !workspace.IsLocalApps() {
		state.RemoteTarget = a.GetRemoteBaseUrl(workspace)
	}
	// A model-only projection has no knowledge of the proxy's loaded config.
	// Always clear effective fields here: retaining values from a previous
	// projection would make a provider outage or failed reload look verified.
	state.EffectiveMode = RouteModeUnknown
	state.Target = ""
	state.ProxyHealth = ProxyHealthUnknown
	state.VerificationStatus = RouteVerificationUnknown
	state.VerifiedAt = nil
	state.RemoteFallback = false
	return state
}

// RefreshRouteState derives the intent/static portion of the typed route
// projection from legacy fields. Effective mode and verification metadata are
// supplied by the registry after a proxy snapshot is loaded.
func (a *Application) RefreshRouteState(workspace *Workspace) {
	if a == nil {
		return
	}
	a.Route = a.RouteStateFor(workspace)
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := value.UTC()
	return &clone
}

// NormalizeIdentityToken normalizes a canonical ID or alias for lookup.
// Separators are normalized so human-facing repository names remain usable;
// callers still validate the resulting token before persisting it.
func NormalizeIdentityToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || unicode.IsSpace(r):
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// NormalizedAliases returns de-duplicated, normalized aliases in stable order.
func (a *Application) NormalizedAliases() []string {
	if a == nil {
		return nil
	}
	canonical := NormalizeIdentityToken(a.CanonicalAppID())
	seen := make(map[string]struct{}, len(a.Aliases))
	aliases := make([]string, 0, len(a.Aliases))
	for _, raw := range a.Aliases {
		alias := NormalizeIdentityToken(raw)
		if alias == "" || alias == canonical {
			continue
		}
		if _, ok := seen[alias]; ok {
			continue
		}
		seen[alias] = struct{}{}
		aliases = append(aliases, alias)
	}
	return aliases
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
	return fmt.Sprintf("%s://%s:%d%s/%s", ProtocolHTTP, Localhost, a.Port, a.BackendContextPath(), a.Health)
}

// LocalHealthURL returns the local health check URL (path-based)
func (a *Application) LocalHealthURL() string {
	return fmt.Sprintf("%s://%s:%d%s/%s", ProtocolHTTP, Localhost, a.Port, a.BackendContextPath(), a.Health)
}

// RemoteHealthURL returns the remote health check URL (path-based)
func (a *Application) RemoteHealthURL(defaultRemoteBaseUrl string) string {
	context := a.BackendContextPath()
	if context == "" {
		context = a.PublicRoutePath()
	}
	context = strings.Trim(context, "/")
	if context == "" {
		return fmt.Sprintf("%s/%s", strings.TrimRight(defaultRemoteBaseUrl, "/"), strings.TrimLeft(a.Health, "/"))
	}
	return fmt.Sprintf("%s/%s/%s", strings.TrimRight(defaultRemoteBaseUrl, "/"), context, strings.TrimLeft(a.Health, "/"))
}

// Workspace represents a workspace with its applications
type Workspace struct {
	WorkspaceID   string             `json:"workspaceId"`
	Type          string             `json:"type,omitempty"`
	Name          *string            `json:"name,omitempty"`
	Color         *string            `json:"color,omitempty"`
	RoutingConfig *RoutingConfig     `json:"routingConfig,omitempty"`
	Applications  []*Application     `json:"applications"`
	Route         RouteState         `json:"route"`
	LastOperation *OperationMetadata `json:"lastOperation,omitempty"`
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
		app.NormalizeRouteMetadata()
		app.SetWsID(w.WorkspaceID)
		app.RefreshRouteState(w)
	}
}

// RefreshRouteStates recalculates persisted intent and static targets for all
// applications in a workspace. Effective proxy state is supplied by the
// registry after a successful proxy load.
func (w *Workspace) RefreshRouteStates() {
	if w == nil {
		return
	}
	for _, app := range w.Applications {
		app.RefreshRouteState(w)
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
	ID            string   `json:"id"`
	AppID         string   `json:"appId,omitempty"`
	ServiceID     string   `json:"serviceId,omitempty"`
	Repository    string   `json:"repository,omitempty"`
	Aliases       []string `json:"aliases,omitempty"`
	Name          string   `json:"name,omitempty"`
	Path          string   `json:"path"` // legacy public path
	PublicPath    string   `json:"publicPath,omitempty"`
	Domain        string   `json:"domain,omitempty"`
	RemoteBaseUrl string   `json:"remoteBaseUrl,omitempty"` // Per-app remote base URL override
	Port          int      `json:"port"`
	Health        string   `json:"health,omitempty"`
	Active        bool     `json:"active"`
	RoutePattern  *string  `json:"routePattern,omitempty"`
	RouteOverride bool     `json:"routeOverride,omitempty"`
	// RouteOverrideSet distinguishes an omitted update from an explicit false.
	RouteOverrideSet      bool               `json:"-"`
	Context               string             `json:"context,omitempty"` // legacy upstream context
	UpstreamContext       string             `json:"upstreamContext,omitempty"`
	FrontendRoot          string             `json:"frontendRoot,omitempty"`
	FrontendRootSource    string             `json:"frontendRootSource,omitempty"`
	LegacyPublicPath      bool               `json:"-"`
	LegacyUpstreamContext bool               `json:"-"`
	StripOrigin           bool               `json:"stripOrigin,omitempty"` // Strip Origin header for local proxy (Quarkus Dev UI fix)
	Route                 RouteState         `json:"route,omitempty"`
	LastOperation         *OperationMetadata `json:"lastOperation,omitempty"`
}

// PublicRoutePath returns the canonical public path while preserving the
// legacy path field for existing config files and clients.
func (a ApplicationConfig) PublicRoutePath() string {
	if strings.TrimSpace(a.PublicPath) != "" {
		return strings.TrimSpace(a.PublicPath)
	}
	return strings.TrimSpace(a.Path)
}

// IngressPath returns the browser-facing path used by the legacy renderer.
// Config values loaded from old path/context-only records keep their former
// context-as-ingress behavior until an explicit publicPath is registered.
func (a ApplicationConfig) IngressPath() string {
	if a.LegacyPublicPath && a.LegacyUpstreamContext && strings.TrimSpace(a.Context) != "" {
		return strings.TrimSpace(a.Context)
	}
	if strings.TrimSpace(a.PublicPath) != "" {
		return strings.TrimSpace(a.PublicPath)
	}
	if strings.TrimSpace(a.PublicPath) == "" && strings.TrimSpace(a.UpstreamContext) == "" && strings.TrimSpace(a.Context) != "" {
		return strings.TrimSpace(a.Context)
	}
	return strings.TrimSpace(a.Path)
}

// BackendContextPath returns the canonical upstream context while preserving
// the legacy context field for existing config files and clients.
func (a ApplicationConfig) BackendContextPath() string {
	if strings.TrimSpace(a.UpstreamContext) != "" {
		return strings.TrimSpace(a.UpstreamContext)
	}
	return strings.TrimSpace(a.Context)
}

// NormalizeRouteMetadata resolves legacy path/context values into the
// explicit fields and mirrors them back to legacy names for compatibility.
func (a *ApplicationConfig) NormalizeRouteMetadata() {
	if a == nil {
		return
	}
	if strings.TrimSpace(a.PublicPath) == "" && strings.TrimSpace(a.Path) != "" {
		a.LegacyPublicPath = true
		a.PublicPath = strings.TrimSpace(a.Path)
	}
	if strings.TrimSpace(a.Path) == "" {
		a.Path = strings.TrimSpace(a.PublicPath)
	}
	if strings.TrimSpace(a.UpstreamContext) == "" && strings.TrimSpace(a.Context) != "" {
		a.LegacyUpstreamContext = true
		a.UpstreamContext = strings.TrimSpace(a.Context)
	}
	if strings.TrimSpace(a.Context) == "" {
		a.Context = strings.TrimSpace(a.UpstreamContext)
	}
	a.Path = normalizeMetadataPath(a.Path)
	a.PublicPath = normalizeMetadataPath(a.PublicPath)
	a.Context = normalizeMetadataPath(a.Context)
	a.UpstreamContext = normalizeMetadataPath(a.UpstreamContext)
	a.FrontendRoot = normalizeMetadataPath(a.FrontendRoot)
}

func normalizeMetadataPath(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 1 {
		value = strings.TrimRight(value, "/")
	}
	return value
}

// CanonicalAppID returns the stable application identity, falling back to the
// legacy ID field for persisted configurations created before identity support.
func (a ApplicationConfig) CanonicalAppID() string {
	if strings.TrimSpace(a.AppID) != "" {
		return strings.TrimSpace(a.AppID)
	}
	return strings.TrimSpace(a.ID)
}

// NormalizedAliases returns the normalized aliases configured for this app.
func (a ApplicationConfig) NormalizedAliases() []string {
	app := Application{ID: a.ID, AppID: a.AppID, Aliases: a.Aliases}
	return app.NormalizedAliases()
}

// WorkspaceConfig represents a persisted workspace definition.
type WorkspaceConfig struct {
	ID            string              `json:"id"`
	Type          string              `json:"type,omitempty"`
	Name          string              `json:"name,omitempty"`
	Color         string              `json:"color,omitempty"`
	Routing       RoutingConfig       `json:"routing"`
	Applications  []ApplicationConfig `json:"applications"`
	Route         RouteState          `json:"route,omitempty"`
	LastOperation *OperationMetadata  `json:"lastOperation,omitempty"`
}
