package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/thiagojdb/rementor/internal/config"
	"github.com/thiagojdb/rementor/internal/models"
	"github.com/thiagojdb/rementor/internal/validation"
)

// Registry manages the in-memory runtime projection of durable workspace data.
// SQLite remains the source of durable truth for workspace definitions and
// route state; the registry adds runtime-only state such as health results,
// subscribers, streams, and the routing provider.
type Registry struct {
	workspaces        []*models.Workspace
	workspacesMu      sync.RWMutex
	mutationMu        sync.Mutex
	store             WorkspaceStore
	routingProvider   RoutingProvider
	stopChan          chan struct{}
	httpClient        *http.Client
	subscribers       map[string]int // workspaceID -> count of active subscribers
	subscribersMu     sync.RWMutex
	healthStreams     map[uint64]healthStream
	healthStreamsMu   sync.RWMutex
	nextHealthStream  uint64
	lastFullCheck     time.Time
	nextRouteVersion  uint64
	routingStateMu    sync.RWMutex
	effectiveRoutes   map[string][]Route
	effectiveHashes   map[string]string
	effectiveVersions map[string]uint64
	idempotencyMu     sync.Mutex
	routeIdempotency  map[string]routeApplyRecord
}

type routeApplyRecord struct {
	Fingerprint string
	Result      RouteApplyResult
}

// RouteApplyResult is returned by ApplyRoutePlan. It is kept in the service
// package so RPC, CLI, and MCP adapters all consume the same result shape.
type RouteApplyResult struct {
	Changed      bool                      `json:"changed"`
	Plan         RoutePlan                 `json:"plan"`
	Routes       []Route                   `json:"routes"`
	Verified     bool                      `json:"verified"`
	Verification string                    `json:"verificationStatus"`
	Status       string                    `json:"status,omitempty"`
	Degraded     bool                      `json:"degraded,omitempty"`
	Rollback     string                    `json:"rollbackStatus,omitempty"`
	Operation    *models.OperationMetadata `json:"operation,omitempty"`
}

// RouteTransactionError identifies which side of the proxy/durable-state
// transaction failed.  Degraded is true only when compensation itself failed;
// callers can then surface that the previous effective route is no longer
// guaranteed even though the desired state was not committed.
type RouteTransactionError struct {
	Operation *models.OperationMetadata
	Status    string
	Rollback  string
	Degraded  bool
	Cause     error
}

func (e *RouteTransactionError) Error() string {
	if e == nil || e.Cause == nil {
		return "route transaction failed"
	}
	if e.Degraded {
		return fmt.Sprintf("route transaction %s (rollback %s): %v", e.Status, e.Rollback, e.Cause)
	}
	if e.Rollback != "" {
		return fmt.Sprintf("route transaction %s (rollback %s): %v", e.Status, e.Rollback, e.Cause)
	}
	return fmt.Sprintf("route transaction %s: %v", e.Status, e.Cause)
}

