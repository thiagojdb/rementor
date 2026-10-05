package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/thiagojdb/rementor/internal/fleet"
	"github.com/thiagojdb/rementor/internal/models"
)

var ErrInheritanceConflict = errors.New("workspace changed; reload and review again")

type PeerWorkspaceClient interface {
	FetchWorkspace(context.Context, string, string, string) (*fleet.Snapshot, error)
	WithTrustedPeer(string, func() error) error
}

type InheritanceStatus struct {
	SourceName   string                           `json:"sourceName,omitempty"`
	Source       models.PeerWorkspaceReference    `json:"source"`
	Status       string                           `json:"status"`
	CheckedAt    time.Time                        `json:"checkedAt"`
	UpdatedAt    time.Time                        `json:"updatedAt"`
	Error        string                           `json:"error,omitempty"`
	Overrides    map[string]models.RouteSelection `json:"overrides"`
	RouteVersion uint64                           `json:"routeVersion"`
}

type InheritanceResult struct {
	Changed   bool                      `json:"changed"`
	Workspace *models.Workspace         `json:"workspace"`
	Operation *models.OperationMetadata `json:"operation,omitempty"`
}

func (r *Registry) SetPeerWorkspaceClient(owner string, client PeerWorkspaceClient) error {
	for _, ws := range r.workspaceSnapshot() {
		if err := fleet.ValidateInheritance(owner, peerScope(ws), ws.Inheritance); err != nil {
			return fmt.Errorf("workspace %s: %w", ws.WorkspaceID, err)
		}
	}
	state, _ := r.ComputerFollowing()
	if state.OwnerFingerprint != "" {
		cache, err := cachedConfiguration(state)
		if err != nil {
			return err
		}
		for _, parent := range append([]string{state.OwnerFingerprint}, cache.Parents...) {
			if parent == owner {
				return errors.New("computer inheritance cycle")
			}
		}
	}
	r.fleetMu.Lock()
	defer r.fleetMu.Unlock()
	r.fleetOwner = owner
	r.fleetClient = client
	r.fleetChecks = make(map[string]InheritanceStatus)
	return nil
}

func (r *Registry) peerWorkspaceClient() (string, PeerWorkspaceClient, error) {
	r.fleetMu.RLock()
	defer r.fleetMu.RUnlock()
	if r.fleetClient == nil {
		return "", nil, errors.New("peer workspace connection unavailable")
	}
	return r.fleetOwner, r.fleetClient, nil
}

func inheritanceConfig(ws *models.Workspace) models.WorkspaceConfig {
	result := models.WorkspaceConfig{ID: ws.WorkspaceID, Type: ws.GetType(), Inheritance: ws.Inheritance.Clone(), Applications: applicationConfigs(ws.Applications)}
	if ws.RoutingConfig != nil {
		result.Routing = *ws.RoutingConfig
	}
	return result
}

func recordInheritedSelection(ws *models.Workspace, app *models.Application) {
	if ws.Inheritance == nil {
		return
	}
	prior := ws.Inheritance.Overrides[app.ID]
	selection := models.RouteSelection{Mode: models.RouteModeRemote, Port: app.Port, RemoteBaseURL: prior.RemoteBaseURL, RoutePattern: prior.RoutePattern}
	if app.Active {
		selection.Mode = models.RouteModeLocal
		selection.Port = app.Port
	}
	ws.Inheritance.Overrides[app.ID] = selection
}

func recordInheritedPattern(ws *models.Workspace, app *models.Application) {
	if ws.Inheritance == nil {
		return
	}
	selection, exists := ws.Inheritance.Overrides[app.ID]
	if !exists {
		selection.Mode = "inherit"
	}
	pattern := ""
	if app.RoutePattern != nil {
		pattern = *app.RoutePattern
	}
	selection.RoutePattern = &pattern
	ws.Inheritance.Overrides[app.ID] = selection
}

