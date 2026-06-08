package services

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/thiagojdb/rementor/internal/config"
	"github.com/thiagojdb/rementor/internal/models"
)

// Registry manages workspaces and applications
type Registry struct {
	workspaces       []*models.Workspace
	workspacesMu     sync.RWMutex
	routingProvider  RoutingProvider
	stopChan         chan struct{}
	httpClient       *http.Client
	subscribers      map[string]int // workspaceID -> count of active subscribers
	subscribersMu    sync.RWMutex
	healthStreams    map[uint64]healthStream
	healthStreamsMu  sync.RWMutex
	nextHealthStream uint64
	lastFullCheck    time.Time
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
			stopChan:      make(chan struct{}),
			httpClient:    &http.Client{Timeout: 5 * time.Second},
			subscribers:   make(map[string]int),
			healthStreams: make(map[uint64]healthStream),
			lastFullCheck: time.Time{},
		}
	})
	return globalRegistry
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

	workspaces, err := config.LoadWorkspaces()
	if err != nil {
		return fmt.Errorf("failed to load workspaces: %w", err)
	}

	r.workspacesMu.Lock()
	r.workspaces = workspaces
	r.workspacesMu.Unlock()

	log.Printf("Loaded %d workspaces", len(workspaces))

	// Initialize runtime for all applications
	for _, ws := range r.workspaces {
		for _, app := range ws.Applications {
			app.InitializeRuntime()
		}
	}
	log.Println("Initialized application runtimes")

	// Load state
	if err := config.LoadState(r.workspaces); err != nil {
		log.Printf("Warning: failed to load state: %v", err)
	}

	// Warm health checks (non-blocking)
	log.Println("Warming health checks...")
	go r.warmHealth()

	// Load initial config if routing provider is available
	if r.routingProvider != nil {
		log.Printf("Loading initial routing config for %d workspaces", len(r.workspaces))
		if err := r.reloadProxy(); err != nil {
			log.Printf("Warning: failed to load initial routing config: %v", err)
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
			}(ws, app)
		}
	}

	elapsed := time.Since(startTime)
	log.Printf("[Health Check] Completed %s health check loop for workspaces: %v (total: %d applications, took: %v)",
		checkType, wsNames, totalApps, elapsed)
}

// GetWorkspaces returns all workspaces
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

