package services

import (
	"errors"
	"fmt"
	"strings"

	"github.com/thiagojdb/rementor/internal/models"
)

var (
	// ErrBrowserURLBinding is returned when an application identity exists but
	// the selected environment does not provide enough public binding metadata
	// to construct a stable browser entry point.
	ErrBrowserURLBinding = errors.New("browser URL binding is missing")
)

// BrowserURLBindingError describes a missing public host/path binding without
// collapsing it into a generic not-found error. RPC callers map this to a
// structured failed-precondition response.
type BrowserURLBindingError struct {
	WorkspaceID string
	AppID       string
	Field       string
}

func (e *BrowserURLBindingError) Error() string {
	if e == nil {
		return ErrBrowserURLBinding.Error()
	}
	return fmt.Sprintf("browser URL binding is missing %s for %s/%s", e.Field, e.WorkspaceID, e.AppID)
}

func (e *BrowserURLBindingError) Unwrap() error { return ErrBrowserURLBinding }

// BrowserURLResolution is the shared result returned by RPC, CLI, MCP, and
// the browser client. URL/BrowserURL is the stable public entry point;
// Target/LocalTarget/RemoteTarget describe the current proxy destination and
// are intentionally separate so route toggles never rewrite the browser URL.
type BrowserURLResolution struct {
	WorkspaceID     string                         `json:"workspaceId"`
	Environment     string                         `json:"environment"`
	ApplicationRef  string                         `json:"applicationRef"`
	CanonicalAppID  string                         `json:"appId"`
	ServiceID       string                         `json:"serviceId,omitempty"`
	Repository      string                         `json:"repository,omitempty"`
	PublicHost      string                         `json:"publicHost"`
	PublicPath      string                         `json:"publicPath"`
	URL             string                         `json:"url"`
	BrowserURL      string                         `json:"browserUrl"`
	Target          string                         `json:"target,omitempty"`
	LocalTarget     string                         `json:"localTarget,omitempty"`
	RemoteTarget    string                         `json:"remoteTarget,omitempty"`
	DesiredMode     string                         `json:"desiredMode"`
	EffectiveMode   string                         `json:"effectiveMode"`
	RouteVersion    uint64                         `json:"routeVersion"`
	OperationID     string                         `json:"operationId,omitempty"`
	CorrelationID   string                         `json:"correlationId,omitempty"`
	Route           *Route                         `json:"route,omitempty"`
	RouteState      models.RouteState              `json:"routeState"`
	Identity        models.ApplicationIdentity     `json:"identity"`
	EnvironmentRef  models.WorkspaceEnvironmentRef `json:"environmentRef"`
	Operation       *models.OperationMetadata      `json:"operation,omitempty"`
	Precedence      int                            `json:"precedence"`
	MatchingPattern string                         `json:"matchingPattern,omitempty"`
}

// URLResolution is a concise compatibility name for integrations that expose
// the operation as URL resolution rather than browser URL resolution.
type URLResolution = BrowserURLResolution