func (r *Registry) FollowPeerWorkspace(ctx context.Context, id, peer string, scope fleet.Scope, expected uint64, correlation string) (InheritanceResult, error) {
	if r.IsComputerFollowed(id) {
		return InheritanceResult{}, errors.New("this environment follows the computer selected in Configuration")
	}
	if err := fleet.ValidateScope(scope); err != nil {
		return InheritanceResult{}, err
	}
	owner, client, err := r.peerWorkspaceClient()
	if err != nil {
		return InheritanceResult{}, err
	}
	ws := r.GetWorkspaceView(id)
	if ws == nil {
		return InheritanceResult{}, fleet.ErrWorkspaceNotFound
	}
	if ws.Route.RouteVersion != expected {
		return InheritanceResult{}, ErrInheritanceConflict
	}
	snapshot, err := client.FetchWorkspace(ctx, peer, scope.WorkspaceID, "")
	if err != nil {
		return InheritanceResult{}, err
	}
	if snapshot == nil || snapshot.Intent.Scope != scope {
		return InheritanceResult{}, errors.New("peer returned a different environment or session")
	}
	if err := snapshot.Validate(peer, scope.WorkspaceID); err != nil {
		return InheritanceResult{}, err
	}
	var result InheritanceResult
	err = client.WithTrustedPeer(peer, func() error {
		var err error
		result, err = r.changeInheritance(id, expected, "inheritance-follow", correlation, func(ws *models.Workspace) (bool, error) {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			candidate, err := fleet.ProjectInheritance(owner, peerScope(ws), inheritanceConfig(ws), *snapshot, time.Now())
			if err != nil {
				return false, err
			}
			if ws.Inheritance != nil && ws.Inheritance.Source == candidate.Inheritance.Source {
				return false, nil
			}
			applyInheritanceProjection(ws, candidate)
			return true, nil
		})
		return err
	})
	if err == nil {
		r.recordInheritanceCheck(id, fleet.SnapshotReference(*snapshot), "current", nil)
	}
	return result, err
}

func applyInheritanceProjection(ws *models.Workspace, candidate models.WorkspaceConfig) {
	previousRemote := ws.GetDefaultRemoteBaseURL()
	previous := map[string]*models.Application{}
	for _, app := range ws.Applications {
		previous[app.ID] = app
	}
	ws.Inheritance = candidate.Inheritance
	ws.Applications = applicationsFromConfigs(ws, candidate.Applications)
	// The inherited active selection is authoritative. The ordinary registration
	// adapter preserves old toggles; synchronization must copy the candidate.
	for i := range ws.Applications {
		ws.Applications[i].Active = candidate.Applications[i].Active
		app := ws.Applications[i]
		if prior := previous[app.ID]; prior != nil && (prior.Port != app.Port || !reflect.DeepEqual(prior.PeerEndpoint, app.PeerEndpoint) || prior.RemoteBaseUrl != app.RemoteBaseUrl || (app.RemoteBaseUrl == "" && previousRemote != candidate.Routing.DefaultRemoteBaseURL) || prior.Health != app.Health || prior.BackendContextPath() != app.BackendContextPath()) {
			app.Runtime.UpdateBothStatuses(false, nil, false, nil)
		}
	}
	if !ws.IsLocalApps() {
		routing := candidate.Routing
		ws.RoutingConfig = &routing
	}
	ws.SetDefaults()
}

type peerEndpointProber interface {
	ProbeEndpoint(context.Context, fleet.EndpointRef, string) (bool, error)
}

func (r *Registry) checkLocalApplicationHealth(app *models.Application) bool {
	if app.PeerEndpoint == nil {
		return CheckHealth(app.HealthURL())
	}
	_, client, err := r.peerWorkspaceClient()
	if err != nil {
		return false
	}
	prober, ok := client.(peerEndpointProber)
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	path := strings.TrimRight(app.BackendContextPath(), "/") + "/" + strings.TrimLeft(app.Health, "/")
	healthy, _ := prober.ProbeEndpoint(ctx, *app.PeerEndpoint, path)
	return healthy
}