// FindApp finds an application by workspace ID and application name
func (r *Registry) FindApp(wsID, appName string) (*models.Workspace, *models.Application, error) {
	ws := r.FindWorkspace(wsID)
	if ws == nil {
		return nil, nil, fmt.Errorf("workspace not found: %s", wsID)
	}

	for _, app := range ws.Applications {
		if app.ID == appName {
			return ws, app, nil
		}
	}

	return nil, nil, fmt.Errorf("application not found: %s/%s", wsID, appName)
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

// SaveState saves the current state
func (r *Registry) SaveState() error {
	return config.SaveState(r.workspaces)
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

// ToggleApp toggles an application between local and remote
func (r *Registry) ToggleApp(wsID, appName string) (*models.Application, error) {
	ws, app, err := r.FindApp(wsID, appName)
	if err != nil {
		return nil, err
	}

	// local-apps workspaces have no toggle concept
	if ws.IsLocalApps() {
		return app, nil
	}

	// Toggle active state in-memory
	app.Active = !app.Active

	if err := r.reloadProxy(); err != nil {
		app.Active = !app.Active
		return nil, fmt.Errorf("apply routing: %w", err)
	}
	if err := config.SaveState(r.workspaces); err != nil {
		app.Active = !app.Active
		_ = r.reloadProxy()
		return nil, fmt.Errorf("save state: %w", err)
	}

	// Update health status asynchronously to not block the response
	go func() {
		healthOk := CheckHealth(app.HealthURL())
		var remoteOk bool
		remoteBase := app.GetRemoteBaseUrl(ws)
		if remoteBase != "" {
			remoteOk = CheckHealth(app.RemoteHealthURL(remoteBase))
		}
		now := time.Now()
		app.Runtime.UpdateBothStatuses(healthOk, &now, remoteOk, &now)
	}()

	return app, nil
}

// ToggleAllToRemote toggles all applications to remote
func (r *Registry) ToggleAllToRemote(wsID string) (*ToggleResult, error) {
	ws := r.FindWorkspace(wsID)
	if ws == nil {
		return nil, fmt.Errorf("workspace not found: %s", wsID)
	}

	if ws.IsLocalApps() {
		return &ToggleResult{}, nil
	}

	changed := false
	result := &ToggleResult{}
	changedApps := make([]*models.Application, 0)
	for _, app := range ws.Applications {
		if app.Active && app.HasLocal() {
			app.Active = false
			changedApps = append(changedApps, app)
			changed = true
			result.SuccessCount++
		}
	}

	if !changed {
		return result, nil
	}

	if err := r.reloadProxy(); err != nil {
		for _, app := range changedApps {
			app.Active = true
		}
		return nil, fmt.Errorf("apply routing: %w", err)
	}
	if err := config.SaveState(r.workspaces); err != nil {
		for _, app := range changedApps {
			app.Active = true
		}
		_ = r.reloadProxy()
		return nil, fmt.Errorf("save state: %w", err)
	}

	return result, nil
}

// ToggleAllToLocal toggles all applications to local
func (r *Registry) ToggleAllToLocal(wsID string) (*ToggleResult, error) {
	ws := r.FindWorkspace(wsID)
	if ws == nil {
		return nil, fmt.Errorf("workspace not found: %s", wsID)
	}

	if ws.IsLocalApps() {
		return &ToggleResult{}, nil
	}

	changed := false
	result := &ToggleResult{}
	changedApps := make([]*models.Application, 0)
	for _, app := range ws.Applications {
		if !app.Active && app.HasLocal() {
			app.Active = true
			changedApps = append(changedApps, app)
			changed = true
			result.SuccessCount++
		}
	}

	if !changed {
		return result, nil
	}

	if err := r.reloadProxy(); err != nil {
		for _, app := range changedApps {
			app.Active = false
		}
		return nil, fmt.Errorf("apply routing: %w", err)
	}
	if err := config.SaveState(r.workspaces); err != nil {
		for _, app := range changedApps {
			app.Active = false
		}
		_ = r.reloadProxy()
		return nil, fmt.Errorf("save state: %w", err)
	}

	return result, nil
}

// SyncRouting reloads the full routing config from the current in-memory state.
func (r *Registry) SyncRouting() error {
	return r.reloadProxy()
}

// UpdateWorkspaceApplications persists the new application list for a workspace and reloads it in-memory
func (r *Registry) UpdateWorkspaceApplications(wsID string, apps []models.ApplicationConfig, localDomain, defaultRemoteBaseURL string) error {
	old := r.FindWorkspace(wsID)
	if old == nil {
		return fmt.Errorf("workspace not found: %s", wsID)
	}
	oldApps := applicationConfigs(old.Applications)
	oldLocalDomain := old.GetLocalDomain()
	oldRemoteBaseURL := old.GetDefaultRemoteBaseURL()

	if err := config.UpdateWorkspaceApplications(wsID, apps, localDomain, defaultRemoteBaseURL); err != nil {
		return err
	}
	workspaces, err := config.LoadWorkspaces()
	if err != nil {
		return err
	}

	r.workspacesMu.Lock()
	for _, loaded := range workspaces {
		if loaded.WorkspaceID == wsID {
			for _, app := range loaded.Applications {
				app.InitializeRuntime()
			}
			for i, ws := range r.workspaces {
				if ws.WorkspaceID == wsID {
					r.workspaces[i] = loaded
					break
				}
			}
			break
		}
	}
	r.workspacesMu.Unlock()

	if err := r.reloadProxyIfAvailable(); err != nil {
		_ = config.UpdateWorkspaceApplications(wsID, oldApps, oldLocalDomain, oldRemoteBaseURL)
		r.workspacesMu.Lock()
		for i, workspace := range r.workspaces {
			if workspace.WorkspaceID == wsID {
				r.workspaces[i] = old
				break
			}
		}
		r.workspacesMu.Unlock()
		return fmt.Errorf("apply routing: %w", err)
	}

	return nil
}

// AddWorkspace adds a new workspace to the registry at runtime
func (r *Registry) AddWorkspace(ws *models.Workspace) error {
	r.workspacesMu.Lock()
	r.workspaces = append(r.workspaces, ws)
	r.workspacesMu.Unlock()

	for _, app := range ws.Applications {
		app.InitializeRuntime()
	}

	if err := r.reloadProxyIfAvailable(); err != nil {
		r.workspacesMu.Lock()
		for i, candidate := range r.workspaces {
			if candidate == ws {
				r.workspaces = append(r.workspaces[:i], r.workspaces[i+1:]...)
				break
			}
		}
		r.workspacesMu.Unlock()
		return fmt.Errorf("apply routing: %w", err)
	}

	return nil
}

// DeleteWorkspace removes a workspace from the registry and config
func (r *Registry) DeleteWorkspace(wsID string) error {
	ws := r.FindWorkspace(wsID)
	if ws == nil {
		return fmt.Errorf("workspace not found: %s", wsID)
	}

	// Remove from in-memory registry
	r.workspacesMu.Lock()
	for i, w := range r.workspaces {
		if w.WorkspaceID == wsID {
			r.workspaces = append(r.workspaces[:i], r.workspaces[i+1:]...)
			break
		}
	}
	r.workspacesMu.Unlock()

	// Remove from subscribers
	r.subscribersMu.Lock()
	delete(r.subscribers, wsID)
	r.subscribersMu.Unlock()

	if err := r.reloadProxyIfAvailable(); err != nil {
		r.workspacesMu.Lock()
		r.workspaces = append(r.workspaces, ws)
		r.workspacesMu.Unlock()
		return fmt.Errorf("apply routing: %w", err)
	}
	if err := config.RemoveWorkspace(wsID); err != nil {
		r.workspacesMu.Lock()
		r.workspaces = append(r.workspaces, ws)
		r.workspacesMu.Unlock()
		_ = r.reloadProxyIfAvailable()
		return err
	}

	return nil
}

// UpdateRoutePattern updates the route pattern for an application
func (r *Registry) UpdateRoutePattern(wsID, appName string, pattern *string) (*models.Application, error) {
	_, app, err := r.FindApp(wsID, appName)
	if err != nil {
		return nil, err
	}

	oldPattern := app.RoutePattern
	app.RoutePattern = pattern
	if err := r.reloadProxy(); err != nil {
		app.RoutePattern = oldPattern
		return nil, fmt.Errorf("apply routing: %w", err)
	}
	if err := config.SaveState(r.workspaces); err != nil {
		app.RoutePattern = oldPattern
		_ = r.reloadProxy()
		return nil, fmt.Errorf("save route pattern: %w", err)
	}

	return app, nil
}

func applicationConfigs(apps []*models.Application) []models.ApplicationConfig {
	result := make([]models.ApplicationConfig, 0, len(apps))
	for _, app := range apps {
		result = append(result, models.ApplicationConfig{
			ID: app.ID, Name: app.Name, Path: app.Path, Domain: app.Domain,
			RemoteBaseUrl: app.RemoteBaseUrl, Port: app.Port, Health: app.Health,
			Active: app.Active, RoutePattern: app.RoutePattern, Context: app.Context,
			LoggerConfig: app.LoggerConfig, StripOrigin: app.StripOrigin,
		})
	}
	return result
}

// reloadProxy generates the current desired config and pushes it to Proxy.
func (r *Registry) reloadProxy() error {
	provider := r.GetRoutingProvider()
	if provider == nil {
		return fmt.Errorf("routing provider not set")
	}
	if !provider.IsAvailable() {
		return fmt.Errorf("routing provider is unavailable")
	}

	r.workspacesMu.RLock()
	wsCopy := make([]*models.Workspace, len(r.workspaces))
	copy(wsCopy, r.workspaces)
	r.workspacesMu.RUnlock()

	if err := r.loadProxyWithRetry(wsCopy); err != nil {
		return err
	}

	return nil
}

func (r *Registry) reloadProxyIfAvailable() error {
	provider := r.GetRoutingProvider()
	if provider == nil || !provider.IsAvailable() {
		return nil
	}
	return r.reloadProxy()
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