// ResolveBrowserURL resolves a canonical application ID or alias inside one
// workspace/environment and returns its stable public browser entry point.
// The resolver reads a detached snapshot, so a concurrent route mutation
// cannot produce a result assembled from two different workspace versions.
func (r *Registry) ResolveBrowserURL(workspaceID, applicationRef string) (BrowserURLResolution, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	applicationRef = strings.TrimSpace(applicationRef)
	workspaces := r.workspaceSnapshot()
	ws := findWorkspace(workspaces, workspaceID)
	if ws == nil {
		return BrowserURLResolution{}, fmt.Errorf("workspace not found: %s", workspaceID)
	}
	_, app, err := findAppInWorkspace(ws, applicationRef)
	if err != nil {
		// Identity metadata is global, while the route/public binding is
		// environment-specific. If the reference is valid elsewhere, surface a
		// typed binding error instead of making callers guess whether the alias
		// itself is invalid.
		if !errors.Is(err, models.ErrAmbiguousApplication) {
			for _, other := range workspaces {
				if other == ws {
					continue
				}
				_, candidate, candidateErr := findAppInWorkspace(other, applicationRef)
				if candidateErr == nil && candidate != nil {
					return BrowserURLResolution{}, &BrowserURLBindingError{WorkspaceID: ws.WorkspaceID, AppID: candidate.CanonicalAppID(), Field: "environment binding"}
				}
			}
		}
		return BrowserURLResolution{}, err
	}
	if ws == nil || app == nil {
		return BrowserURLResolution{}, &BrowserURLBindingError{WorkspaceID: workspaceID, AppID: applicationRef, Field: "environment"}
	}

	host := browserPublicHost(ws, app)
	if host == "" {
		return BrowserURLResolution{}, &BrowserURLBindingError{WorkspaceID: ws.WorkspaceID, AppID: app.CanonicalAppID(), Field: "public host"}
	}
	path := browserPublicPath(ws, app)
	if path == "" {
		return BrowserURLResolution{}, &BrowserURLBindingError{WorkspaceID: ws.WorkspaceID, AppID: app.CanonicalAppID(), Field: "public path"}
	}

	mode := appMode(ws, app)
	if mode == "local" && app.Port == 0 {
		// Match the normalized route builder: a local route without a local
		// listener is effectively remote/fallback, while its public URL stays
		// unchanged.
		mode = "remote"
	}

	// Prefer the exact normalized route emitted for this app and public entry
	// point. This keeps precedence/effective-mode metadata identical to route
	// inspection and nginx rendering, including root/context special cases.
	var resolvedRoute *Route
	for _, route := range buildNormalizedRoutes(ws) {
		if route.CanonicalAppID != app.CanonicalAppID() || normalizeHost(route.PublicHost) != host {
			continue
		}
		if !routeMatches(route, path) {
			continue
		}
		if resolvedRoute == nil || routePrecedes(route, *resolvedRoute) {
			resolvedRoute = cloneRoute(&route)
		}
	}

	if resolvedRoute == nil {
		pattern := path
		if path == "/" {
			pattern = "/*"
		}
		fallback := buildRoute(ws, app, host, pattern, mode)
		resolvedRoute = &fallback
	}
	if mode == "remote" && resolvedRoute.Target == "" {
		return BrowserURLResolution{}, &BrowserURLBindingError{WorkspaceID: ws.WorkspaceID, AppID: app.CanonicalAppID(), Field: "remote target"}
	}

	state := app.RouteStateFor(ws)
	state.DesiredMode = resolvedRoute.DesiredMode
	state.EffectiveMode = resolvedRoute.EffectiveMode
	state.Target = resolvedRoute.Target
	state.LocalTarget = resolvedRoute.LocalTarget
	state.RemoteTarget = resolvedRoute.RemoteTarget
	state.RemoteFallback = resolvedRoute.RemoteFallback
	version := app.Route.RouteVersion
	if ws.Route.RouteVersion > version {
		version = ws.Route.RouteVersion
	}
	state.RouteVersion = version
	state.OperationID = app.Route.OperationID
	if state.OperationID == "" {
		state.OperationID = ws.Route.OperationID
	}

	operation := cloneOperation(app.LastOperation)
	if operation == nil {
		operation = cloneOperation(ws.LastOperation)
	}
	if operation != nil {
		if operation.RouteVersion > version {
			version = operation.RouteVersion
			state.RouteVersion = version
		}
		state.OperationID = operation.OperationID
	}

	identity := models.ApplicationIdentity{AppID: app.CanonicalAppID(), ServiceID: app.ServiceID, Repository: app.Repository, Aliases: app.NormalizedAliases()}
	if app.ID != app.CanonicalAppID() {
		identity.LegacyID = app.ID
	}

	return BrowserURLResolution{
		WorkspaceID: ws.WorkspaceID, Environment: ws.WorkspaceID, ApplicationRef: applicationRef,
		CanonicalAppID: app.CanonicalAppID(), ServiceID: app.ServiceID, Repository: app.Repository,
		PublicHost: host, PublicPath: path, URL: "http://" + host + path, BrowserURL: "http://" + host + path,
		Target: resolvedRoute.Target, LocalTarget: resolvedRoute.LocalTarget, RemoteTarget: resolvedRoute.RemoteTarget,
		DesiredMode: resolvedRoute.DesiredMode, EffectiveMode: resolvedRoute.EffectiveMode,
		RouteVersion: version, OperationID: state.OperationID,
		CorrelationID: operationCorrelation(operation), Route: cloneRoute(resolvedRoute), RouteState: state,
		Identity:       identity,
		EnvironmentRef: models.WorkspaceEnvironmentRef{WorkspaceID: ws.WorkspaceID, Environment: ws.WorkspaceID, LegacyID: ws.WorkspaceID},
		Operation:      operation, Precedence: resolvedRoute.Precedence, MatchingPattern: resolvedRoute.Pattern,
	}, nil
}

// ResolveURL is a shorter compatibility alias for callers that do not use the
// full BrowserURL operation name.
func (r *Registry) ResolveURL(workspaceID, applicationRef string) (BrowserURLResolution, error) {
	return r.ResolveBrowserURL(workspaceID, applicationRef)
}

// ResolveApplicationURL is an explicit application-oriented alias retained
// for integrations that name the operation after the identity being resolved.
func (r *Registry) ResolveApplicationURL(workspaceID, applicationRef string) (BrowserURLResolution, error) {
	return r.ResolveBrowserURL(workspaceID, applicationRef)
}

func operationCorrelation(operation *models.OperationMetadata) string {
	if operation == nil {
		return ""
	}
	return operation.CorrelationID
}

func browserPublicHost(ws *models.Workspace, app *models.Application) string {
	if ws == nil || app == nil {
		return ""
	}
	if ws.IsLocalApps() {
		return normalizeHost(app.Domain)
	}
	if app.Domain != "" {
		return normalizeHost(app.Domain)
	}
	return normalizeHost(ws.GetLocalDomain())
}

func browserPublicPath(ws *models.Workspace, app *models.Application) string {
	if ws != nil && ws.IsLocalApps() {
		return "/"
	}
	if app == nil {
		return ""
	}
	// Reuse the normalized route entries so the browser entry follows the same
	// public context path nginx exposes. This covers explicit route patterns,
	// nested context paths, and the root/context special case without
	// duplicating the renderer's route rules here.
	entries := routePatternEntries(app)
	if len(entries) > 0 {
		match, _, _ := routePatternInfo(entries[0].Pattern)
		return normalizeRequestPath(match)
	}
	return normalizeRequestPath(app.Context)
}

func routePrecedes(left, right Route) bool {
	if left.Precedence != right.Precedence {
		return left.Precedence > right.Precedence
	}
	if left.Exact != right.Exact {
		return left.Exact
	}
	return routeSortKey(left) < routeSortKey(right)
}
