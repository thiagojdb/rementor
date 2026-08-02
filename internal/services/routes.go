package services

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/thiagojdb/rementor/internal/models"
)

// Route is the normalized route representation used by route inspection,
// resolution, planning, and application. It deliberately mirrors the
// semantics emitted by the nginx renderer instead of exposing the legacy
// Application fields directly.
type Route struct {
	WorkspaceID        string     `json:"workspaceId"`
	Environment        string     `json:"environment"`
	PublicHost         string     `json:"publicHost"`
	Pattern            string     `json:"pattern"`
	CanonicalAppID     string     `json:"appId,omitempty"`
	ServiceID          string     `json:"serviceId,omitempty"`
	Repository         string     `json:"repository,omitempty"`
	DesiredMode        string     `json:"desiredMode"`
	EffectiveMode      string     `json:"effectiveMode"`
	Target             string     `json:"target,omitempty"`
	LocalTarget        string     `json:"localTarget,omitempty"`
	RemoteTarget       string     `json:"remoteTarget,omitempty"`
	RemoteFallback     bool       `json:"remoteFallback"`
	UpstreamContext    string     `json:"upstreamContext,omitempty"`
	Precedence         int        `json:"precedence"`
	PrecedenceReason   string     `json:"precedenceReason"`
	Exact              bool       `json:"exact"`
	ProxyHealth        string     `json:"proxyHealth,omitempty"`
	VerificationStatus string     `json:"verificationStatus,omitempty"`
	RouteVersion       uint64     `json:"routeVersion,omitempty"`
	OperationID        string     `json:"operationId,omitempty"`
	VerifiedAt         *time.Time `json:"verifiedAt,omitempty"`
	// IntentionalOverride is copied from the owning application metadata (and
	// set on generated server fallbacks) so consumers can distinguish accepted
	// overlap from accidental ownership without changing nginx's behavior.
	IntentionalOverride bool `json:"intentionalOverride,omitempty"`
}

// RouteWarning is a non-fatal issue surfaced by a plan or sync operation.
type RouteWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// RouteConflict describes two routes competing for the same public request.
// Conflict detection is intentionally deterministic so a plan never depends
// on application registration order.
type RouteConflict struct {
	WorkspaceID string `json:"workspaceId"`
	Environment string `json:"environment"`
	PublicHost  string `json:"publicHost"`
	// Pattern remains the normalized pattern that identifies the conflict. For
	// shadowing conflicts it is the winning (narrower) pattern, while the
	// explicit winning/shadowed fields carry both route entries.
	Pattern          string `json:"pattern"`
	AppID            string `json:"appId"`
	ConflictingAppID string `json:"conflictingAppId"`
	WinningAppID     string `json:"winningAppId"`
	Reason           string `json:"reason"`

	// The original pair fields above are retained for wire compatibility. The
	// fields below make ownership and precedence explicit for API/MCP clients.
	AppServiceID             string `json:"appServiceId,omitempty"`
	ConflictingServiceID     string `json:"conflictingServiceId,omitempty"`
	WinningServiceID         string `json:"winningServiceId,omitempty"`
	ShadowedAppID            string `json:"shadowedAppId,omitempty"`
	ShadowedServiceID        string `json:"shadowedServiceId,omitempty"`
	WinningPattern           string `json:"winningPattern,omitempty"`
	ShadowedPattern          string `json:"shadowedPattern,omitempty"`
	WinningPrecedence        int    `json:"winningPrecedence,omitempty"`
	ShadowedPrecedence       int    `json:"shadowedPrecedence,omitempty"`
	WinningPrecedenceReason  string `json:"winningPrecedenceReason,omitempty"`
	ShadowedPrecedenceReason string `json:"shadowedPrecedenceReason,omitempty"`
	PrecedenceReason         string `json:"precedenceReason,omitempty"`
	Intentional              bool   `json:"intentional"`
	WinningRoute             *Route `json:"winningRoute,omitempty"`
	ShadowedRoute            *Route `json:"shadowedRoute,omitempty"`
}

// RouteChange records the before/after route entries affected by a plan.
type RouteChange struct {
	ApplicationID string `json:"applicationId"`
	Before        *Route `json:"before,omitempty"`
	After         *Route `json:"after,omitempty"`
}