func (e *RouteTransactionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

type healthStream struct {
	workspaceID string
	updates     chan models.HealthUpdate
}

// Global registry instance
var (
	globalRegistry *Registry
	registryOnce   sync.Once
)

// GetRegistry returns the global registry instance
func GetRegistry() *Registry {
	registryOnce.Do(func() {
		globalRegistry = &Registry{
			store:             NewConfigWorkspaceStore(),
			stopChan:          make(chan struct{}),
			httpClient:        &http.Client{Timeout: 5 * time.Second},
			subscribers:       make(map[string]int),
			healthStreams:     make(map[uint64]healthStream),
			lastFullCheck:     time.Time{},
			effectiveRoutes:   make(map[string][]Route),
			effectiveHashes:   make(map[string]string),
			effectiveVersions: make(map[string]uint64),
			routeIdempotency:  make(map[string]routeApplyRecord),
		}
	})
	return globalRegistry
}

func (r *Registry) workspaceStore() WorkspaceStore {
	if r.store == nil {
		r.store = NewConfigWorkspaceStore()
	}
	return r.store
}

// SetRoutingProvider sets the routing provider
func (r *Registry) SetRoutingProvider(provider RoutingProvider) {
	r.routingProvider = provider
}

// GetRoutingProvider returns the routing provider
func (r *Registry) GetRoutingProvider() RoutingProvider {
	return r.routingProvider
}

// Load loads the registry from configuration
func (r *Registry) Load() error {
	log.Println("Loading workspaces from config...")

	workspaces, err := r.workspaceStore().LoadWorkspaces()
	if err != nil {
		return fmt.Errorf("failed to load workspaces: %w", err)
	}

	r.workspacesMu.Lock()
	r.workspaces = workspaces
	r.workspacesMu.Unlock()
	for _, ws := range workspaces {
		if ws.Route.RouteVersion > r.nextRouteVersion {
			r.nextRouteVersion = ws.Route.RouteVersion
		}
		for _, app := range ws.Applications {
			if app.Route.RouteVersion > r.nextRouteVersion {
				r.nextRouteVersion = app.Route.RouteVersion
			}
		}
	}

	log.Printf("Loaded %d workspaces", len(workspaces))

	// Initialize runtime for all applications
	for _, ws := range r.workspaces {
		for _, app := range ws.Applications {
			app.InitializeRuntime()
		}
	}
	log.Println("Initialized application runtimes")

	// Load state
	if err := r.workspaceStore().LoadState(r.workspaces); err != nil {
		log.Printf("Warning: failed to load state: %v", err)
	}
	if err := r.restoreRouteJournal(); err != nil {
		// A journal is an enhancement over the legacy store.  Do not prevent a
		// daemon from serving its durable workspace state when an old or
		// partially migrated database cannot be decoded, but make the recovery
		// gap visible to operators.
		log.Printf("Warning: failed to load route operation journal: %v", err)
	}

	// Warm health checks (non-blocking)
	log.Println("Warming health checks...")
	go r.warmHealth()

	// Load initial config if routing provider is available
	if r.routingProvider != nil {
		log.Printf("Loading initial routing config for %d workspaces", len(r.workspaces))
		if err := r.reloadProxy(); err != nil {
			log.Printf("Warning: failed to load initial routing config: %v", err)
		} else if err := r.reconcileRouteJournal(); err != nil {
			log.Printf("Warning: failed to reconcile route operation journal: %v", err)
		}
	} else {
		log.Println("Routing provider not available, running without routing")
	}

	log.Println("Registry loaded successfully")

	return nil
}

// warmHealth performs initial health checks for active workspaces
func (r *Registry) warmHealth() {
	// Only warm health for workspaces with subscribers initially
	wssToCheck := r.GetActiveWorkspaces()

	var allApps int
	wsNames := make([]string, 0, len(wssToCheck))
	for _, ws := range wssToCheck {
		allApps += len(ws.Applications)
		wsNames = append(wsNames, ws.WorkspaceID)
	}

	// If no subscribers yet, we'll skip warming and let the periodic checker handle it
	if allApps == 0 {
		log.Println("[Health Warm] No active subscribers yet, skipping initial health warm")
		return
	}

	log.Printf("[Health Warm] Starting health check for workspaces: %v (total: %d applications)", wsNames, allApps)

	// Increased stagger to reduce network congestion: 1 second between checks
	staggerDelay := 1 * time.Second
	startTime := time.Now()

	for _, ws := range wssToCheck {
		log.Printf("[Health Warm] Processing workspace '%s' (%d applications)", ws.WorkspaceID, len(ws.Applications))
		for _, app := range ws.Applications {
			time.Sleep(staggerDelay)

			go func(w *models.Workspace, a *models.Application) {
				// local-apps have no remote concept — only check local health
				isLocalApps := w.IsLocalApps()
				remoteBase := ""
				if !isLocalApps {
					remoteBase = a.GetRemoteBaseUrl(w)
				}
				remoteURL := ""
				if remoteBase != "" {
					remoteURL = a.RemoteHealthURL(remoteBase)
				}
				log.Printf("[Health Warm] Checking application '%s/%s' (local: %s, remote: %s)",
					w.WorkspaceID, a.ID, a.HealthURL(), remoteURL)
				oldLocalOk := a.Runtime.GetHealthOk()
				oldRemoteOk := a.Runtime.GetRemoteOk()

				healthOk := CheckHealth(a.HealthURL())
				var remoteOk bool
				if remoteBase != "" {
					remoteOk = CheckHealth(a.RemoteHealthURL(remoteBase))
				}

				log.Printf("[Health Warm] Application '%s/%s' health status - Local: %v, Remote: %v",
					w.WorkspaceID, a.ID, healthOk, remoteOk)

				now := time.Now()
				a.Runtime.UpdateBothStatuses(healthOk, &now, remoteOk, &now)
				a.RefreshRouteState(w, &now)

				// Only send updates when there are changes
				if oldLocalOk != healthOk || oldRemoteOk != remoteOk {
					update := models.HealthUpdate{
						WsID:          w.WorkspaceID,
						AppName:       a.ID,
						LocalOk:       healthOk,
						RemoteOk:      remoteOk,
						LocalChecked:  now,
						RemoteChecked: now,
					}
					r.publishHealth(update)
				}
			}(ws, app)
		}
	}

	elapsed := time.Since(startTime)
	log.Printf("[Health Warm] Completed health check for workspaces: %v (total: %d applications, took: %v)",
		wsNames, allApps, elapsed)
}

// StartHealthChecker starts the periodic health checker
func (r *Registry) StartHealthChecker(ctx context.Context) {
	interval := time.Duration(config.Config.HealthCheckIntervalSecs) * time.Second

	log.Printf("Starting health checker with interval %v", interval)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				r.refreshAllHealth()
			case <-r.stopChan:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// refreshAllHealth refreshes health status for active workspaces
// Only checks workspaces with active subscribers, with periodic full checks
func (r *Registry) refreshAllHealth() {
	// Determine which workspaces to check
	var wssToCheck []*models.Workspace
	checkType := "active"

	activeWss := r.GetActiveWorkspaces()
	if len(activeWss) > 0 {
		// Someone is viewing - check only their workspaces
		wssToCheck = activeWss
		checkType = "active"
	} else if r.ShouldRunFullCheck() {
		// No one viewing - do full check every 5 minutes to keep data fresh
		r.workspacesMu.RLock()
		wssToCheck = make([]*models.Workspace, len(r.workspaces))
		copy(wssToCheck, r.workspaces)
		r.workspacesMu.RUnlock()
		checkType = "full (periodic)"
	} else {
		// No one viewing and not time for full check - skip entirely
		return
	}

	// Count applications to check and collect workspace names
	var totalApps int
	wsNames := make([]string, 0, len(wssToCheck))
	for _, ws := range wssToCheck {
		totalApps += len(ws.Applications)
		wsNames = append(wsNames, ws.WorkspaceID)
	}

	if totalApps == 0 {
		log.Printf("[Health Check] No applications to check (type: %s)", checkType)
		return
	}

	log.Printf("[Health Check] Starting %s health check loop for workspaces: %v (total: %d applications)", checkType, wsNames, totalApps)

	// Increased stagger to reduce network congestion: 500ms between checks
	staggerDelay := 500 * time.Millisecond
	startTime := time.Now()

	for _, ws := range wssToCheck {
		log.Printf("[Health Check] Processing workspace '%s' (%d applications)", ws.WorkspaceID, len(ws.Applications))
		for _, app := range ws.Applications {
			time.Sleep(staggerDelay)

			go func(w *models.Workspace, a *models.Application) {
				// local-apps have no remote concept — only check local health
				isLocalApps := w.IsLocalApps()
				remoteBase := ""
				if !isLocalApps {
					remoteBase = a.GetRemoteBaseUrl(w)
				}
				remoteURL := ""
				if remoteBase != "" {
					remoteURL = a.RemoteHealthURL(remoteBase)
				}
				log.Printf("[Health Check] Checking application '%s/%s' (local: %s, remote: %s)",
					w.WorkspaceID, a.ID, a.HealthURL(), remoteURL)
				oldLocalOk := a.Runtime.GetHealthOk()
				oldRemoteOk := a.Runtime.GetRemoteOk()

				newLocalOk := CheckHealth(a.HealthURL())
				var newRemoteOk bool
				if remoteBase != "" {
					newRemoteOk = CheckHealth(a.RemoteHealthURL(remoteBase))
				}

				log.Printf("[Health Check] Application '%s/%s' health status - Local: %v, Remote: %v",
					w.WorkspaceID, a.ID, newLocalOk, newRemoteOk)

				if oldLocalOk != newLocalOk || oldRemoteOk != newRemoteOk {
					update := models.HealthUpdate{
						WsID:          w.WorkspaceID,
						AppName:       a.ID,
						LocalOk:       newLocalOk,
						RemoteOk:      newRemoteOk,
						LocalChecked:  time.Now(),
						RemoteChecked: time.Now(),
					}
					r.publishHealth(update)
				}

				now := time.Now()
				a.Runtime.UpdateBothStatuses(newLocalOk, &now, newRemoteOk, &now)
				a.RefreshRouteState(w, &now)
			}(ws, app)
		}
	}

	elapsed := time.Since(startTime)
	log.Printf("[Health Check] Completed %s health check loop for workspaces: %v (total: %d applications, took: %v)",
		checkType, wsNames, totalApps, elapsed)
}

// GetWorkspaces returns a shallow copy of the runtime projection. Callers must
// treat returned workspaces as read-only.
func (r *Registry) GetWorkspaces() []*models.Workspace {
	r.workspacesMu.RLock()
	defer r.workspacesMu.RUnlock()

	// Return a copy to prevent external modification
	result := make([]*models.Workspace, len(r.workspaces))
	copy(result, r.workspaces)
	return result
}

// FindWorkspace finds a workspace by ID
func (r *Registry) FindWorkspace(wsID string) *models.Workspace {
	r.workspacesMu.RLock()
	defer r.workspacesMu.RUnlock()

	for _, ws := range r.workspaces {
		if ws.WorkspaceID == wsID {
			return ws
		}
	}
	return nil
}

// FindApp finds an application by workspace ID and canonical ID or alias.
func (r *Registry) FindApp(wsID, appName string) (*models.Workspace, *models.Application, error) {
	ws := r.FindWorkspace(wsID)
	if ws == nil {
		return nil, nil, fmt.Errorf("workspace not found: %s", wsID)
	}
	return findAppInWorkspace(ws, appName)
}

// GetRoutes returns the normalized desired routes for a workspace. The
// representation is shared by all control-plane surfaces and is independent
// of whether nginx is installed on the host.
func (r *Registry) GetRoutes(wsID string) ([]Route, uint64, []RouteWarning, []RouteConflict, error) {
	ws := r.FindWorkspace(wsID)
	if ws == nil {
		return nil, 0, nil, nil, fmt.Errorf("workspace not found: %s", wsID)
	}
	snapshot := cloneWorkspace(ws)
	routes := buildNormalizedRoutes(snapshot)
	conflicts := conflictsForRoutes(routes)
	warnings := make([]RouteWarning, 0, 1)
	if hasAccidentalConflicts(conflicts) {
		warnings = append(warnings, RouteWarning{Code: "ROUTE_CONFLICT", Message: "one or more routes compete for the same host and pattern"})
	}
	return routes, snapshot.Route.RouteVersion, warnings, conflicts, nil
}

// GetRouteConflicts returns the deterministic ownership/shadowing analysis
// for a workspace without requiring callers to inspect the full route list.
// It is intentionally derived from the same normalized projection used by
// GetRoutes, ResolveRoute, PlanRoute, and the nginx renderer.
func (r *Registry) GetRouteConflicts(wsID string) ([]RouteConflict, uint64, []RouteWarning, error) {
	ws := r.FindWorkspace(wsID)
	if ws == nil {
		return nil, 0, nil, fmt.Errorf("workspace not found: %s", wsID)
	}
	snapshot := cloneWorkspace(ws)
	conflicts := conflictsForRoutes(buildNormalizedRoutes(snapshot))
	warnings := make([]RouteWarning, 0, 1)
	if hasAccidentalConflicts(conflicts) {
		warnings = append(warnings, RouteWarning{Code: "ROUTE_CONFLICT", Message: "one or more routes compete for the same host and pattern"})
	}
	return conflicts, snapshot.Route.RouteVersion, warnings, nil
}

// ResolveRoute resolves a host/path request with nginx-compatible precedence.
func (r *Registry) ResolveRoute(wsID, host, path string) (RouteResolution, error) {
	ws := r.FindWorkspace(wsID)
	if ws == nil {
		return RouteResolution{}, fmt.Errorf("workspace not found: %s", wsID)
	}
	if strings.TrimSpace(host) == "" {
		if ws.IsLocalApps() {
			for _, app := range ws.Applications {
				if app.Domain != "" {
					host = app.Domain
					break
				}
			}
		} else {
			host = ws.GetLocalDomain()
		}
	}
	return resolveRoutes(buildNormalizedRoutes(cloneWorkspace(ws)), wsID, host, path)
}

// PlanRoute creates a deterministic, non-mutating route plan.
func (r *Registry) PlanRoute(wsID, application, desiredMode string, routePattern *string) (RoutePlan, error) {
	return r.planRoute(routePlanInput{WorkspaceID: wsID, Application: application, DesiredMode: desiredMode, RoutePattern: routePattern})
}

// ApplyRoutePlan applies a previously generated plan. It performs all checks
// while holding the same mutation lock used by legacy operations, so a plan
// cannot become stale between its version check and proxy reload.
func (r *Registry) ApplyRoutePlan(wsID string, plan RoutePlan, expectedVersion uint64, idempotencyKey, correlationID string) (RouteApplyResult, error) {
	if strings.TrimSpace(plan.WorkspaceID) == "" {
		plan.WorkspaceID = wsID
	}
	if strings.TrimSpace(plan.Environment) == "" {
		plan.Environment = wsID
	}
	if plan.WorkspaceID != wsID {
		return RouteApplyResult{}, fmt.Errorf("route plan workspace %q does not match request workspace %q", plan.WorkspaceID, wsID)
	}
	if strings.TrimSpace(plan.ApplicationID) == "" {
		return RouteApplyResult{}, fmt.Errorf("route plan application is required")
	}
	if strings.TrimSpace(plan.DesiredMode) == "" {
		return RouteApplyResult{}, fmt.Errorf("route plan desired mode is required")
	}
	mode, err := normalizeMode(plan.DesiredMode)
	if err != nil {
		return RouteApplyResult{}, err
	}
	plan.DesiredMode = mode
	idempotencyFingerprint := routeIntentFingerprint(plan)

	key := strings.TrimSpace(idempotencyKey)

	r.mutationMu.Lock()
	defer r.mutationMu.Unlock()
	// Idempotency lookup is deliberately inside the mutation critical section.
	// Looking up a key before taking this lock permits two concurrent callers
	// to both pass the check and reload nginx twice.
	if key != "" {
		r.ensureRouteState()
		r.idempotencyMu.Lock()
		record, found := r.routeIdempotency[wsID+"\x00"+key]
		r.idempotencyMu.Unlock()
		if found {
			if record.Fingerprint != idempotencyFingerprint {
				return RouteApplyResult{}, &RouteIdempotencyConflictError{Key: key}
			}
			result := record.Result
			result.Changed = false
			result.Status = "idempotent-replay"
			result.Verification = "unchanged"
			return result, nil
		}
	}

	previous := r.workspaceSnapshot()
	ws := findWorkspace(previous, wsID)
	if ws == nil {
		return RouteApplyResult{}, fmt.Errorf("workspace not found: %s", wsID)
	}
	currentVersion := ws.Route.RouteVersion
	if r.nextRouteVersion < currentVersion {
		r.nextRouteVersion = currentVersion
	}
	if expectedVersion == 0 {
		expectedVersion = plan.BaseRouteVersion
	}
	canonical, err := planForWorkspace(ws, routePlanInput{WorkspaceID: wsID, Application: plan.ApplicationID, DesiredMode: mode, RoutePattern: plan.RoutePattern})
	if err != nil {
		return RouteApplyResult{}, err
	}
	// A concrete plan (fingerprint or route snapshots) carries an expected
	// version even when that version is zero.  Only a direct, unplanned apply
	// with no expected value treats zero as "unspecified".
	hasExpectedVersion := expectedVersion != 0 || plan.BaseRouteVersion != 0 || plan.Fingerprint != "" || len(plan.Before) > 0 || len(plan.After) > 0
	versionConflict := hasExpectedVersion && (expectedVersion != currentVersion || (plan.BaseRouteVersion != 0 && plan.BaseRouteVersion != currentVersion))
	if versionConflict {
		// Compare-and-swap is strict. A request that was planned against an
		// older route version must fail before touching either the proxy or
		// durable state; retries that should replay an earlier result are handled
		// by the idempotency record above.
		expected := expectedVersion
		if plan.BaseRouteVersion != 0 {
			expected = plan.BaseRouteVersion
		}
		return RouteApplyResult{}, &RouteVersionConflictError{WorkspaceID: wsID, Expected: expected, Actual: currentVersion}
	}
	if plan.Fingerprint != "" && plan.Fingerprint != canonical.Fingerprint {
		return RouteApplyResult{}, fmt.Errorf("route plan fingerprint does not match current desired state")
	}
	if hasAccidentalConflicts(canonical.Conflicts) {
		return RouteApplyResult{}, fmt.Errorf("route plan contains %d accidental route conflict(s)", countAccidentalConflicts(canonical.Conflicts))
	}
	if len(canonical.Changes) == 0 {
		result := r.routeNoopResult(ws, canonical, correlationID)
		r.rememberRouteResult(wsID, key, idempotencyFingerprint, result)
		r.persistNoopRouteResult(wsID, key, idempotencyFingerprint, result)
		return result, nil
	}

	candidate := cloneWorkspaces(previous)
	candidateWS := findWorkspace(candidate, wsID)
	_, candidateApp, err := findAppInWorkspace(candidateWS, plan.ApplicationID)
	if err != nil {
		return RouteApplyResult{}, err
	}
	if mode == "local" {
		candidateApp.Active = true
	} else {
		candidateApp.Active = false
	}
	if plan.RoutePattern != nil {
		if strings.TrimSpace(*plan.RoutePattern) == "" {
			candidateApp.RoutePattern = nil
		} else {
			pattern := normalizeRequestPath(*plan.RoutePattern)
			candidateApp.RoutePattern = &pattern
		}
	}
	candidateWS.SetDefaults()
	operation := r.beginOperation("route-apply", correlationID)
	r.applyOperation(candidateWS, candidateApp, operation)
	if err := validateWorkspaces(candidate); err != nil {
		return RouteApplyResult{}, fmt.Errorf("validate route plan: %w", err)
	}

	journal := models.RouteOperationJournal{
		OperationID:     operation.OperationID,
		WorkspaceID:     wsID,
		IdempotencyKey:  key,
		Fingerprint:     idempotencyFingerprint,
		CorrelationID:   operation.CorrelationID,
		ExpectedVersion: currentVersion,
		RouteVersion:    operation.RouteVersion,
		Phase:           "prepared",
		Status:          "prepared",
		CreatedAt:       operation.CreatedAt,
		UpdatedAt:       time.Now().UTC(),
		PriorState:      cloneWorkspaces(previous),
		CandidateState:  cloneWorkspaces(candidate),
	}
	journalStore, journalEnabled := r.routeJournalStore()
	if journalEnabled {
		if err := journalStore.BeginRouteOperation(journal); err != nil {
			r.releaseOperationVersion(operation)
			return RouteApplyResult{}, &RouteTransactionError{Operation: operation, Status: "journal", Rollback: "not-required", Cause: err}
		}
	}

	if err := r.applyRouting(candidate, true); err != nil {
		rollbackStatus := r.rollbackAfterApplyFailure(previous)
		if journalEnabled {
			journal.Status = "failed"
			journal.Phase = "proxy-apply-failed"
			journal.Error = err.Error()
			journal.Degraded = strings.HasPrefix(rollbackStatus, "failed")
			journal.RollbackStatus = rollbackStatus
			journal.UpdatedAt = time.Now().UTC()
			_ = journalStore.UpdateRouteOperation(journal)
		}
		r.releaseOperationVersion(operation)
		return RouteApplyResult{}, &RouteTransactionError{Operation: operation, Status: "proxy-apply", Rollback: rollbackStatus, Degraded: strings.HasPrefix(rollbackStatus, "failed"), Cause: err}
	}
	if journalEnabled {
		journal.Status = "proxy-applied"
		journal.Phase = "proxy-applied"
		journal.UpdatedAt = time.Now().UTC()
		if err := journalStore.UpdateRouteOperation(journal); err != nil {
			rollbackStatus := r.rollbackRoute(previous)
			journal.Status = "rolled_back"
			journal.Phase = "journal-failed"
			journal.Error = err.Error()
			journal.Degraded = rollbackStatus != "succeeded"
			journal.RollbackStatus = rollbackStatus
			journal.UpdatedAt = time.Now().UTC()
			_ = journalStore.UpdateRouteOperation(journal)
			r.releaseOperationVersion(operation)
			return RouteApplyResult{}, &RouteTransactionError{Operation: operation, Status: "journal", Rollback: rollbackStatus, Degraded: rollbackStatus != "succeeded", Cause: err}
		}
	}

	// Persist the completed timestamp with the desired state.  The proxy has
	// already verified the candidate, so this is the durable half of the
	// transaction.
	operation.CompletedAt = time.Now().UTC()
	candidateWS.LastOperation.CompletedAt = operation.CompletedAt
	candidateWS.Route.OperationID = operation.OperationID
	candidateWS.Route.RouteVersion = operation.RouteVersion
	candidateApp.LastOperation.CompletedAt = operation.CompletedAt
	candidateApp.Route.OperationID = operation.OperationID
	candidateApp.Route.RouteVersion = operation.RouteVersion
	if err := r.workspaceStore().SaveState(candidate); err != nil {
		durableRollback := r.rollbackDurableState(previous)
		proxyRollback := r.rollbackRoute(previous)
		rollbackStatus := combineRollbackStatus(proxyRollback, durableRollback)
		if journalEnabled {
			journal.Status = "rolled_back"
			journal.Phase = "persistence-failed"
			journal.Error = err.Error()
			journal.Degraded = rollbackStatus != "succeeded"
			journal.RollbackStatus = rollbackStatus
			journal.UpdatedAt = time.Now().UTC()
			_ = journalStore.UpdateRouteOperation(journal)
		}
		r.releaseOperationVersion(operation)
		return RouteApplyResult{}, &RouteTransactionError{Operation: operation, Status: "persist", Rollback: rollbackStatus, Degraded: rollbackStatus != "succeeded", Cause: err}
	}
	r.workspacesMu.Lock()
	r.workspaces = candidate
	r.workspacesMu.Unlock()
	after := buildNormalizedRoutes(candidateWS)
	result := RouteApplyResult{Changed: true, Plan: canonical, Routes: after, Verified: r.routesAreEffective(wsID, after), Verification: "verified", Status: "committed", Rollback: "not-needed", Operation: cloneOperation(operation)}
	if journalEnabled {
		journal.Status = "committed"
		journal.Phase = "committed"
		journal.Result, _ = json.Marshal(result)
		journal.UpdatedAt = time.Now().UTC()
		if err := journalStore.UpdateRouteOperation(journal); err != nil {
			// Desired state and proxy are already aligned.  Keep the journal
			// pending so startup recovery can mark it committed, but surface
			// the degraded observability rather than claiming a durable history
			// write that did not happen.
			result.Status = "committed-with-journal-warning"
			result.Degraded = true
			log.Printf("Warning: route operation %s committed but journal update failed: %v", operation.OperationID, err)
		}
	}
	r.rememberRouteResult(wsID, key, idempotencyFingerprint, result)
	return result, nil
}

// SyncRoute reconciles the desired normalized routes with the last successful
// proxy projection. The provider abstraction intentionally remains optional;
// when nginx is unavailable the operation fails without claiming verification.
func (r *Registry) SyncRoute(wsID, correlationID string, repair bool) (RouteSyncResult, error) {
	r.mutationMu.Lock()
	defer r.mutationMu.Unlock()
	previous := r.workspaceSnapshot()
	ws := findWorkspace(previous, wsID)
	if ws == nil {
		return RouteSyncResult{}, fmt.Errorf("workspace not found: %s", wsID)
	}
	if r.nextRouteVersion < ws.Route.RouteVersion {
		r.nextRouteVersion = ws.Route.RouteVersion
	}
	desired := buildNormalizedRoutes(ws)
	desiredHash := fingerprintRoutes(desired)
	warnings := make([]RouteWarning, 0)
	conflicts := conflictsForRoutes(desired)
	if hasAccidentalConflicts(conflicts) {
		warnings = append(warnings, RouteWarning{Code: "ROUTE_CONFLICT", Message: "one or more routes compete for the same host and pattern"})
		if repair {
			return RouteSyncResult{}, fmt.Errorf("route sync blocked by %d accidental route conflict(s)", countAccidentalConflicts(conflicts))
		}
	}
	// In-memory effective state is only a cache. When a provider exposes the
	// optional inspector, compare the generated candidate with what the proxy
	// actually has loaded so an external edit/reload is reported as drift. A
	// missing or unavailable provider cannot produce a verified in-sync result.
	cacheInSync := r.routesAreEffectiveHash(wsID, desiredHash) && r.effectiveVersion(wsID) == ws.Route.RouteVersion
	proxyInSync := cacheInSync
	provider := r.GetRoutingProvider()
	if provider == nil || !provider.IsAvailable() {
		proxyInSync = false
		warnings = append(warnings, RouteWarning{Code: "ROUTE_PROVIDER_UNAVAILABLE", Message: "routing provider is unavailable; effective proxy state could not be verified"})
	} else if inspector, ok := provider.(RoutingInspector); ok {
		inspected, inspectErr := inspector.InspectRouting(previous)
		if inspectErr != nil {
			proxyInSync = false
			warnings = append(warnings, RouteWarning{Code: "ROUTE_INSPECTION_FAILED", Message: inspectErr.Error()})
		} else if !inspected {
			proxyInSync = false
			warnings = append(warnings, RouteWarning{Code: "ROUTE_PROXY_DRIFT", Message: "proxy configuration differs from the desired route projection"})
		} else {
			// The provider is authoritative when it can inspect the loaded
			// projection. Hydrate the in-memory cache without forcing a reload
			// merely because this registry instance has not recorded that state.
			proxyInSync = true
			if !cacheInSync {
				r.recordEffectiveRoutes(previous)
			}
		}
	}
	if proxyInSync {
		return RouteSyncResult{WorkspaceID: wsID, Changed: false, Verified: true, Status: "in-sync", DesiredRouteVersion: ws.Route.RouteVersion, EffectiveRouteVersion: r.effectiveVersion(wsID), Routes: desired, Warnings: warnings, Operation: r.routeNoopOperation(ws, correlationID, "route-sync")}, nil
	}
	if !repair {
		return RouteSyncResult{WorkspaceID: wsID, Changed: false, Verified: false, Status: "drift", DesiredRouteVersion: ws.Route.RouteVersion, EffectiveRouteVersion: r.effectiveVersion(wsID), Routes: desired, Warnings: warnings}, nil
	}
	candidate := cloneWorkspaces(previous)
	candidateWS := findWorkspace(candidate, wsID)
	candidateWS.SetDefaults()
	operation := r.beginOperation("route-sync", correlationID)
	r.applyOperation(candidateWS, nil, operation)
	for _, app := range candidateWS.Applications {
		app.LastOperation = cloneOperation(operation)
		app.Route.RouteVersion = operation.RouteVersion
		app.Route.OperationID = operation.OperationID
		app.RefreshRouteState(candidateWS, &operation.CompletedAt)
		app.Route.RouteVersion = operation.RouteVersion
		app.Route.OperationID = operation.OperationID
	}
	journal := models.RouteOperationJournal{
		OperationID:     operation.OperationID,
		WorkspaceID:     wsID,
		CorrelationID:   operation.CorrelationID,
		ExpectedVersion: ws.Route.RouteVersion,
		RouteVersion:    operation.RouteVersion,
		Phase:           "prepared",
		Status:          "prepared",
		CreatedAt:       operation.CreatedAt,
		UpdatedAt:       time.Now().UTC(),
		PriorState:      cloneWorkspaces(previous),
		CandidateState:  cloneWorkspaces(candidate),
	}
	journalStore, journalEnabled := r.routeJournalStore()
	if journalEnabled {
		if err := journalStore.BeginRouteOperation(journal); err != nil {
			r.releaseOperationVersion(operation)
			return RouteSyncResult{}, &RouteTransactionError{Operation: operation, Status: "journal", Rollback: "not-required", Cause: err}
		}
	}
	if err := r.applyRouting(candidate, true); err != nil {
		rollbackStatus := r.rollbackAfterApplyFailure(previous)
		if journalEnabled {
			journal.Status = "failed"
			journal.Phase = "proxy-apply-failed"
			journal.Error = err.Error()
			journal.Degraded = strings.HasPrefix(rollbackStatus, "failed")
			journal.RollbackStatus = rollbackStatus
			journal.UpdatedAt = time.Now().UTC()
			_ = journalStore.UpdateRouteOperation(journal)
		}
		r.releaseOperationVersion(operation)
		return RouteSyncResult{}, &RouteTransactionError{Operation: operation, Status: "proxy-apply", Rollback: rollbackStatus, Degraded: strings.HasPrefix(rollbackStatus, "failed"), Cause: err}
	}
	if journalEnabled {
		journal.Status = "proxy-applied"
		journal.Phase = "proxy-applied"
		journal.UpdatedAt = time.Now().UTC()
		if err := journalStore.UpdateRouteOperation(journal); err != nil {
			rollbackStatus := r.rollbackRoute(previous)
			journal.Status = "rolled_back"
			journal.Phase = "journal-failed"
			journal.Error = err.Error()
			journal.Degraded = rollbackStatus != "succeeded"
			journal.RollbackStatus = rollbackStatus
			journal.UpdatedAt = time.Now().UTC()
			_ = journalStore.UpdateRouteOperation(journal)
			r.releaseOperationVersion(operation)
			return RouteSyncResult{}, &RouteTransactionError{Operation: operation, Status: "journal", Rollback: rollbackStatus, Degraded: rollbackStatus != "succeeded", Cause: err}
		}
	}
	operation.CompletedAt = time.Now().UTC()
	candidateWS.LastOperation.CompletedAt = operation.CompletedAt
	for _, app := range candidateWS.Applications {
		if app.LastOperation != nil {
			app.LastOperation.CompletedAt = operation.CompletedAt
		}
	}
	if err := r.workspaceStore().SaveState(candidate); err != nil {
		durableRollback := r.rollbackDurableState(previous)
		proxyRollback := r.rollbackRoute(previous)
		rollbackStatus := combineRollbackStatus(proxyRollback, durableRollback)
		if journalEnabled {
			journal.Status = "rolled_back"
			journal.Phase = "persistence-failed"
			journal.Error = err.Error()
			journal.Degraded = rollbackStatus != "succeeded"
			journal.RollbackStatus = rollbackStatus
			journal.UpdatedAt = time.Now().UTC()
			_ = journalStore.UpdateRouteOperation(journal)
		}
		r.releaseOperationVersion(operation)
		return RouteSyncResult{}, &RouteTransactionError{Operation: operation, Status: "persist", Rollback: rollbackStatus, Degraded: rollbackStatus != "succeeded", Cause: err}
	}
	r.workspacesMu.Lock()
	r.workspaces = candidate
	r.workspacesMu.Unlock()
	result := RouteSyncResult{WorkspaceID: wsID, Changed: true, Verified: r.routesAreEffectiveHash(wsID, desiredHash), Status: "repaired", DesiredRouteVersion: operation.RouteVersion, EffectiveRouteVersion: operation.RouteVersion, Routes: buildNormalizedRoutes(candidateWS), Warnings: warnings, Rollback: "not-needed", Operation: cloneOperation(operation)}
	if journalEnabled {
		journal.Status = "committed"
		journal.Phase = "committed"
		journal.UpdatedAt = time.Now().UTC()
		if err := journalStore.UpdateRouteOperation(journal); err != nil {
			result.Status = "repaired-with-journal-warning"
			result.Degraded = true
			log.Printf("Warning: route sync %s committed but journal update failed: %v", operation.OperationID, err)
		}
	}
	return result, nil
}

// RegisterApplicationAlias adds an unambiguous normalized alias to an app
// identity and propagates it to every workspace binding for that identity.
func (r *Registry) RegisterApplicationAlias(wsID, appRef, alias string) (*models.Application, error) {
	result, _, err := r.RegisterApplicationAliasWithMetadata(wsID, appRef, alias, "")
	return result, err
}

// RegisterApplicationAliasWithMetadata is the contract-aware alias mutation.
func (r *Registry) RegisterApplicationAliasWithMetadata(wsID, appRef, alias, correlationID string) (*models.Application, *models.OperationMetadata, error) {
	rawAlias := alias
	alias = models.NormalizeIdentityToken(alias)
	if alias == "" {
		return nil, nil, fmt.Errorf("application alias is required")
	}
	if err := validation.IdentityIdentifier("application alias", rawAlias); err != nil {
		return nil, nil, err
	}
	var result *models.Application
	var operation *models.OperationMetadata
	_, err := r.mutate(false, func(candidate *[]*models.Workspace) error {
		_, app, err := findApp(*candidate, wsID, appRef)
		if err != nil {
			return err
		}
		canonical := app.CanonicalAppID()
		for _, workspace := range *candidate {
			for _, binding := range workspace.Applications {
				bindingCanonical := binding.CanonicalAppID()
				if models.NormalizeIdentityToken(bindingCanonical) == alias && bindingCanonical != canonical {
					return &models.AliasConflictError{Alias: alias, ExistingAppID: bindingCanonical, RequestedAppID: canonical}
				}
				for _, existing := range binding.NormalizedAliases() {
					if existing == alias && bindingCanonical != canonical {
						return &models.AliasConflictError{Alias: alias, ExistingAppID: bindingCanonical, RequestedAppID: canonical}
					}
				}
			}
		}
		operation = r.beginOperation("upsert", correlationID)
		for _, workspace := range *candidate {
			for _, binding := range workspace.Applications {
				if binding.CanonicalAppID() != canonical {
					continue
				}
				already := false
				for _, existing := range binding.NormalizedAliases() {
					if existing == alias {
						already = true
						break
					}
				}
				if !already {
					binding.Aliases = append(binding.Aliases, alias)
				}
				binding.LastOperation = cloneOperation(operation)
				binding.Route.RouteVersion = operation.RouteVersion
				binding.Route.OperationID = operation.OperationID
				binding.RefreshRouteState(workspace, &operation.CompletedAt)
				binding.Route.RouteVersion = operation.RouteVersion
				binding.Route.OperationID = operation.OperationID
				result = binding
			}
		}
		// Keep the operation visible even when the identity has no binding in
		// the requested workspace (the validation above still found one).
		if workspace := findWorkspace(*candidate, wsID); workspace != nil {
			workspace.LastOperation = cloneOperation(operation)
			workspace.Route.RouteVersion = operation.RouteVersion
			workspace.Route.OperationID = operation.OperationID
		}
		return nil
	}, r.workspaceStore().ReplaceWorkspaces)
	if err != nil {
		return nil, nil, err
	}
	return result, operation, nil
}

// SubscribeHealth creates an independent event stream so every client receives
// every health update for its selected workspace.
func (r *Registry) SubscribeHealth(workspaceID string) (uint64, <-chan models.HealthUpdate) {
	r.healthStreamsMu.Lock()
	defer r.healthStreamsMu.Unlock()
	r.nextHealthStream++
	id := r.nextHealthStream
	updates := make(chan models.HealthUpdate, 100)
	r.healthStreams[id] = healthStream{workspaceID: workspaceID, updates: updates}
	return id, updates
}

func (r *Registry) UnsubscribeHealth(id uint64) {
	r.healthStreamsMu.Lock()
	delete(r.healthStreams, id)
	r.healthStreamsMu.Unlock()
}

func (r *Registry) publishHealth(update models.HealthUpdate) {
	r.healthStreamsMu.RLock()
	defer r.healthStreamsMu.RUnlock()
	for _, stream := range r.healthStreams {
		if stream.workspaceID != "" && stream.workspaceID != update.WsID {
			continue
		}
		select {
		case stream.updates <- update:
		default:
			log.Printf("Warning: health stream full, dropping update for %s/%s", update.WsID, update.AppName)
		}
	}
}

// SaveState persists the durable route state from the runtime projection.
func (r *Registry) SaveState() error {
	return r.workspaceStore().SaveState(r.workspaceSnapshot())
}

// Stop stops the registry
func (r *Registry) Stop() {
	close(r.stopChan)
	r.SaveState()
	if r.routingProvider != nil {
		r.routingProvider.Close()
	}
}

// SubscribeWorkspace adds a subscriber for a workspace
func (r *Registry) SubscribeWorkspace(wsID string) {
	r.subscribersMu.Lock()
	defer r.subscribersMu.Unlock()
	r.subscribers[wsID]++
	log.Printf("Subscribed to workspace '%s' (total subscribers: %d)", wsID, r.subscribers[wsID])
}

// UnsubscribeWorkspace removes a subscriber for a workspace
func (r *Registry) UnsubscribeWorkspace(wsID string) {
	r.subscribersMu.Lock()
	defer r.subscribersMu.Unlock()
	if r.subscribers[wsID] > 0 {
		r.subscribers[wsID]--
		log.Printf("Unsubscribed from workspace '%s' (remaining subscribers: %d)", wsID, r.subscribers[wsID])
		if r.subscribers[wsID] == 0 {
			delete(r.subscribers, wsID)
		}
	}
}

// GetActiveWorkspaces returns workspaces that have active subscribers
// If no workspaces have subscribers, returns all workspaces (fallback for initial load)
func (r *Registry) GetActiveWorkspaces() []*models.Workspace {
	r.subscribersMu.RLock()
	subscribers := make(map[string]int)
	for k, v := range r.subscribers {
		subscribers[k] = v
	}
	r.subscribersMu.RUnlock()

	r.workspacesMu.RLock()
	defer r.workspacesMu.RUnlock()

	// If no one is subscribed to anything, return empty (health check will skip all)
	if len(subscribers) == 0 {
		return []*models.Workspace{}
	}

	// Return only workspaces with active subscribers
	var active []*models.Workspace
	for _, ws := range r.workspaces {
		if subscribers[ws.WorkspaceID] > 0 {
			active = append(active, ws)
		}
	}

	return active
}

// ShouldRunFullCheck determines if we should do a full health check of all workspaces
// This runs periodically even when no one is viewing, to keep data fresh
func (r *Registry) ShouldRunFullCheck() bool {
	const fullCheckInterval = 5 * time.Minute

	r.subscribersMu.Lock()
	defer r.subscribersMu.Unlock()
	hasSubscribers := len(r.subscribers) > 0

	// If someone is viewing, don't run full check (they get targeted checks)
	if hasSubscribers {
		return false
	}

	// Run full check every 5 minutes when no one is viewing
	if time.Since(r.lastFullCheck) > fullCheckInterval {
		r.lastFullCheck = time.Now()
		return true
	}

	return false
}

func (r *Registry) beginOperation(kind, correlationID string) *models.OperationMetadata {
	r.nextRouteVersion++
	now := time.Now().UTC()
	if correlationID == "" {
		correlationID = newOperationID("corr")
	}
	return &models.OperationMetadata{
		OperationID:   newOperationID("op"),
		CorrelationID: correlationID,
		RouteVersion:  r.nextRouteVersion,
		Kind:          kind,
		CreatedAt:     now,
		CompletedAt:   now,
	}
}

func newOperationID(prefix string) string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// crypto/rand failures are exceptionally rare; a timestamp still gives
		// callers a unique-enough identifier without making a successful route
		// mutation fail solely because metadata generation failed.
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(raw[:])
}

func cloneOperation(operation *models.OperationMetadata) *models.OperationMetadata {
	if operation == nil {
		return nil
	}
	clone := *operation
	return &clone
}

func (r *Registry) applyOperation(workspace *models.Workspace, app *models.Application, operation *models.OperationMetadata) {
	if workspace == nil || operation == nil {
		return
	}
	workspace.LastOperation = cloneOperation(operation)
	workspace.Route.RouteVersion = operation.RouteVersion
	workspace.Route.OperationID = operation.OperationID
	if app != nil {
		app.LastOperation = cloneOperation(operation)
		app.Route.RouteVersion = operation.RouteVersion
		app.Route.OperationID = operation.OperationID
		app.RefreshRouteState(workspace, &operation.CompletedAt)
		// RefreshRouteState derives the projection from legacy fields. Restore
		// operation metadata after that derivation so it cannot be lost.
		app.Route.RouteVersion = operation.RouteVersion
		app.Route.OperationID = operation.OperationID
	}
}

// ToggleApp toggles an application between local and remote. It retains the
// original return shape for legacy callers; new callers should use
// ToggleAppWithMetadata to receive the canonical operation contract.
func (r *Registry) ToggleApp(wsID, appName string) (*models.Application, error) {
	result, _, err := r.ToggleAppWithMetadata(wsID, appName, "")
	return result, err
}

// ToggleAppWithMetadata toggles an application and records a monotonic route
// version plus operation/correlation IDs on the workspace and application.
func (r *Registry) ToggleAppWithMetadata(wsID, appName, correlationID string) (*models.Application, *models.OperationMetadata, error) {
	var result *models.Application
	var workspace *models.Workspace
	var operation *models.OperationMetadata
	_, err := r.mutate(true, func(candidate *[]*models.Workspace) error {
		ws, app, err := findApp(*candidate, wsID, appName)
		if err != nil {
			return err
		}
		if !ws.IsLocalApps() {
			app.Active = !app.Active
		}
		operation = r.beginOperation("toggle", correlationID)
		r.applyOperation(ws, app, operation)
		result = app
		workspace = ws
		return nil
	}, r.workspaceStore().SaveState)
	if err != nil {
		return nil, nil, err
	}

	go r.checkApplicationHealth(result, workspace)
	return result, operation, nil
}

// ToggleAllToRemote toggles all applications to remote and retains the
// original return shape for legacy callers.
func (r *Registry) ToggleAllToRemote(wsID string) (*ToggleResult, error) {
	result, _, err := r.ToggleAllToRemoteWithMetadata(wsID, "")
	return result, err
}

func (r *Registry) ToggleAllToRemoteWithMetadata(wsID, correlationID string) (*ToggleResult, *models.OperationMetadata, error) {
	result := &ToggleResult{}
	var operation *models.OperationMetadata
	_, err := r.mutate(true, func(candidate *[]*models.Workspace) error {
		ws := findWorkspace(*candidate, wsID)
		if ws == nil {
			return fmt.Errorf("workspace not found: %s", wsID)
		}
		if !ws.IsLocalApps() {
			for _, app := range ws.Applications {
				if app.Active && app.HasLocal() {
					app.Active = false
					result.SuccessCount++
				}
			}
		}
		operation = r.beginOperation("toggle-all", correlationID)
		r.applyOperation(ws, nil, operation)
		for _, app := range ws.Applications {
			app.LastOperation = cloneOperation(operation)
			app.Route.RouteVersion = operation.RouteVersion
			app.Route.OperationID = operation.OperationID
			app.RefreshRouteState(ws, &operation.CompletedAt)
			app.Route.RouteVersion = operation.RouteVersion
			app.Route.OperationID = operation.OperationID
		}
		return nil
	}, r.workspaceStore().SaveState)
	if err != nil {
		return nil, nil, err
	}
	return result, operation, nil
}

// ToggleAllToLocal toggles all applications to local and retains the
// original return shape for legacy callers.
func (r *Registry) ToggleAllToLocal(wsID string) (*ToggleResult, error) {
	result, _, err := r.ToggleAllToLocalWithMetadata(wsID, "")
	return result, err
}

func (r *Registry) ToggleAllToLocalWithMetadata(wsID, correlationID string) (*ToggleResult, *models.OperationMetadata, error) {
	result := &ToggleResult{}
	var operation *models.OperationMetadata
	_, err := r.mutate(true, func(candidate *[]*models.Workspace) error {
		ws := findWorkspace(*candidate, wsID)
		if ws == nil {
			return fmt.Errorf("workspace not found: %s", wsID)
		}
		if !ws.IsLocalApps() {
			for _, app := range ws.Applications {
				if !app.Active && app.HasLocal() {
					app.Active = true
					result.SuccessCount++
				}
			}
		}
		operation = r.beginOperation("toggle-all", correlationID)
		r.applyOperation(ws, nil, operation)
		for _, app := range ws.Applications {
			app.LastOperation = cloneOperation(operation)
			app.Route.RouteVersion = operation.RouteVersion
			app.Route.OperationID = operation.OperationID
			app.RefreshRouteState(ws, &operation.CompletedAt)
			app.Route.RouteVersion = operation.RouteVersion
			app.Route.OperationID = operation.OperationID
		}
		return nil
	}, r.workspaceStore().SaveState)
	if err != nil {
		return nil, nil, err
	}
	return result, operation, nil
}

// SyncRouting reloads the full routing config from the current in-memory state
// for legacy callers.
func (r *Registry) SyncRouting() error {
	_, err := r.SyncRoutingWithMetadata("")
	return err
}

// SyncRoutingWithMetadata records a route operation even though synchronization
// does not change application configuration. The operation is published on
// the workspace projection so subsequent reads can correlate the sync.
func (r *Registry) SyncRoutingWithMetadata(correlationID string) (*models.OperationMetadata, error) {
	var operation *models.OperationMetadata
	_, err := r.mutate(true, func(candidate *[]*models.Workspace) error {
		operation = r.beginOperation("sync", correlationID)
		for _, ws := range *candidate {
			r.applyOperation(ws, nil, operation)
			for _, app := range ws.Applications {
				app.LastOperation = cloneOperation(operation)
				app.Route.RouteVersion = operation.RouteVersion
				app.Route.OperationID = operation.OperationID
				app.RefreshRouteState(ws, &operation.CompletedAt)
				app.Route.RouteVersion = operation.RouteVersion
				app.Route.OperationID = operation.OperationID
			}
		}
		return nil
	}, r.workspaceStore().SaveState)
	if err != nil {
		return nil, err
	}
	return operation, nil
}

// UpdateWorkspaceApplications applies a complete candidate workspace before persisting it.
func (r *Registry) UpdateWorkspaceApplications(wsID string, apps []models.ApplicationConfig, localDomain, defaultRemoteBaseURL string) error {
	_, err := r.UpdateWorkspaceApplicationsWithMetadata(wsID, apps, localDomain, defaultRemoteBaseURL, "")
	return err
}

// UpdateWorkspaceApplicationsWithMetadata applies a complete candidate
// workspace and records the operation that changed its route projection.
func (r *Registry) UpdateWorkspaceApplicationsWithMetadata(wsID string, apps []models.ApplicationConfig, localDomain, defaultRemoteBaseURL, correlationID string) (*models.OperationMetadata, error) {
	var operation *models.OperationMetadata
	_, err := r.mutate(true, func(candidate *[]*models.Workspace) error {
		workspace := findWorkspace(*candidate, wsID)
		if workspace == nil {
			return fmt.Errorf("workspace not found: %s", wsID)
		}
		workspace.Applications = applicationsFromConfigs(workspace, apps)
		if !workspace.IsLocalApps() {
			if workspace.RoutingConfig == nil {
				workspace.RoutingConfig = &models.RoutingConfig{}
			}
			workspace.RoutingConfig.LocalDomain = localDomain
			workspace.RoutingConfig.DefaultRemoteBaseURL = defaultRemoteBaseURL
		}
		workspace.SetDefaults()
		for _, app := range workspace.Applications {
			app.InitializeRuntime()
		}
		operation = r.beginOperation("upsert", correlationID)
		r.applyOperation(workspace, nil, operation)
		for _, app := range workspace.Applications {
			app.LastOperation = cloneOperation(operation)
			app.Route.RouteVersion = operation.RouteVersion
			app.Route.OperationID = operation.OperationID
			app.RefreshRouteState(workspace, &operation.CompletedAt)
			app.Route.RouteVersion = operation.RouteVersion
			app.Route.OperationID = operation.OperationID
		}
		return nil
	}, r.workspaceStore().ReplaceWorkspaces)
	return operation, err
}

// CreateWorkspace persists a workspace and adds it to the runtime projection.
func (r *Registry) CreateWorkspace(wsConfig models.WorkspaceConfig) (*models.Workspace, error) {
	workspace, _, err := r.CreateWorkspaceWithMetadata(wsConfig, "")
	return workspace, err
}

func (r *Registry) CreateWorkspaceWithMetadata(wsConfig models.WorkspaceConfig, correlationID string) (*models.Workspace, *models.OperationMetadata, error) {
	ws := r.workspaceStore().WorkspaceFromConfig(wsConfig)
	if ws == nil {
		return nil, nil, fmt.Errorf("initialize workspace")
	}
	var result *models.Workspace
	var operation *models.OperationMetadata
	_, err := r.mutate(true, func(candidate *[]*models.Workspace) error {
		if findWorkspace(*candidate, wsConfig.ID) != nil {
			return fmt.Errorf("workspace already exists: %s", wsConfig.ID)
		}
		result = cloneWorkspace(ws)
		operation = r.beginOperation("upsert", correlationID)
		r.applyOperation(result, nil, operation)
		for _, app := range result.Applications {
			app.LastOperation = cloneOperation(operation)
			app.Route.RouteVersion = operation.RouteVersion
			app.Route.OperationID = operation.OperationID
			app.RefreshRouteState(result, &operation.CompletedAt)
			app.Route.RouteVersion = operation.RouteVersion
			app.Route.OperationID = operation.OperationID
		}
		*candidate = append(*candidate, result)
		return nil
	}, r.workspaceStore().ReplaceWorkspaces)
	if err != nil {
		return nil, nil, err
	}
	return result, operation, nil
}

// AddWorkspace adds a workspace to the runtime projection and durable store.
func (r *Registry) AddWorkspace(workspace *models.Workspace) error {
	if workspace == nil {
		return fmt.Errorf("workspace is required")
	}
	_, err := r.mutate(true, func(candidate *[]*models.Workspace) error {
		if findWorkspace(*candidate, workspace.WorkspaceID) != nil {
			return fmt.Errorf("workspace already exists: %s", workspace.WorkspaceID)
		}
		*candidate = append(*candidate, cloneWorkspace(workspace))
		return nil
	}, r.workspaceStore().ReplaceWorkspaces)
	return err
}

// DeleteWorkspace removes a workspace from the registry and config
func (r *Registry) DeleteWorkspace(wsID string) error {
	_, err := r.DeleteWorkspaceWithMetadata(wsID, "")
	return err
}

func (r *Registry) DeleteWorkspaceWithMetadata(wsID, correlationID string) (*models.OperationMetadata, error) {
	var operation *models.OperationMetadata
	_, err := r.mutate(true, func(candidate *[]*models.Workspace) error {
		for i, workspace := range *candidate {
			if workspace.WorkspaceID == wsID {
				operation = r.beginOperation("delete", correlationID)
				*candidate = append((*candidate)[:i], (*candidate)[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("workspace not found: %s", wsID)
	}, r.workspaceStore().ReplaceWorkspaces)
	if err != nil {
		return nil, err
	}
	r.subscribersMu.Lock()
	delete(r.subscribers, wsID)
	r.subscribersMu.Unlock()
	return operation, nil
}

// UpdateRoutePattern updates the route pattern for an application. The
// metadata-free wrapper preserves the original API for existing callers.
func (r *Registry) UpdateRoutePattern(wsID, appName string, pattern *string) (*models.Application, error) {
	result, _, err := r.UpdateRoutePatternWithMetadata(wsID, appName, pattern, "")
	return result, err
}

func (r *Registry) UpdateRoutePatternWithMetadata(wsID, appName string, pattern *string, correlationID string) (*models.Application, *models.OperationMetadata, error) {
	var result *models.Application
	var operation *models.OperationMetadata
	_, err := r.mutate(true, func(candidate *[]*models.Workspace) error {
		ws, app, err := findApp(*candidate, wsID, appName)
		if err != nil {
			return err
		}
		app.RoutePattern = cloneStringPtr(pattern)
		operation = r.beginOperation("update-pattern", correlationID)
		r.applyOperation(ws, app, operation)
		result = app
		return nil
	}, r.workspaceStore().SaveState)
	if err != nil {
		return nil, nil, err
	}
	return result, operation, nil
}

// mutate applies a change to a detached candidate snapshot. Routing is updated
// before persistence; a persistence failure triggers a compensating proxy reload
// from the previous snapshot. The runtime projection changes only after both
// external steps succeed.
func (r *Registry) mutate(requireRouting bool, change func(*[]*models.Workspace) error, persist func([]*models.Workspace) error) ([]*models.Workspace, error) {
	r.mutationMu.Lock()
	defer r.mutationMu.Unlock()

	previous := r.workspaceSnapshot()
	candidate := cloneWorkspaces(previous)
	if err := change(&candidate); err != nil {
		return nil, err
	}
	if err := validateWorkspaces(candidate); err != nil {
		return nil, fmt.Errorf("validate workspace state: %w", err)
	}
	if err := r.applyRouting(candidate, requireRouting); err != nil {
		provider := r.GetRoutingProvider()
		if provider != nil && provider.IsAvailable() {
			if rollbackErr := r.applyRouting(previous, true); rollbackErr != nil {
				return nil, fmt.Errorf("apply routing: %w; restore routing: %v", err, rollbackErr)
			}
		}
		return nil, fmt.Errorf("apply routing: %w", err)
	}
	if err := persist(candidate); err != nil {
		if rollbackErr := r.applyRouting(previous, requireRouting); rollbackErr != nil {
			return nil, fmt.Errorf("persist workspace state: %w; restore routing: %v", err, rollbackErr)
		}
		return nil, fmt.Errorf("persist workspace state: %w", err)
	}

	r.workspacesMu.Lock()
	r.workspaces = candidate
	r.workspacesMu.Unlock()
	return candidate, nil
}

func (r *Registry) workspaceSnapshot() []*models.Workspace {
	r.workspacesMu.RLock()
	defer r.workspacesMu.RUnlock()
	return cloneWorkspaces(r.workspaces)
}

func (r *Registry) checkApplicationHealth(app *models.Application, workspace *models.Workspace) {
	if app == nil || workspace == nil {
		return
	}
	localOK := CheckHealth(app.HealthURL())
	remoteOK := false
	if remoteBase := app.GetRemoteBaseUrl(workspace); remoteBase != "" {
		remoteOK = CheckHealth(app.RemoteHealthURL(remoteBase))
	}
	now := time.Now()
	app.Runtime.UpdateBothStatuses(localOK, &now, remoteOK, &now)
	app.RefreshRouteState(workspace, &now)
}

func findWorkspace(workspaces []*models.Workspace, wsID string) *models.Workspace {
	for _, workspace := range workspaces {
		if workspace.WorkspaceID == wsID {
			return workspace
		}
	}
	return nil
}

func validateWorkspaces(workspaces []*models.Workspace) error {
	seen := make(map[string]struct{}, len(workspaces))
	for _, workspace := range workspaces {
		if _, exists := seen[workspace.WorkspaceID]; exists {
			return fmt.Errorf("workspace %q is duplicated", workspace.WorkspaceID)
		}
		seen[workspace.WorkspaceID] = struct{}{}
		if err := validation.Identifier("workspace ID", workspace.WorkspaceID); err != nil {
			return err
		}
		if err := validation.Workspace(
			workspace.GetType(), workspace.GetLocalDomain(), workspace.GetDefaultRemoteBaseURL(), applicationConfigs(workspace.Applications),
		); err != nil {
			return fmt.Errorf("workspace %q: %w", workspace.WorkspaceID, err)
		}
	}
	return nil
}

func findApp(workspaces []*models.Workspace, wsID, appID string) (*models.Workspace, *models.Application, error) {
	workspace := findWorkspace(workspaces, wsID)
	if workspace == nil {
		return nil, nil, fmt.Errorf("workspace not found: %s", wsID)
	}
	return findAppInWorkspace(workspace, appID)
}

func findAppInWorkspace(workspace *models.Workspace, reference string) (*models.Workspace, *models.Application, error) {
	normalized := models.NormalizeIdentityToken(reference)
	var matches []*models.Application
	for _, app := range workspace.Applications {
		if models.NormalizeIdentityToken(app.CanonicalAppID()) == normalized {
			matches = append(matches, app)
			continue
		}
		// A legacy binding may retain an ID that differs from its canonical
		// app_id. Treat that wire-compatible identifier as a lookup alias while
		// keeping the canonical identity as the returned key.
		if models.NormalizeIdentityToken(app.ID) == normalized {
			matches = append(matches, app)
			continue
		}
		for _, alias := range app.NormalizedAliases() {
			if alias == normalized {
				matches = append(matches, app)
				break
			}
		}
	}
	if len(matches) == 1 {
		return workspace, matches[0], nil
	}
	if len(matches) > 1 {
		ids := make([]string, 0, len(matches))
		for _, app := range matches {
			ids = append(ids, app.CanonicalAppID())
		}
		return nil, nil, &models.AmbiguousApplicationError{Reference: reference, Matches: ids}
	}
	return nil, nil, fmt.Errorf("application not found: %s/%s", workspace.WorkspaceID, reference)
}

func applicationsFromConfigs(workspace *models.Workspace, configs []models.ApplicationConfig) []*models.Application {
	previous := make(map[string]*models.Application, len(workspace.Applications))
	for _, app := range workspace.Applications {
		previous[app.ID] = app
	}
	applications := make([]*models.Application, 0, len(configs))
	for _, config := range configs {
		if old := previous[config.CanonicalAppID()]; old != nil {
			if config.AppID == "" {
				config.AppID = old.CanonicalAppID()
			}
			if config.ServiceID == "" {
				config.ServiceID = old.ServiceID
			}
			if config.Repository == "" {
				config.Repository = old.Repository
			}
			if len(config.Aliases) == 0 {
				config.Aliases = old.NormalizedAliases()
			}
		}
		canonical := models.NormalizeIdentityToken(config.CanonicalAppID())
		serviceID := models.NormalizeIdentityToken(config.ServiceID)
		if serviceID == "" {
			serviceID = canonical
		}
		app := &models.Application{
			ID: canonical, AppID: canonical, ServiceID: serviceID, Repository: config.Repository, Aliases: append([]string(nil), config.Aliases...), Name: config.Name, Path: config.Path, Domain: config.Domain,
			RemoteBaseUrl: config.RemoteBaseUrl, Context: config.Context, Health: config.Health,
			Port: config.Port, Active: config.Active, RoutePattern: cloneStringPtr(config.RoutePattern),
			RouteOverride: config.RouteOverride, StripOrigin: config.StripOrigin,
		}
		if app.Health == "" {
			app.Health = models.DefaultHealthEndpoint
		}
		if old := previous[app.ID]; old != nil {
			app.Active = old.Active
			app.StripOrigin = old.StripOrigin
			if app.RoutePattern == nil {
				app.RoutePattern = cloneStringPtr(old.RoutePattern)
			}
			copyRuntime(old, app)
			app.Route = old.Route
			app.LastOperation = cloneOperation(old.LastOperation)
		}
		applications = append(applications, app)
	}
	return applications
}

func cloneWorkspaces(workspaces []*models.Workspace) []*models.Workspace {
	cloned := make([]*models.Workspace, 0, len(workspaces))
	for _, workspace := range workspaces {
		cloned = append(cloned, cloneWorkspace(workspace))
	}
	return cloned
}

func cloneWorkspace(source *models.Workspace) *models.Workspace {
	clone := &models.Workspace{
		WorkspaceID:   source.WorkspaceID,
		Type:          source.Type,
		Name:          cloneStringPtr(source.Name),
		Color:         cloneStringPtr(source.Color),
		Applications:  make([]*models.Application, 0, len(source.Applications)),
		Route:         source.Route,
		LastOperation: cloneOperation(source.LastOperation),
	}
	if source.RoutingConfig != nil {
		routing := *source.RoutingConfig
		clone.RoutingConfig = &routing
	}
	for _, sourceApp := range source.Applications {
		app := &models.Application{
			ID: sourceApp.ID, AppID: sourceApp.CanonicalAppID(), ServiceID: sourceApp.ServiceID, Repository: sourceApp.Repository, Aliases: append([]string(nil), sourceApp.Aliases...), Name: sourceApp.Name, Path: sourceApp.Path, Domain: sourceApp.Domain,
			RemoteBaseUrl: sourceApp.RemoteBaseUrl, Context: sourceApp.Context, Health: sourceApp.Health,
			Port: sourceApp.Port, Active: sourceApp.Active, RoutePattern: cloneStringPtr(sourceApp.RoutePattern),
			RouteOverride: sourceApp.RouteOverride, StripOrigin: sourceApp.StripOrigin,
			Route:         sourceApp.Route,
			LastOperation: cloneOperation(sourceApp.LastOperation),
		}
		copyRuntime(sourceApp, app)
		clone.Applications = append(clone.Applications, app)
	}
	clone.SetDefaults()
	for _, app := range clone.Applications {
		app.InitializeRuntime()
	}
	return clone
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func copyRuntime(source, target *models.Application) {
	localLast := source.Runtime.GetHealthLast()
	remoteLast := source.Runtime.GetRemoteLast()
	target.Runtime.UpdateBothStatuses(
		source.Runtime.GetHealthOk(), cloneTimePtr(localLast),
		source.Runtime.GetRemoteOk(), cloneTimePtr(remoteLast),
	)
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func applicationConfigs(apps []*models.Application) []models.ApplicationConfig {
	result := make([]models.ApplicationConfig, 0, len(apps))
	for _, app := range apps {
		result = append(result, models.ApplicationConfig{
			ID: app.ID, AppID: app.CanonicalAppID(), ServiceID: app.ServiceID, Repository: app.Repository, Aliases: app.NormalizedAliases(), Name: app.Name, Path: app.Path, Domain: app.Domain,
			RemoteBaseUrl: app.RemoteBaseUrl, Port: app.Port, Health: app.Health,
			Active: app.Active, RoutePattern: app.RoutePattern, Context: app.Context,
			RouteOverride: app.RouteOverride, StripOrigin: app.StripOrigin,
		})
	}
	return result
}

// reloadProxy generates the current desired config and pushes it to Proxy.
func (r *Registry) reloadProxy() error {
	return r.applyRouting(r.workspaceSnapshot(), true)
}

func (r *Registry) reloadProxyIfAvailable() error {
	return r.applyRouting(r.workspaceSnapshot(), false)
}

func (r *Registry) ensureRouteState() {
	r.routingStateMu.Lock()
	if r.effectiveRoutes == nil {
		r.effectiveRoutes = make(map[string][]Route)
	}
	if r.effectiveHashes == nil {
		r.effectiveHashes = make(map[string]string)
	}
	if r.effectiveVersions == nil {
		r.effectiveVersions = make(map[string]uint64)
	}
	r.routingStateMu.Unlock()
	r.idempotencyMu.Lock()
	if r.routeIdempotency == nil {
		r.routeIdempotency = make(map[string]routeApplyRecord)
	}
	r.idempotencyMu.Unlock()
}

func (r *Registry) routeJournalStore() (RouteJournalStore, bool) {
	store := r.workspaceStore()
	journal, ok := store.(RouteJournalStore)
	return journal, ok
}

func (r *Registry) rememberRouteResult(wsID, key, fingerprint string, result RouteApplyResult) {
	if strings.TrimSpace(key) == "" {
		return
	}
	r.ensureRouteState()
	r.idempotencyMu.Lock()
	r.routeIdempotency[wsID+"\x00"+key] = routeApplyRecord{Fingerprint: fingerprint, Result: result}
	r.idempotencyMu.Unlock()
}

func (r *Registry) persistNoopRouteResult(wsID, key, fingerprint string, result RouteApplyResult) {
	if strings.TrimSpace(key) == "" || result.Operation == nil {
		return
	}
	journal, ok := r.routeJournalStore()
	if !ok {
		return
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		log.Printf("Warning: failed to encode idempotent route result: %v", err)
		return
	}
	now := time.Now().UTC()
	entry := models.RouteOperationJournal{
		// No-op responses get a synthetic operation ID without consuming a route
		// version, so the durable history can use the same identity returned to
		// the caller.
		OperationID:     result.Operation.OperationID,
		WorkspaceID:     wsID,
		IdempotencyKey:  key,
		Fingerprint:     fingerprint,
		CorrelationID:   result.Operation.CorrelationID,
		ExpectedVersion: result.Operation.RouteVersion,
		RouteVersion:    result.Operation.RouteVersion,
		Phase:           "committed",
		Status:          "committed",
		CreatedAt:       result.Operation.CreatedAt,
		UpdatedAt:       now,
		Result:          encoded,
	}
	if err := journal.BeginRouteOperation(entry); err != nil {
		log.Printf("Warning: failed to persist idempotent route result: %v", err)
	}
}

func (r *Registry) routeNoopResult(ws *models.Workspace, plan RoutePlan, correlationID string) RouteApplyResult {
	operation := r.routeNoopOperation(ws, correlationID, "route-apply")
	return RouteApplyResult{
		Changed:      false,
		Plan:         plan,
		Routes:       cloneRoutes(plan.After),
		Verified:     r.routesAreEffective(ws.WorkspaceID, plan.After),
		Verification: "unchanged",
		Status:       "unchanged",
		Rollback:     "not-needed",
		Operation:    operation,
	}
}

func (r *Registry) routeNoopOperation(ws *models.Workspace, correlationID, kind string) *models.OperationMetadata {
	now := time.Now().UTC()
	if strings.TrimSpace(correlationID) == "" {
		correlationID = newOperationID("corr")
	}
	return &models.OperationMetadata{
		OperationID:   newOperationID("op-noop"),
		CorrelationID: correlationID,
		RouteVersion:  ws.Route.RouteVersion,
		Kind:          kind,
		CreatedAt:     now,
		CompletedAt:   now,
	}
}

func (r *Registry) rollbackRoute(previous []*models.Workspace) string {
	if err := r.applyRouting(previous, true); err != nil {
		return "failed: " + err.Error()
	}
	return "succeeded"
}

func (r *Registry) rollbackDurableState(previous []*models.Workspace) string {
	if err := r.workspaceStore().SaveState(previous); err != nil {
		return "failed: " + err.Error()
	}
	return "succeeded"
}

func combineRollbackStatus(proxy, durable string) string {
	if proxy == "succeeded" && durable == "succeeded" {
		return "succeeded"
	}
	return fmt.Sprintf("proxy=%s; durable=%s", proxy, durable)
}

func (r *Registry) rollbackAfterApplyFailure(previous []*models.Workspace) string {
	provider := r.GetRoutingProvider()
	if provider == nil || !provider.IsAvailable() {
		// No proxy was available to mutate, so there is no compensation to
		// perform and the previous effective route remains the provider's last
		// known state.
		return "not-required"
	}
	return r.rollbackRoute(previous)
}

func (r *Registry) releaseOperationVersion(operation *models.OperationMetadata) {
	if operation == nil || r.nextRouteVersion != operation.RouteVersion {
		return
	}
	if operation.RouteVersion > 0 {
		r.nextRouteVersion = operation.RouteVersion - 1
	}
}

// restoreRouteJournal hydrates durable idempotency results before requests can
// arrive.  Pending entries are intentionally retained until the initial proxy
// load has completed; reconcileRouteJournal then resolves them against the
// durable desired state.
func (r *Registry) restoreRouteJournal() error {
	journal, ok := r.routeJournalStore()
	if !ok {
		return nil
	}
	operations, err := journal.LoadRouteOperations()
	if err != nil {
		return err
	}
	r.ensureRouteState()
	for _, operation := range operations {
		// Reserve every journaled version, including failed/pending attempts. A
		// recovered pending operation may become a commit during reconciliation,
		// and reusing its version after restart would break monotonicity.
		if operation.RouteVersion > r.nextRouteVersion {
			r.nextRouteVersion = operation.RouteVersion
		}
		if operation.Status != "committed" || operation.IdempotencyKey == "" {
			continue
		}
		result := routeResultFromJournal(operation)
		fingerprint := journalIntentFingerprint(operation, result)
		r.idempotencyMu.Lock()
		r.routeIdempotency[operation.WorkspaceID+"\x00"+operation.IdempotencyKey] = routeApplyRecord{Fingerprint: fingerprint, Result: result}
		r.idempotencyMu.Unlock()
	}
	return nil
}

// reconcileRouteJournal closes operations left between proxy application and
// the durable state commit.  The initial proxy load has just made the loaded
// durable workspace state authoritative, so a journal is either committed
// when its version is present or rolled back when the previous version is
// still present.  This is deliberately idempotent and safe to run on every
// daemon start.
func (r *Registry) reconcileRouteJournal() error {
	journal, ok := r.routeJournalStore()
	if !ok {
		return nil
	}
	operations, err := journal.LoadRouteOperations()
	if err != nil {
		return err
	}
	workspaces := r.workspaceSnapshot()
	for _, operation := range operations {
		if operation.Status == "committed" || operation.Status == "rolled_back" || operation.Status == "failed" || operation.Status == "degraded" {
			continue
		}
		current := findWorkspace(workspaces, operation.WorkspaceID)
		matchesCandidate := current != nil && current.Route.RouteVersion == operation.RouteVersion
		operation.UpdatedAt = time.Now().UTC()
		if matchesCandidate {
			operation.Status = "committed"
			operation.Phase = "recovered-commit"
			operation.RollbackStatus = "not-needed"
			if operation.IdempotencyKey != "" {
				result := routeResultFromJournal(operation)
				fingerprint := journalIntentFingerprint(operation, result)
				r.ensureRouteState()
				r.idempotencyMu.Lock()
				r.routeIdempotency[operation.WorkspaceID+"\x00"+operation.IdempotencyKey] = routeApplyRecord{Fingerprint: fingerprint, Result: result}
				r.idempotencyMu.Unlock()
			}
		} else {
			operation.Status = "rolled_back"
			operation.Phase = "recovered-rollback"
			operation.RollbackStatus = "recovered"
		}
		if err := journal.UpdateRouteOperation(operation); err != nil {
			return err
		}
	}
	return nil
}

func routeResultFromJournal(operation models.RouteOperationJournal) RouteApplyResult {
	var result RouteApplyResult
	recovered := true
	if len(operation.Result) > 0 {
		if err := json.Unmarshal(operation.Result, &result); err != nil {
			log.Printf("Warning: ignoring malformed route result for %s: %v", operation.OperationID, err)
		} else {
			recovered = false
		}
	}
	if result.Operation == nil {
		result.Operation = &models.OperationMetadata{
			OperationID:   operation.OperationID,
			CorrelationID: operation.CorrelationID,
			RouteVersion:  operation.RouteVersion,
			Kind:          "route-apply",
			CreatedAt:     operation.CreatedAt,
			CompletedAt:   operation.UpdatedAt,
		}
	}
	if result.Status == "" {
		result.Status = "recovered"
	}
	if result.Verification == "" {
		result.Verification = "verified"
	}
	if recovered {
		result.Verified = true
	}
	return result
}

func journalIntentFingerprint(operation models.RouteOperationJournal, result RouteApplyResult) string {
	if result.Plan.ApplicationID != "" && result.Plan.DesiredMode != "" {
		return routeIntentFingerprint(result.Plan)
	}
	return operation.Fingerprint
}

func (r *Registry) recordEffectiveRoutes(workspaces []*models.Workspace) {
	r.ensureRouteState()
	r.routingStateMu.Lock()
	defer r.routingStateMu.Unlock()
	for _, ws := range workspaces {
		if ws == nil {
			continue
		}
		routes := buildNormalizedRoutes(ws)
		r.effectiveRoutes[ws.WorkspaceID] = cloneRoutes(routes)
		r.effectiveHashes[ws.WorkspaceID] = fingerprintRoutes(routes)
		r.effectiveVersions[ws.WorkspaceID] = ws.Route.RouteVersion
	}
}

func (r *Registry) routesAreEffectiveHash(wsID, hash string) bool {
	r.ensureRouteState()
	r.routingStateMu.RLock()
	defer r.routingStateMu.RUnlock()
	return hash != "" && r.effectiveHashes[wsID] == hash
}

func (r *Registry) routesAreEffective(wsID string, routes []Route) bool {
	return r.routesAreEffectiveHash(wsID, fingerprintRoutes(routes))
}

func (r *Registry) effectiveVersion(wsID string) uint64 {
	r.ensureRouteState()
	r.routingStateMu.RLock()
	defer r.routingStateMu.RUnlock()
	return r.effectiveVersions[wsID]
}

func (r *Registry) applyRouting(workspaces []*models.Workspace, required bool) error {
	provider := r.GetRoutingProvider()
	if provider == nil {
		if required {
			return fmt.Errorf("routing provider not set")
		}
		return nil
	}
	if !provider.IsAvailable() {
		if required {
			return fmt.Errorf("routing provider is unavailable")
		}
		return nil
	}
	if err := r.loadProxyWithRetry(workspaces); err != nil {
		return err
	}
	if verifier, ok := provider.(RoutingVerifier); ok {
		if err := verifier.VerifyRouting(workspaces); err != nil {
			return fmt.Errorf("verify loaded routing: %w", err)
		}
	} else if inspector, ok := provider.(RoutingInspector); ok {
		// Providers without the richer verifier can still close the apply
		// boundary with their read-only projection check.
		inSync, err := inspector.InspectRouting(workspaces)
		if err != nil {
			return fmt.Errorf("inspect loaded routing: %w", err)
		}
		if !inSync {
			return fmt.Errorf("loaded routing differs from the candidate")
		}
	}
	r.recordEffectiveRoutes(workspaces)
	return nil
}

// loadProxyWithRetry attempts to load the config into Proxy with exponential backoff.
func (r *Registry) loadProxyWithRetry(workspaces []*models.Workspace) error {
	provider := r.GetRoutingProvider()
	if provider == nil {
		return fmt.Errorf("routing provider not set")
	}

	var lastErr error
	for i := 0; i < 3; i++ {
		if i > 0 {
			time.Sleep(time.Duration(i) * time.Second)
		}
		if err := provider.LoadInitialConfig(workspaces); err != nil {
			lastErr = err
			log.Printf("[loadProxyWithRetry] attempt %d failed: %v", i+1, err)
			continue
		}
		return nil
	}
	return fmt.Errorf("failed after %d attempts: %w", 3, lastErr)
}