func (r *Registry) SyncPeerWorkspace(ctx context.Context, id string) (InheritanceResult, error) {
	if r.IsComputerFollowed(id) {
		before := r.GetWorkspaceView(id)
		if err := r.SyncComputerConfiguration(ctx); err != nil {
			return InheritanceResult{}, err
		}
		current := r.GetWorkspaceView(id)
		if current == nil {
			return InheritanceResult{}, fleet.ErrWorkspaceNotFound
		}
		return InheritanceResult{Changed: current.Route.RouteVersion != before.Route.RouteVersion, Workspace: current, Operation: current.LastOperation}, nil
	}
	owner, client, err := r.peerWorkspaceClient()
	if err != nil {
		return InheritanceResult{}, err
	}
	ws := r.GetWorkspaceView(id)
	if ws == nil {
		return InheritanceResult{}, fleet.ErrWorkspaceNotFound
	}
	if ws.Inheritance == nil {
		return InheritanceResult{}, errors.New("workspace is not following a computer")
	}
	source := ws.Inheritance.Source
	snapshot, err := client.FetchWorkspace(ctx, source.OwnerFingerprint, source.Scope.WorkspaceID, source.Digest)
	if err != nil {
		// A cancelled local request/poller says nothing about source availability.
		if ctx.Err() != context.Canceled {
			r.recordInheritanceCheck(id, source, "stale", err)
		}
		return InheritanceResult{}, err
	}
	var result InheritanceResult
	err = client.WithTrustedPeer(source.OwnerFingerprint, func() error {
		if snapshot == nil {
			current := r.GetWorkspaceView(id)
			if current == nil || current.Inheritance == nil || current.Inheritance.Source != source {
				return ErrInheritanceConflict
			}
			result = InheritanceResult{Workspace: current}
			return nil
		}
		if snapshot.Intent.Scope != source.Scope {
			return errors.New("peer changed the selected environment or session")
		}
		if err := snapshot.Validate(source.OwnerFingerprint, source.Scope.WorkspaceID); err != nil {
			return err
		}
		var err error
		result, err = r.changeInheritance(id, ws.Route.RouteVersion, "inheritance-sync", "", func(current *models.Workspace) (bool, error) {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			if current.Inheritance == nil || current.Inheritance.Source != source {
				return false, ErrInheritanceConflict
			}
			candidate, err := fleet.ProjectInheritance(owner, peerScope(current), inheritanceConfig(current), *snapshot, time.Now())
			if err != nil {
				return false, err
			}
			applyInheritanceProjection(current, candidate)
			return true, nil
		})
		return err
	})
	if err != nil {
		// A cancelled local request/poller says nothing about source availability.
		if ctx.Err() != context.Canceled {
			r.recordInheritanceCheck(id, source, "stale", err)
		}
	} else {
		ref := source
		if snapshot != nil {
			ref = fleet.SnapshotReference(*snapshot)
		}
		r.recordInheritanceCheck(id, ref, "current", nil)
	}
	return result, err
}