// RoutePlan is immutable output from PlanRoute and the input contract for
// ApplyRoutePlan. ApplicationID/DesiredMode/RoutePattern make the intended
// mutation explicit even when a route currently has no generated entry.
type RoutePlan struct {
	WorkspaceID      string          `json:"workspaceId"`
	Environment      string          `json:"environment"`
	BaseRouteVersion uint64          `json:"baseRouteVersion"`
	ApplicationID    string          `json:"applicationId,omitempty"`
	DesiredMode      string          `json:"desiredMode,omitempty"`
	RoutePattern     *string         `json:"routePattern,omitempty"`
	Before           []Route         `json:"beforeRoutes"`
	After            []Route         `json:"afterRoutes"`
	Changes          []RouteChange   `json:"changes"`
	Warnings         []RouteWarning  `json:"warnings"`
	Conflicts        []RouteConflict `json:"conflicts"`
	Fingerprint      string          `json:"fingerprint"`
}

// RouteResolution is the result of applying nginx-like exact/prefix
// precedence to a host and request path.
type RouteResolution struct {
	WorkspaceID      string `json:"workspaceId"`
	Environment      string `json:"environment"`
	Host             string `json:"host"`
	Path             string `json:"path"`
	Route            *Route `json:"route"`
	MatchingPattern  string `json:"matchingPattern"`
	CanonicalAppID   string `json:"appId,omitempty"`
	ServiceID        string `json:"serviceId,omitempty"`
	Target           string `json:"target,omitempty"`
	Precedence       int    `json:"precedence"`
	PrecedenceReason string `json:"precedenceReason"`
}

// RouteSyncResult reports whether the proxy projection had to be repaired.
type RouteSyncResult struct {
	WorkspaceID           string                    `json:"workspaceId"`
	Changed               bool                      `json:"changed"`
	Verified              bool                      `json:"verified"`
	Status                string                    `json:"status"`
	DesiredRouteVersion   uint64                    `json:"desiredRouteVersion"`
	EffectiveRouteVersion uint64                    `json:"effectiveRouteVersion"`
	Routes                []Route                   `json:"routes"`
	Warnings              []RouteWarning            `json:"warnings"`
	Degraded              bool                      `json:"degraded,omitempty"`
	Rollback              string                    `json:"rollbackStatus,omitempty"`
	Operation             *models.OperationMetadata `json:"operation,omitempty"`
}

var (
	// ErrRouteVersionConflict is returned when a plan was generated against an
	// older desired route version.
	ErrRouteVersionConflict = errors.New("route version conflict")
	// ErrRouteIdempotencyConflict means an idempotency key was reused for a
	// different plan.
	ErrRouteIdempotencyConflict = errors.New("route idempotency key conflict")
)

type RouteVersionConflictError struct {
	WorkspaceID string
	Expected    uint64
	Actual      uint64
}

func (e *RouteVersionConflictError) Error() string {
	return fmt.Sprintf("route version conflict for workspace %q: expected %d, current %d", e.WorkspaceID, e.Expected, e.Actual)
}

func (e *RouteVersionConflictError) Unwrap() error { return ErrRouteVersionConflict }

type RouteIdempotencyConflictError struct {
	Key string
}

func (e *RouteIdempotencyConflictError) Error() string {
	return fmt.Sprintf("idempotency key %q was already used for a different route plan", e.Key)
}

func (e *RouteIdempotencyConflictError) Unwrap() error { return ErrRouteIdempotencyConflict }

type routePatternEntry struct {
	Pattern string
	Exact   bool
}

type routePlanInput struct {
	WorkspaceID  string
	Application  string
	DesiredMode  string
	RoutePattern *string
}

func normalizeMode(mode string) (string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "local" && mode != "remote" {
		return "", fmt.Errorf("route mode must be local or remote")
	}
	return mode, nil
}

func normalizeRequestPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	if index := strings.IndexAny(path, "?#"); index >= 0 {
		path = path[:index]
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
	}
	return path
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(host)
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

func routePatternEntries(app *models.Application) []routePatternEntry {
	// The nginx renderer treats a root application with an upstream context as
	// a special three-location route (exact root, exact context, context
	// prefix), even when a legacy route_pattern value is present.
	if app.Path == "/" && strings.TrimSpace(app.Context) != "" && normalizeRequestPath(app.Context) != "/" {
		context := normalizeRequestPath(app.Context)
		return []routePatternEntry{{Pattern: "/", Exact: true}, {Pattern: context, Exact: true}, {Pattern: context + "/*"}}
	}
	if app.RoutePattern != nil && strings.TrimSpace(*app.RoutePattern) != "" {
		pattern := normalizeRequestPath(*app.RoutePattern)
		if pattern == "/" {
			return []routePatternEntry{{Pattern: "/*"}}
		}
		_, exact, _ := routePatternInfo(pattern)
		return []routePatternEntry{{Pattern: pattern, Exact: exact}}
	}
	if app.Path == "/" || strings.TrimSpace(app.Path) == "" {
		return []routePatternEntry{{Pattern: "/*"}}
	}
	path := app.Context
	if path == "" {
		path = app.Path
	}
	path = normalizeRequestPath(path)
	return []routePatternEntry{{Pattern: path, Exact: true}, {Pattern: path + "/*"}}
}

func routePatternInfo(pattern string) (match string, exact bool, length int) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || pattern == "/" || pattern == "/*" {
		return "/", false, 0
	}
	if strings.HasSuffix(pattern, "/*") {
		prefix := normalizeRequestPath(strings.TrimSuffix(pattern, "/*"))
		return prefix, false, len(prefix)
	}
	return normalizeRequestPath(pattern), true, len(normalizeRequestPath(pattern))
}

func routeInfo(route Route) (match string, exact bool, length int) {
	match, exact, length = routePatternInfo(route.Pattern)
	if route.Exact {
		exact = true
	}
	return match, exact, length
}

func routeMatches(route Route, path string) bool {
	match, exact, _ := routeInfo(route)
	path = normalizeRequestPath(path)
	if exact {
		return path == match
	}
	if match == "/" {
		return true
	}
	if strings.HasSuffix(route.Pattern, "/*") {
		return strings.HasPrefix(path, match+"/")
	}
	return path == match || strings.HasPrefix(path, match+"/")
}

func routeSortKey(route Route) string {
	return strings.Join([]string{
		normalizeHost(route.PublicHost), route.Pattern, route.CanonicalAppID,
		route.ServiceID, route.Target,
	}, "\x00")
}

func sortRoutes(routes []Route) {
	sort.SliceStable(routes, func(i, j int) bool {
		left, right := routes[i], routes[j]
		if normalizeHost(left.PublicHost) != normalizeHost(right.PublicHost) {
			return normalizeHost(left.PublicHost) < normalizeHost(right.PublicHost)
		}
		if left.Precedence != right.Precedence {
			return left.Precedence > right.Precedence
		}
		if left.Pattern != right.Pattern {
			return left.Pattern < right.Pattern
		}
		return routeSortKey(left) < routeSortKey(right)
	})
}

func dedupeNormalizedRoutes(routes []Route) []Route {
	seen := make(map[string]int, len(routes))
	result := make([]Route, 0, len(routes))
	for _, route := range routes {
		key := strings.Join([]string{normalizeHost(route.PublicHost), route.Pattern, route.CanonicalAppID, route.ServiceID}, "\x00")
		if index, ok := seen[key]; ok {
			// A generated server fallback and an application-owned wildcard can
			// normalize to the same entry. Preserve the stronger metadata when
			// coalescing them so conflict analysis remains accurate.
			if route.IntentionalOverride {
				result[index].IntentionalOverride = true
			}
			continue
		}
		seen[key] = len(result)
		result = append(result, route)
	}
	return result
}