// A nil selection resets this binding to its validated cached parent choice.
// These local mutations do not require the source to be reachable.
func (r *Registry) OverrideInheritedRoute(id, application string, selection *models.RouteSelection, expected uint64, correlation string) (InheritanceResult, error) {
	if r.IsComputerFollowed(id) {
		if selection == nil || selection.Port != 0 || selection.Mode == "inherit" {
			return InheritanceResult{}, errors.New("followed environments edit their source; local overrides are unavailable")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := r.EditFollowedRoute(ctx, id, application, expected, fleet.ConfigurationEdit{Mode: selection.Mode, RoutePattern: selection.RoutePattern, RemoteBaseURL: &selection.RemoteBaseURL})
		if err != nil {
			return InheritanceResult{}, err
		}
		ws := r.GetWorkspaceView(id)
		return InheritanceResult{Changed: true, Workspace: ws, Operation: ws.LastOperation}, nil
	}
	owner, _, err := r.peerWorkspaceClient()
	if err != nil {
		return InheritanceResult{}, err
	}
	return r.changeInheritance(id, expected, "inheritance-override", correlation, func(ws *models.Workspace) (bool, error) {
		if ws.Inheritance == nil {
			return false, errors.New("workspace is not following a computer")
		}
		_, app, err := findAppInWorkspace(ws, application)
		if err != nil {
			return false, err
		}
		before := ws.Inheritance.Overrides
		state := ws.Inheritance.Clone()
		if selection == nil {
			delete(state.Overrides, app.ID)
		} else {
			copy := *selection
			copy.RoutePattern = cloneStringPtr(selection.RoutePattern)
			if copy.Mode == models.RouteModeRemote && copy.Port == 0 {
				copy.Port = app.Port
			}
			if copy.Mode == models.RouteModeLocal && copy.RemoteBaseURL == "" {
				copy.RemoteBaseURL = state.Overrides[app.ID].RemoteBaseURL
			}
			state.Overrides[app.ID] = copy
		}
		if reflect.DeepEqual(before, state.Overrides) {
			return false, nil
		}
		cached, err := fleet.CachedSnapshot(state)
		if err != nil {
			return false, err
		}
		base := inheritanceConfig(ws)
		base.Inheritance = state
		candidate, err := fleet.ProjectInheritance(owner, peerScope(ws), base, cached, state.UpdatedAt)
		if err != nil {
			return false, err
		}
		applyInheritanceProjection(ws, candidate)
		return true, nil
	})
}

func (r *Registry) StopFollowingWorkspace(id string, expected uint64, correlation string) (InheritanceResult, error) {
	if r.IsComputerFollowed(id) {
		return InheritanceResult{}, errors.New("stop following the computer in Configuration")
	}
	result, err := r.changeInheritance(id, expected, "inheritance-stop", correlation, func(ws *models.Workspace) (bool, error) {
		if ws.Inheritance == nil {
			return false, nil
		}
		candidate := inheritanceConfig(ws)
		candidate.Applications = models.CloneApplicationConfigs(ws.Inheritance.OriginalApplications)
		candidate.Routing = ws.Inheritance.OriginalRouting
		candidate.Inheritance = nil
		applyInheritanceProjection(ws, candidate)
		return true, nil
	})
	if err == nil {
		r.fleetMu.Lock()
		delete(r.fleetChecks, id)
		r.fleetMu.Unlock()
	}
	return result, err
}

func (r *Registry) InheritanceStatus(id string) (InheritanceStatus, error) {
	ws := r.GetWorkspaceView(id)
	if ws == nil {
		return InheritanceStatus{}, fleet.ErrWorkspaceNotFound
	}
	if ws.Inheritance == nil {
		return InheritanceStatus{Status: "independent", RouteVersion: ws.Route.RouteVersion}, nil
	}
	r.fleetMu.RLock()
	check := r.fleetChecks[id]
	r.fleetMu.RUnlock()
	if check.Source != ws.Inheritance.Source || check.CheckedAt.IsZero() {
		check = InheritanceStatus{Status: "stale"}
	}
	if r.IsComputerFollowed(id) {
		_, root := r.ComputerFollowing()
		check.Status = root.Status
		check.CheckedAt = root.CheckedAt
		check.Error = root.Error
		if check.Status == "" {
			check.Status = "stale"
		}
	}
	if snapshot, err := fleet.CachedSnapshot(ws.Inheritance); err == nil {
		check.SourceName = snapshot.Intent.Name
	}
	check.Source = ws.Inheritance.Source
	check.UpdatedAt = ws.Inheritance.UpdatedAt
	check.Overrides = ws.Inheritance.Clone().Overrides
	check.RouteVersion = ws.Route.RouteVersion
	return check, nil
}

func (r *Registry) recordInheritanceCheck(id string, source models.PeerWorkspaceReference, status string, err error) {
	ws := r.GetWorkspaceView(id)
	if ws == nil || ws.Inheritance == nil || ws.Inheritance.Source != source {
		return
	}
	check := InheritanceStatus{Source: ws.Inheritance.Source, Status: status, CheckedAt: time.Now().UTC()}
	if err != nil {
		check.Error = err.Error()
	}
	r.fleetMu.Lock()
	defer r.fleetMu.Unlock()
	if r.fleetChecks == nil {
		r.fleetChecks = make(map[string]InheritanceStatus)
	}
	r.fleetChecks[id] = check
}

func (r *Registry) RunPeerSynchronization(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	timer := time.NewTicker(interval)
	defer timer.Stop()
	for {
		var requests sync.WaitGroup
		state, _ := r.ComputerFollowing()
		if state.OwnerFingerprint != "" {
			request, cancel := context.WithTimeout(ctx, 10*time.Second)
			_ = r.SyncComputerConfiguration(request)
			cancel()
		}
		for _, ws := range r.workspaceSnapshot() {
			if r.IsComputerFollowed(ws.WorkspaceID) {
				continue
			}
			if ctx.Err() != nil {
				break
			}
			if ws.Inheritance == nil {
				continue
			}
			requests.Add(1)
			go func(id string) {
				defer requests.Done()
				request, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()
				_, _ = r.SyncPeerWorkspace(request, id)
			}(ws.WorkspaceID)
		}
		requests.Wait()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}

// This transaction uses the same write-ahead journal and compensation boundary
// as route apply. Cache, relationship and overrides commit together with routes.
func (r *Registry) changeInheritance(id string, expected uint64, kind, correlation string, change func(*models.Workspace) (bool, error)) (InheritanceResult, error) {
	r.mutationMu.Lock()
	defer r.mutationMu.Unlock()
	if r.IsComputerFollowed(id) {
		return InheritanceResult{}, errors.New("configuration changed; reload the followed environment")
	}
	previous := r.workspaceSnapshot()
	ws := findWorkspace(previous, id)
	if ws == nil {
		return InheritanceResult{}, fleet.ErrWorkspaceNotFound
	}
	if ws.Route.RouteVersion != expected {
		return InheritanceResult{}, ErrInheritanceConflict
	}
	candidate := cloneWorkspaces(previous)
	next := findWorkspace(candidate, id)
	changed, err := change(next)
	if err != nil {
		return InheritanceResult{}, err
	}
	if !changed {
		return InheritanceResult{Workspace: cloneWorkspace(ws)}, nil
	}
	if err := validateWorkspaces(candidate); err != nil {
		return InheritanceResult{}, err
	}
	if r.nextRouteVersion < ws.Route.RouteVersion {
		r.nextRouteVersion = ws.Route.RouteVersion
	}
	operation := r.beginOperation(kind, correlation)
	r.applyOperation(next, nil, operation)
	for _, app := range next.Applications {
		setAppOperation(next, app, operation)
	}
	journal := models.RouteOperationJournal{OperationID: operation.OperationID, WorkspaceID: id, CorrelationID: operation.CorrelationID, ExpectedVersion: expected, RouteVersion: operation.RouteVersion, Status: "prepared", Phase: "prepared", CreatedAt: operation.CreatedAt, UpdatedAt: operation.CreatedAt, PriorState: previous, CandidateState: cloneWorkspaces(candidate)}
	store, journaled := r.routeJournalStore()
	if journaled {
		if err := store.BeginRouteOperation(journal); err != nil {
			r.releaseOperationVersion(operation)
			return InheritanceResult{}, err
		}
	}
	fail := func(stage string, cause error, durable bool) (InheritanceResult, error) {
		rollback := r.rollbackAfterApplyFailure(previous)
		if durable {
			persisted := "succeeded"
			if err := r.workspaceStore().ReplaceWorkspaces(previous); err != nil {
				persisted = "failed: " + err.Error()
			}
			rollback = combineRollbackStatus(rollback, persisted)
		}
		journal.Status = "rolled_back"
		journal.Phase = stage
		journal.Error = cause.Error()
		journal.RollbackStatus = rollback
		journal.Degraded = rollback != "succeeded" && rollback != "not-required"
		journal.UpdatedAt = time.Now().UTC()
		if journaled {
			if err := store.UpdateRouteOperation(journal); err != nil {
				log.Printf("Inheritance rollback journal %s: %v", operation.OperationID, err)
			}
		}
		r.releaseOperationVersion(operation)
		return InheritanceResult{}, &RouteTransactionError{Operation: operation, Status: stage, Rollback: rollback, Degraded: journal.Degraded, Cause: cause}
	}
	if err := r.applyRouting(candidate, true); err != nil {
		return fail("proxy-apply", err, false)
	}
	journal.Status = "proxy-applied"
	journal.Phase = "proxy-applied"
	journal.UpdatedAt = time.Now().UTC()
	if journaled {
		if err := store.UpdateRouteOperation(journal); err != nil {
			return fail("journal", err, false)
		}
	}
	operation.CompletedAt = time.Now().UTC()
	next.LastOperation.CompletedAt = operation.CompletedAt
	for _, app := range next.Applications {
		app.LastOperation.CompletedAt = operation.CompletedAt
	}
	if err := r.workspaceStore().ReplaceWorkspaces(candidate); err != nil {
		return fail("persist", err, true)
	}
	r.workspacesMu.Lock()
	r.workspaces = candidate
	r.workspacesMu.Unlock()
	journal.Status = "committed"
	journal.Phase = "committed"
	journal.UpdatedAt = time.Now().UTC()
	if journaled {
		if err := store.UpdateRouteOperation(journal); err != nil {
			log.Printf("Inheritance commit journal %s: %v", operation.OperationID, err)
		}
	}
	return InheritanceResult{Changed: true, Workspace: cloneWorkspace(next), Operation: cloneOperation(operation)}, nil
}