func cloneRoute(value *Route) *Route {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneRoutes(routes []Route) []Route {
	result := make([]Route, len(routes))
	copy(result, routes)
	return result
}

func routeTarget(ws *models.Workspace, app *models.Application, mode string) (target, local, remote string) {
	if app.Port > 0 {
		local = fmt.Sprintf("http://localhost:%d", app.Port)
	}
	if ws != nil {
		remote = app.GetRemoteBaseUrl(ws)
	}
	if mode == models.RouteModeLocal && local != "" {
		return local, local, remote
	}
	if remote != "" {
		return remote, local, remote
	}
	return "", local, remote
}

func buildRoute(ws *models.Workspace, app *models.Application, host, pattern string, mode string) Route {
	return buildRouteWithExact(ws, app, host, pattern, mode, false)
}

func buildRouteWithExact(ws *models.Workspace, app *models.Application, host, pattern string, mode string, exactOverride bool) Route {
	match, exact, length := routePatternInfo(pattern)
	if exactOverride {
		exact = true
	}
	canonical := ""
	serviceID := ""
	repository := ""
	if app != nil {
		canonical = app.CanonicalAppID()
		serviceID = app.ServiceID
		repository = app.Repository
	}
	target, local, remote := "", "", ""
	if app != nil {
		target, local, remote = routeTarget(ws, app, mode)
	}
	effectiveMode := mode
	switch {
	case target == "" && mode == models.RouteModeRemote:
		effectiveMode = models.RouteModeFallback
	case target == "":
		effectiveMode = models.RouteModeUnknown
	case mode == models.RouteModeLocal && target != local:
		effectiveMode = models.RouteModeRemote
	}
	precedence := length
	if exact {
		precedence += 100000
	}
	reason := "root fallback"
	if exact {
		reason = "exact match"
	} else if match != "/" {
		reason = "longest prefix"
	}
	return Route{
		WorkspaceID: ws.WorkspaceID, Environment: ws.WorkspaceID,
		PublicHost: normalizeHost(host), Pattern: pattern,
		CanonicalAppID: canonical, ServiceID: serviceID, Repository: repository,
		DesiredMode: mode, EffectiveMode: effectiveMode, Target: target,
		LocalTarget: local, RemoteTarget: remote, RemoteFallback: false,
		UpstreamContext: func() string {
			if app == nil {
				return ""
			}
			return app.Context
		}(),
		Precedence: precedence, PrecedenceReason: reason, Exact: exact,
		IntentionalOverride: app != nil && app.RouteOverride,
	}
}

func appendAppRoutes(routes *[]Route, ws *models.Workspace, app *models.Application, host, mode string) {
	if app == nil {
		return
	}
	for _, entry := range routePatternEntries(app) {
		*routes = append(*routes, buildRouteWithExact(ws, app, host, entry.Pattern, mode, entry.Exact))
	}
}

func appMode(ws *models.Workspace, app *models.Application) string {
	if ws.IsLocalApps() || app.Active {
		return models.RouteModeLocal
	}
	return models.RouteModeRemote
}

func rootAppSortKey(app *models.Application) string {
	if app == nil {
		return ""
	}
	return strings.Join([]string{app.CanonicalAppID(), app.ServiceID, app.ID}, "\x00")
}

// defaultRootApp selects a root application independently of registration
// order. If multiple root applications exist, their duplicate /* ownership is
// still reported; choosing the canonical first entry only keeps the generated
// fallback metadata stable for the conflict report.
func defaultRootApp(ws *models.Workspace) *models.Application {
	var root *models.Application
	for _, app := range ws.Applications {
		if app == nil || app.Domain != "" || normalizeRequestPath(app.Path) != "/" {
			continue
		}
		if root == nil || rootAppSortKey(app) < rootAppSortKey(root) {
			root = app
		}
	}
	return root
}

// buildNormalizedRoutes follows the same host and application ordering as the
// nginx renderer, then gives every entry an explicit precedence. The result is
// sorted afterwards so callers get stable output independent of config order.
func buildNormalizedRoutes(ws *models.Workspace) []Route {
	if ws == nil {
		return nil
	}
	routes := make([]Route, 0)
	if ws.IsLocalApps() {
		for _, app := range ws.Applications {
			if app.Domain == "" || app.Port == 0 {
				continue
			}
			routes = append(routes, buildRoute(ws, app, app.Domain, "/", "local"))
		}
		routes = dedupeNormalizedRoutes(routes)
		sortRoutes(routes)
		return routes
	}
	defaultHost := ws.GetLocalDomain()
	appendForHost := func(host string, domainApp *models.Application) {
		for _, app := range ws.Applications {
			if app.Domain != "" || (domainApp != nil && app.CanonicalAppID() == domainApp.CanonicalAppID()) {
				continue
			}
			mode := appMode(ws, app)
			if mode == models.RouteModeLocal && app.Port == 0 && app.GetRemoteBaseUrl(ws) == "" {
				continue
			}
			if mode == models.RouteModeRemote && app.GetRemoteBaseUrl(ws) == "" {
				continue
			}
			appendAppRoutes(&routes, ws, app, host, mode)
		}
		if domainApp != nil {
			mode := appMode(ws, domainApp)
			// nginx only emits a remote location for a domain-bound app when
			// that app declares its own remote base. The workspace default is
			// reserved for the server fallback in this case.
			if (mode == models.RouteModeLocal && domainApp.Port > 0) || (domainApp.RemoteBaseUrl != "" && domainApp.GetRemoteBaseUrl(ws) != "") {
				appendAppRoutes(&routes, ws, domainApp, host, mode)
			}
		}
		rootApp := domainApp
		if rootApp == nil {
			rootApp = defaultRootApp(ws)
		}
		if rootApp != nil && rootApp.Active && rootApp.Port > 0 {
			fallback := buildRoute(ws, rootApp, host, "/*", models.RouteModeLocal)
			fallback.IntentionalOverride = true
			routes = append(routes, fallback)
		} else {
			fallbackMode := models.RouteModeRemote
			if rootApp != nil {
				fallbackMode = appMode(ws, rootApp)
			}
			fallback := buildRoute(ws, rootApp, host, "/*", fallbackMode)
			// The server-level fallback is a deliberate catch-all in nginx. It
			// may be shadowed by application-specific routes by design, so mark
			// the generated entry for intentional shadow classification while
			// still treating duplicate ownership as accidental.
			fallback.IntentionalOverride = true
			if rootApp == nil {
				fallback.Target = ws.GetDefaultRemoteBaseURL()
				fallback.RemoteTarget = fallback.Target
				fallback.RemoteFallback = false
				if fallback.Target != "" {
					fallback.EffectiveMode = models.RouteModeRemote
				}
			}
			routes = append(routes, fallback)
		}
	}
	appendForHost(defaultHost, nil)
	for _, app := range ws.Applications {
		if app.Domain != "" {
			appendForHost(app.Domain, app)
		}
	}
	routes = dedupeNormalizedRoutes(routes)
	sortRoutes(routes)
	return routes
}

// NormalizedRoutes exposes the canonical route projection to renderers and
// adapters. The builder is read-only with respect to the supplied workspace.
func NormalizedRoutes(ws *models.Workspace) []Route {
	return buildNormalizedRoutes(ws)
}

// RouteConflicts exposes the canonical ownership analysis used by every
// control surface and by the nginx renderer. The returned slice is stable for
// a given normalized workspace and does not mutate the workspace.
func RouteConflicts(ws *models.Workspace) []RouteConflict {
	if ws == nil {
		return nil
	}
	return conflictsForRoutes(buildNormalizedRoutes(cloneWorkspace(ws)))
}

// RoutePatterns returns the normalized wildcard patterns emitted for one
// application. Nginx uses these patterns for its location blocks while its
// root-context special case still adds the exact redirect/rewrite locations.
func RoutePatterns(ws *models.Workspace, app *models.Application) []string {
	if ws == nil || app == nil {
		return nil
	}
	appID := app.CanonicalAppID()
	seen := make(map[string]struct{})
	patterns := make([]string, 0)
	for _, route := range buildNormalizedRoutes(ws) {
		if route.CanonicalAppID != appID {
			continue
		}
		if _, ok := seen[route.Pattern]; ok {
			continue
		}
		seen[route.Pattern] = struct{}{}
		patterns = append(patterns, route.Pattern)
	}
	return patterns
}

func routeEquivalent(left, right Route) bool {
	return left.WorkspaceID == right.WorkspaceID && left.PublicHost == right.PublicHost && left.Pattern == right.Pattern && left.Exact == right.Exact && left.CanonicalAppID == right.CanonicalAppID && left.ServiceID == right.ServiceID && left.DesiredMode == right.DesiredMode && left.EffectiveMode == right.EffectiveMode && left.Target == right.Target && left.LocalTarget == right.LocalTarget && left.RemoteTarget == right.RemoteTarget && left.RemoteFallback == right.RemoteFallback && left.UpstreamContext == right.UpstreamContext && left.IntentionalOverride == right.IntentionalOverride
}

func routeChangeKey(route Route) string {
	return strings.Join([]string{route.PublicHost, route.Pattern, route.CanonicalAppID, route.ServiceID}, "\x00")
}

func diffRoutes(before, after []Route) []RouteChange {
	left := make(map[string]Route, len(before))
	right := make(map[string]Route, len(after))
	for _, route := range before {
		left[routeChangeKey(route)] = route
	}
	for _, route := range after {
		right[routeChangeKey(route)] = route
	}
	keys := make([]string, 0)
	seen := make(map[string]struct{})
	for key := range left {
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for key := range right {
		if _, ok := seen[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	changes := make([]RouteChange, 0)
	for _, key := range keys {
		beforeRoute, beforeOK := left[key]
		afterRoute, afterOK := right[key]
		if beforeOK && afterOK && routeEquivalent(beforeRoute, afterRoute) {
			continue
		}
		change := RouteChange{ApplicationID: afterRoute.CanonicalAppID}
		if change.ApplicationID == "" {
			change.ApplicationID = beforeRoute.CanonicalAppID
		}
		if beforeOK {
			change.Before = cloneRoute(&beforeRoute)
		}
		if afterOK {
			change.After = cloneRoute(&afterRoute)
		}
		changes = append(changes, change)
	}
	return changes
}

// routeOwnerKey identifies the route owner without relying on registration
// order. A blank owner is a generated/default fallback rather than an
// application-owned route and is intentionally excluded from ownership
// conflicts.
func routeOwnerKey(route Route) string {
	appID := strings.TrimSpace(route.CanonicalAppID)
	serviceID := strings.TrimSpace(route.ServiceID)
	if appID == "" && serviceID == "" {
		return ""
	}
	return appID + "\x00" + serviceID
}

// routeMatchersOverlap answers whether two normalized nginx locations can
// receive the same request. Exact locations only overlap another exact
// location at the same path; a prefix overlaps an exact path beneath it and
// two prefixes overlap when either prefix contains the other.
func routeMatchersOverlap(left, right Route) bool {
	leftMatch, leftExact, _ := routeInfo(left)
	rightMatch, rightExact, _ := routeInfo(right)
	switch {
	case leftExact && rightExact:
		return leftMatch == rightMatch
	case leftExact:
		return prefixRouteContains(right, leftMatch)
	case rightExact:
		return prefixRouteContains(left, rightMatch)
	default:
		return prefixContains(leftMatch, rightMatch) || prefixContains(rightMatch, leftMatch)
	}
}

func prefixRouteContains(route Route, path string) bool {
	match, exact, _ := routeInfo(route)
	if exact {
		return normalizeRequestPath(path) == match
	}
	if match == "/" {
		return true
	}
	// nginx's generated wildcard locations are rendered as `location
	// /prefix/`, so `/prefix` itself belongs to an exact location (if one
	// exists) rather than the wildcard prefix.
	if strings.HasSuffix(route.Pattern, "/*") {
		return strings.HasPrefix(normalizeRequestPath(path), match+"/")
	}
	path = normalizeRequestPath(path)
	return path == match || strings.HasPrefix(path, match+"/")
}

func prefixContains(prefix, path string) bool {
	prefix = normalizeRequestPath(prefix)
	path = normalizeRequestPath(path)
	if prefix == "/" {
		return true
	}
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

func routeConflictSortKey(route Route) string {
	match, exact, length := routeInfo(route)
	return strings.Join([]string{
		normalizeHost(route.PublicHost), match, fmt.Sprintf("%t", exact), fmt.Sprintf("%06d", length),
		route.CanonicalAppID, route.ServiceID, route.Pattern,
	}, "\x00")
}

// routeConflictWinner applies nginx's location precedence and uses canonical
// identity as the final tie breaker. The latter is important for duplicate
// patterns: insertion order must never decide ownership.
func routeConflictWinner(left, right Route) (winning, shadowed Route, samePattern bool) {
	leftMatch, leftExact, leftLength := routeInfo(left)
	rightMatch, rightExact, rightLength := routeInfo(right)
	if leftMatch == rightMatch && leftExact == rightExact {
		if routeOwnerKey(right) < routeOwnerKey(left) || (routeOwnerKey(right) == routeOwnerKey(left) && routeConflictSortKey(right) < routeConflictSortKey(left)) {
			return right, left, true
		}
		return left, right, true
	}
	if leftExact != rightExact {
		if leftExact {
			return left, right, false
		}
		return right, left, false
	}
	if leftLength != rightLength {
		if leftLength > rightLength {
			return left, right, false
		}
		return right, left, false
	}
	// This is defensive for unusual manually constructed normalized routes.
	if routeConflictSortKey(right) < routeConflictSortKey(left) {
		return right, left, false
	}
	return left, right, false
}

func conflictPrecedenceReason(winning, shadowed Route, samePattern bool) string {
	if samePattern {
		return "same precedence; canonical identity tie-breaker"
	}
	_, winningExact, _ := routeInfo(winning)
	_, shadowedExact, _ := routeInfo(shadowed)
	if winningExact && !shadowedExact {
		return "exact location takes precedence over prefix location"
	}
	if !winningExact && !shadowedExact {
		return fmt.Sprintf("longer prefix %q takes precedence over %q", winning.Pattern, shadowed.Pattern)
	}
	return fmt.Sprintf("%s wins over %s", winning.PrecedenceReason, shadowed.PrecedenceReason)
}

func conflictIsIntentional(left, right Route, samePattern bool) bool {
	if samePattern {
		// Duplicate patterns have no nginx precedence winner, so every owner
		// must explicitly accept the overlap before it is allowed.
		return left.IntentionalOverride && right.IntentionalOverride
	}
	return left.IntentionalOverride || right.IntentionalOverride
}

func conflictsForRoutes(routes []Route) []RouteConflict {
	conflicts := make([]RouteConflict, 0)
	for i := 0; i < len(routes); i++ {
		for j := i + 1; j < len(routes); j++ {
			left, right := routes[i], routes[j]
			if normalizeHost(left.PublicHost) != normalizeHost(right.PublicHost) {
				continue
			}
			leftOwner, rightOwner := routeOwnerKey(left), routeOwnerKey(right)
			if leftOwner == "" || rightOwner == "" || leftOwner == rightOwner || !routeMatchersOverlap(left, right) {
				continue
			}

			// Keep the pair itself stable regardless of the caller's route order.
			pairLeft, pairRight := left, right
			if routeConflictSortKey(pairRight) < routeConflictSortKey(pairLeft) {
				pairLeft, pairRight = pairRight, pairLeft
			}
			winning, shadowed, samePattern := routeConflictWinner(pairLeft, pairRight)
			reason := "narrower route shadows broader route"
			if samePattern {
				reason = "same route pattern"
			}
			precedenceReason := conflictPrecedenceReason(winning, shadowed, samePattern)
			conflict := RouteConflict{
				WorkspaceID:              pairLeft.WorkspaceID,
				Environment:              pairLeft.Environment,
				PublicHost:               normalizeHost(pairLeft.PublicHost),
				Pattern:                  winning.Pattern,
				AppID:                    pairLeft.CanonicalAppID,
				ConflictingAppID:         pairRight.CanonicalAppID,
				WinningAppID:             winning.CanonicalAppID,
				Reason:                   reason,
				AppServiceID:             pairLeft.ServiceID,
				ConflictingServiceID:     pairRight.ServiceID,
				WinningServiceID:         winning.ServiceID,
				ShadowedAppID:            shadowed.CanonicalAppID,
				ShadowedServiceID:        shadowed.ServiceID,
				WinningPattern:           winning.Pattern,
				ShadowedPattern:          shadowed.Pattern,
				WinningPrecedence:        winning.Precedence,
				ShadowedPrecedence:       shadowed.Precedence,
				WinningPrecedenceReason:  winning.PrecedenceReason,
				ShadowedPrecedenceReason: shadowed.PrecedenceReason,
				PrecedenceReason:         precedenceReason,
				Intentional:              conflictIsIntentional(left, right, samePattern),
				WinningRoute:             cloneRoute(&winning),
				ShadowedRoute:            cloneRoute(&shadowed),
			}
			conflicts = append(conflicts, conflict)
		}
	}
	sort.Slice(conflicts, func(i, j int) bool {
		left, right := conflicts[i], conflicts[j]
		return strings.Join([]string{left.PublicHost, left.WinningPattern, left.ShadowedPattern, left.AppID, left.AppServiceID, left.ConflictingAppID, left.ConflictingServiceID}, "\x00") < strings.Join([]string{right.PublicHost, right.WinningPattern, right.ShadowedPattern, right.AppID, right.AppServiceID, right.ConflictingAppID, right.ConflictingServiceID}, "\x00")
	})
	return conflicts
}

func hasAccidentalConflicts(conflicts []RouteConflict) bool {
	for _, conflict := range conflicts {
		if !conflict.Intentional {
			return true
		}
	}
	return false
}

func countAccidentalConflicts(conflicts []RouteConflict) int {
	count := 0
	for _, conflict := range conflicts {
		if !conflict.Intentional {
			count++
		}
	}
	return count
}

func warningsForRoutes(ws *models.Workspace, app *models.Application, mode string, routes []Route) []RouteWarning {
	warnings := make([]RouteWarning, 0)
	if app != nil && mode == models.RouteModeLocal && app.Port == 0 {
		warnings = append(warnings, RouteWarning{Code: "LOCAL_TARGET_UNAVAILABLE", Message: fmt.Sprintf("application %q has no local port; no route is generated for it", app.CanonicalAppID())})
	}
	if app != nil && mode == models.RouteModeRemote && app.GetRemoteBaseUrl(ws) == "" {
		warnings = append(warnings, RouteWarning{Code: "REMOTE_TARGET_UNAVAILABLE", Message: fmt.Sprintf("application %q has no remote target", app.CanonicalAppID())})
	}
	if hasAccidentalConflicts(conflictsForRoutes(routes)) {
		warnings = append(warnings, RouteWarning{Code: "ROUTE_CONFLICT", Message: "one or more routes compete for the same host and pattern"})
	}
	return warnings
}

func planForWorkspace(ws *models.Workspace, input routePlanInput) (RoutePlan, error) {
	if ws == nil {
		return RoutePlan{}, fmt.Errorf("workspace not found: %s", input.WorkspaceID)
	}
	mode, err := normalizeMode(input.DesiredMode)
	if err != nil {
		return RoutePlan{}, err
	}
	_, app, err := findAppInWorkspace(ws, input.Application)
	if err != nil {
		return RoutePlan{}, err
	}
	before := buildNormalizedRoutes(ws)
	candidate := cloneWorkspace(ws)
	_, candidateApp, err := findAppInWorkspace(candidate, app.CanonicalAppID())
	if err != nil {
		return RoutePlan{}, err
	}
	if mode == "local" {
		candidateApp.Active = true
	} else {
		candidateApp.Active = false
	}
	if input.RoutePattern != nil {
		if strings.TrimSpace(*input.RoutePattern) == "" {
			candidateApp.RoutePattern = nil
		} else {
			pattern := normalizeRequestPath(*input.RoutePattern)
			candidateApp.RoutePattern = &pattern
		}
	}
	candidate.SetDefaults()
	after := buildNormalizedRoutes(candidate)
	plan := RoutePlan{WorkspaceID: ws.WorkspaceID, Environment: ws.WorkspaceID, BaseRouteVersion: ws.Route.RouteVersion, ApplicationID: app.CanonicalAppID(), DesiredMode: mode, RoutePattern: cloneString(input.RoutePattern), Before: before, After: after}
	plan.Changes = diffRoutes(before, after)
	plan.Conflicts = conflictsForRoutes(after)
	plan.Warnings = warningsForRoutes(ws, app, mode, after)
	plan.Fingerprint = fingerprintPlan(plan)
	return plan, nil
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func fingerprintPlan(plan RoutePlan) string {
	// The plan fingerprint includes the complete planned projection, making a
	// serialized plan tamper-evident. Idempotency uses the separate intent
	// fingerprint below so a server-side re-plan can still replay a request.
	copyPlan := struct {
		WorkspaceID   string
		Environment   string
		BaseVersion   uint64
		ApplicationID string
		DesiredMode   string
		RoutePattern  *string
		Before        []Route
		After         []Route
		Changes       []RouteChange
		Warnings      []RouteWarning
		Conflicts     []RouteConflict
	}{plan.WorkspaceID, plan.Environment, plan.BaseRouteVersion, plan.ApplicationID, plan.DesiredMode, plan.RoutePattern, plan.Before, plan.After, plan.Changes, plan.Warnings, plan.Conflicts}
	raw, _ := json.Marshal(copyPlan)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func routeIntentFingerprint(plan RoutePlan) string {
	intent := struct {
		WorkspaceID   string
		Environment   string
		ApplicationID string
		DesiredMode   string
		RoutePattern  *string
	}{plan.WorkspaceID, plan.Environment, plan.ApplicationID, plan.DesiredMode, plan.RoutePattern}
	raw, _ := json.Marshal(intent)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func fingerprintRoutes(routes []Route) string {
	copyRoutes := cloneRoutes(routes)
	sortRoutes(copyRoutes)
	raw, _ := json.Marshal(copyRoutes)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func (r *Registry) planRoute(input routePlanInput) (RoutePlan, error) {
	ws := r.FindWorkspace(input.WorkspaceID)
	if ws == nil {
		return RoutePlan{}, fmt.Errorf("workspace not found: %s", input.WorkspaceID)
	}
	return planForWorkspace(cloneWorkspace(ws), input)
}

func resolveRoutes(routes []Route, workspaceID, host, path string) (RouteResolution, error) {
	host = normalizeHost(host)
	path = normalizeRequestPath(path)
	candidates := make([]Route, 0)
	for _, route := range routes {
		if normalizeHost(route.PublicHost) == host && routeMatches(route, path) {
			candidates = append(candidates, route)
		}
	}
	if len(candidates) == 0 {
		return RouteResolution{}, fmt.Errorf("route not found for %s%s", host, path)
	}
	sortRoutes(candidates)
	winner := candidates[0]
	return RouteResolution{WorkspaceID: workspaceID, Environment: winner.Environment, Host: host, Path: path, Route: cloneRoute(&winner), MatchingPattern: winner.Pattern, CanonicalAppID: winner.CanonicalAppID, ServiceID: winner.ServiceID, Target: winner.Target, Precedence: winner.Precedence, PrecedenceReason: winner.PrecedenceReason}, nil
}
