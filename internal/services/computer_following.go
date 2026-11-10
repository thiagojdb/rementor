package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/thiagojdb/rementor/internal/config"
	"github.com/thiagojdb/rementor/internal/fleet"
	"github.com/thiagojdb/rementor/internal/models"
	"github.com/thiagojdb/rementor/internal/validation"
	"io"
	"log"
	"reflect"
	"strconv"
	"time"
)

type PeerConfigurationClient interface {
	FetchConfiguration(context.Context, string, string) (*fleet.ConfigurationSnapshot, error)
	WithTrustedPeer(string, func() error) error
}
type ComputerFollowingCheck struct {
	Status    string
	Error     string
	CheckedAt time.Time
}

func cloneFollowing(state models.ComputerFollowing) models.ComputerFollowing {
	out := state
	out.Snapshot = append(json.RawMessage(nil), state.Snapshot...)
	out.SessionIDs = map[string]string{}
	for source, local := range state.SessionIDs {
		out.SessionIDs[source] = local
	}
	out.Excluded = map[string]bool{}
	for id, excluded := range state.Excluded {
		out.Excluded[id] = excluded
	}
	out.Originals = map[string]*models.Workspace{}
	for id, workspace := range state.Originals {
		if workspace != nil {
			out.Originals[id] = cloneWorkspace(workspace)
		} else {
			out.Originals[id] = nil
		}
	}
	return out
}
func (r *Registry) ComputerFollowing() (models.ComputerFollowing, ComputerFollowingCheck) {
	r.workspacesMu.RLock()
	defer r.workspacesMu.RUnlock()
	return cloneFollowing(r.computerFollowing), r.computerCheck
}
func cachedConfiguration(state models.ComputerFollowing) (fleet.ConfigurationSnapshot, error) {
	var snapshot fleet.ConfigurationSnapshot
	if len(state.Snapshot) > fleet.MaxConfigurationBytes {
		return snapshot, errors.New("computer configuration cache exceeds limits")
	}
	decoder := json.NewDecoder(bytes.NewReader(state.Snapshot))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return snapshot, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return snapshot, errors.New("invalid computer configuration cache")
	}
	return snapshot, snapshot.Validate(state.OwnerFingerprint)
}
func (r *Registry) ExportPeerConfiguration() (fleet.ConfigurationSnapshot, error) {
	owner, _, err := r.peerWorkspaceClient()
	if err != nil {
		return fleet.ConfigurationSnapshot{}, err
	}
	// Capture the complete environment set and its computer ancestry together.
	r.workspacesMu.RLock()
	workspaces := cloneWorkspaces(r.workspaces)
	state := cloneFollowing(r.computerFollowing)
	r.workspacesMu.RUnlock()
	parents := []string{}
	if state.OwnerFingerprint != "" {
		cache, err := cachedConfiguration(state)
		if err != nil {
			return fleet.ConfigurationSnapshot{}, err
		}
		parents = append(parents, state.OwnerFingerprint)
		parents = append(parents, cache.Parents...)
	}
	environments := []fleet.ConfigurationEnvironment{}
	sessions := []fleet.ConfigurationSession{}
	for _, ws := range workspaces {
		intent, err := peerIntent(ws)
		if err != nil {
			return fleet.ConfigurationSnapshot{}, err
		}
		snapshot, err := fleet.NewSnapshot(owner, ws.Route.RouteVersion, intent)
		if err != nil {
			return fleet.ConfigurationSnapshot{}, err
		}
		color := ""
		if ws.Color != nil {
			color = *ws.Color
		}
		entry := fleet.ConfigurationEnvironment{Snapshot: snapshot, Color: color}
		if ws.Session != nil {
			metadata := cloneSession(ws.Session)
			metadata.HostPrefix = routingSessionHostPrefix(ws)
			sessions = append(sessions, fleet.ConfigurationSession{ConfigurationEnvironment: entry, Session: *metadata})
		} else {
			environments = append(environments, entry)
		}
	}
	return fleet.NewConfiguration(owner, parents, environments, sessions...)
}
func (r *Registry) configurationClient() (string, PeerConfigurationClient, error) {
	owner, client, err := r.peerWorkspaceClient()
	if err != nil {
		return owner, nil, err
	}
	configuration, ok := client.(PeerConfigurationClient)
	if !ok {
		return owner, nil, errors.New("computer configuration connection unavailable")
	}
	return owner, configuration, nil
}
func (r *Registry) FollowComputer(ctx context.Context, peer string, expected uint64) error {
	owner, client, err := r.configurationClient()
	if err != nil {
		return err
	}
	state, _ := r.ComputerFollowing()
	if state.Revision != expected {
		return ErrInheritanceConflict
	}
	if state.OwnerFingerprint != "" && state.OwnerFingerprint != peer {
		return errors.New("stop following the current computer first")
	}
	snapshot, err := client.FetchConfiguration(ctx, peer, "")
	if err != nil {
		return err
	}
	if snapshot == nil {
		return errors.New("missing computer configuration")
	}
	return client.WithTrustedPeer(peer, func() error { return r.applyComputerConfiguration(ctx, owner, peer, *snapshot, expected) })
}
func (r *Registry) SyncComputerConfiguration(ctx context.Context) error {
	owner, client, err := r.configurationClient()
	if err != nil {
		return err
	}
	state, _ := r.ComputerFollowing()
	if state.OwnerFingerprint == "" {
		return nil
	}
	cache, err := cachedConfiguration(state)
	if err != nil {
		return err
	}
	snapshot, err := client.FetchConfiguration(ctx, state.OwnerFingerprint, cache.Digest)
	if err == nil {
		err = client.WithTrustedPeer(state.OwnerFingerprint, func() error {
			if snapshot == nil {
				return ctx.Err()
			}
			return r.applyComputerConfiguration(ctx, owner, state.OwnerFingerprint, *snapshot, state.Revision)
		})
	}
	if ctx.Err() != context.Canceled && !errors.Is(err, ErrInheritanceConflict) && !errors.Is(err, config.ErrFleetConflict) {
		r.workspacesMu.Lock()
		if r.computerFollowing.OwnerFingerprint == state.OwnerFingerprint {
			check := ComputerFollowingCheck{Status: "current", CheckedAt: time.Now().UTC()}
			if err != nil {
				check.Status = "stale"
				check.Error = err.Error()
			}
			r.computerCheck = check
		}
		r.workspacesMu.Unlock()
	}
	return err
}
func (r *Registry) applyComputerConfiguration(ctx context.Context, owner, peer string, snapshot fleet.ConfigurationSnapshot, expected uint64) error {
	return r.changeComputerFollowing(expected, func(previous []*models.Workspace, state models.ComputerFollowing) ([]*models.Workspace, models.ComputerFollowing, error) {
		return r.computerConfigurationCandidate(ctx, owner, peer, snapshot, previous, state, false)
	})
}
func (r *Registry) computerConfigurationCandidate(ctx context.Context, owner, peer string, snapshot fleet.ConfigurationSnapshot, previous []*models.Workspace, state models.ComputerFollowing, force bool) ([]*models.Workspace, models.ComputerFollowing, error) {
	if err := snapshot.Validate(peer); err != nil {
		return nil, state, err
	}
	if owner == peer {
		return nil, state, errors.New("computer cannot follow itself")
	}
	for _, parent := range snapshot.Parents {
		if parent == owner {
			return nil, state, errors.New("computer inheritance cycle")
		}
	}

	if err := ctx.Err(); err != nil {
		return nil, state, err
	}
	if state.OwnerFingerprint != "" && state.OwnerFingerprint != peer {
		return nil, state, ErrInheritanceConflict
	}
	old := fleet.ConfigurationSnapshot{}
	if state.OwnerFingerprint != "" {
		var err error
		old, err = cachedConfiguration(state)
		if err != nil {
			return nil, state, err
		}
	}
	if old.Digest == snapshot.Digest && !force {
		return previous, state, nil
	}
	for _, prior := range old.Environments {
		for _, current := range snapshot.Environments {
			if prior.Snapshot.Intent.Scope == current.Snapshot.Intent.Scope && (current.Snapshot.Version < prior.Snapshot.Version || (current.Snapshot.Version == prior.Snapshot.Version && current.Snapshot.Digest != prior.Snapshot.Digest)) {
				return nil, state, errors.New("source snapshot version regressed or changed without a new version")
			}
		}
	}
	managed := map[string]bool{}
	sessionIDs := map[string]bool{}
	for _, id := range state.SessionIDs {
		sessionIDs[id] = true
	}
	for id := range state.Originals {
		if !sessionIDs[id] {
			managed[id] = true
		}
	}
	nextManaged := map[string]bool{}
	for _, environment := range snapshot.Environments {
		id := environment.Snapshot.Intent.Scope.WorkspaceID
		if !state.Excluded[id] {
			nextManaged[id] = true
		}
	}
	next := []*models.Workspace{}
	for _, ws := range previous {
		if !managed[ws.WorkspaceID] && !nextManaged[ws.WorkspaceID] {
			local := cloneWorkspace(ws)
			if state.Excluded[ws.WorkspaceID] {
				r.refreshUnchangedPeerBindings(local, old, snapshot)
			}
			next = append(next, local)
			continue
		}
		if managed[ws.WorkspaceID] && !nextManaged[ws.WorkspaceID] {
			if original := state.Originals[ws.WorkspaceID]; original != nil {
				next = append(next, cloneWorkspace(original))
			} else {
				for _, local := range previous {
					if local.Session != nil && local.Session.EnvironmentID == ws.WorkspaceID && !sessionIDs[local.WorkspaceID] {
						return nil, state, fmt.Errorf("source removed environment %s with a local session; close or move that session first", ws.WorkspaceID)
					}
				}
			}
			delete(state.Originals, ws.WorkspaceID)
		}
	}
	if state.Originals == nil {
		state.Originals = map[string]*models.Workspace{}
	}
	for _, environment := range snapshot.Environments {
		source := environment.Snapshot
		id := source.Intent.Scope.WorkspaceID
		if state.Excluded[id] {
			continue
		}
		prior := findWorkspace(previous, id)
		if prior != nil && prior.Session != nil {
			return nil, state, fmt.Errorf("source environment %s conflicts with a local session", id)
		}
		if !managed[id] {
			if prior != nil {
				state.Originals[id] = cloneWorkspace(prior)
			} else {
				state.Originals[id] = nil
			}
		}
		workspace, err := r.projectComputerEnvironment(owner, environment, prior, managed[id])
		if err != nil {
			return nil, state, err
		}
		next = append(next, workspace)
	}
	next, err := r.projectComputerSessions(owner, peer, snapshot, previous, next, &state)
	if err != nil {
		return nil, state, err
	}
	state.OwnerFingerprint = peer
	state.Snapshot, _ = json.Marshal(snapshot)
	state.Revision++
	return next, state, nil
}

// Keep a retained endpoint usable when its owner changes routing selections,
// but never adopt a changed registration or copy source settings into a local
// environment. A changed port/identity/context leaves the pinned target stale.
func (r *Registry) refreshUnchangedPeerBindings(ws *models.Workspace, old, current fleet.ConfigurationSnapshot) {
	var before, after *fleet.Snapshot
	for i := range old.Environments {
		source := &old.Environments[i].Snapshot
		if source.Intent.Scope.WorkspaceID == ws.WorkspaceID {
			before = source
			break
		}
	}
	for i := range current.Environments {
		source := &current.Environments[i].Snapshot
		if source.Intent.Scope.WorkspaceID == ws.WorkspaceID {
			after = source
			break
		}
	}
	if before == nil || after == nil || before.Digest == after.Digest {
		return
	}
	changed := false
	for _, app := range ws.Applications {
		endpoint := app.PeerEndpoint
		if endpoint == nil {
			continue
		}
		pinned := before.Endpoint(endpoint.ApplicationID)
		if *endpoint != pinned {
			continue
		}
		for _, prior := range before.Intent.Applications {
			if prior.ID != endpoint.ApplicationID || prior.Port == 0 {
				continue
			}
			for _, next := range after.Intent.Applications {
				if next.ID == prior.ID && next.Port == prior.Port && next.CanonicalAppID() == prior.CanonicalAppID() && next.ServiceID == prior.ServiceID && next.Health == prior.Health && next.BackendContextPath() == prior.BackendContextPath() {
					updated := after.Endpoint(next.ID)
					app.PeerEndpoint = &updated
					changed = true
				}
			}
		}
	}
	if changed {
		operation := r.beginOperation("endpoint-refresh", "")
		r.applyOperation(ws, nil, operation)
		for _, app := range ws.Applications {
			setAppOperation(ws, app, operation)
		}
	}
}

func (r *Registry) projectComputerEnvironment(owner string, environment fleet.ConfigurationEnvironment, prior *models.Workspace, managed bool) (*models.Workspace, error) {
	return r.projectComputerWorkspace(owner, environment, environment.Snapshot.Intent.Scope.WorkspaceID, nil, prior, managed)
}

func (r *Registry) projectComputerWorkspace(owner string, environment fleet.ConfigurationEnvironment, id string, session *models.RoutingSession, prior *models.Workspace, managed bool) (*models.Workspace, error) {
	source := environment.Snapshot
	base := models.WorkspaceConfig{ID: id, Name: source.Intent.Name, Color: environment.Color, Type: source.Intent.Type, Routing: source.Intent.Routing, Applications: models.CloneApplicationConfigs(source.Intent.Applications), Session: cloneSession(session)}
	localScope := fleet.Scope{WorkspaceID: id, EnvironmentID: id}
	if session != nil {
		localScope.EnvironmentID, localScope.SessionID = session.EnvironmentID, id
	}
	if managed && prior != nil {
		base.Inheritance = prior.Inheritance.Clone()
		base.Inheritance.Overrides = map[string]models.RouteSelection{}
		base.Inheritance.OriginalRouting = source.Intent.Routing
		base.Inheritance.OriginalApplications = models.CloneApplicationConfigs(source.Intent.Applications)
	}
	projection, err := fleet.ProjectInheritance(owner, localScope, base, source, time.Now())
	if err != nil {
		return nil, err
	}
	workspace := r.workspaceStore().WorkspaceFromConfig(projection)
	applyInheritanceProjection(workspace, projection)
	if managed && prior != nil {
		for _, app := range workspace.Applications {
			for _, oldApp := range prior.Applications {
				if app.ID == oldApp.ID && app.Port == oldApp.Port && app.Health == oldApp.Health && app.BackendContextPath() == oldApp.BackendContextPath() && reflect.DeepEqual(app.PeerEndpoint, oldApp.PeerEndpoint) && app.GetRemoteBaseUrl(workspace) == oldApp.GetRemoteBaseUrl(prior) {
					copyRuntime(oldApp, app)
				}
			}
		}
	}
	operation := r.beginOperation("computer-follow", "")
	r.applyOperation(workspace, nil, operation)
	for _, app := range workspace.Applications {
		setAppOperation(workspace, app, operation)
	}
	return workspace, nil
}

func (r *Registry) changeComputerFollowing(expected uint64, change func([]*models.Workspace, models.ComputerFollowing) ([]*models.Workspace, models.ComputerFollowing, error)) error {
	r.mutationMu.Lock()
	defer r.mutationMu.Unlock()
	store, ok := r.workspaceStore().(ComputerFollowingStore)
	if !ok {
		return errors.New("computer following persistence unavailable")
	}
	previous := r.workspaceSnapshot()
	state, _ := r.ComputerFollowing()
	if state.Revision != expected {
		return ErrInheritanceConflict
	}
	next, candidate, err := change(previous, cloneFollowing(state))
	if err != nil {
		return err
	}
	if candidate.Revision == state.Revision {
		return nil
	}
	if err = validateWorkspaces(next); err != nil {
		return err
	}
	if err = validateComputerFollowingState(candidate, next); err != nil {
		return err
	}
	operation := r.beginOperation("computer-configuration", "")
	journal := models.RouteOperationJournal{OperationID: operation.OperationID, WorkspaceID: "@computer", CorrelationID: operation.CorrelationID, ExpectedVersion: expected, RouteVersion: operation.RouteVersion, Status: "prepared", Phase: "prepared", CreatedAt: operation.CreatedAt, UpdatedAt: operation.CreatedAt, PriorState: previous, CandidateState: cloneWorkspaces(next), Result: json.RawMessage(strconv.FormatUint(candidate.Revision, 10))}
	journalStore, journaled := r.routeJournalStore()
	if journaled {
		if err := journalStore.BeginRouteOperation(journal); err != nil {
			return err
		}
	}
	fail := func(stage string, cause error) error {
		rollback := r.rollbackAfterApplyFailure(previous)
		journal.Status = "rolled_back"
		journal.Phase = stage
		journal.Error = cause.Error()
		journal.RollbackStatus = rollback
		journal.Degraded = rollback != "succeeded" && rollback != "not-required"
		journal.UpdatedAt = time.Now().UTC()
		if journaled {
			if err := journalStore.UpdateRouteOperation(journal); err != nil {
				log.Printf("Computer configuration rollback journal: %v", err)
			}
		}
		return &RouteTransactionError{Operation: operation, Status: stage, Rollback: rollback, Degraded: journal.Degraded, Cause: cause}
	}
	if err = r.applyRouting(next, true); err != nil {
		return fail("proxy-apply", err)
	}
	journal.Status = "proxy-applied"
	journal.Phase = "proxy-applied"
	journal.UpdatedAt = time.Now().UTC()
	if journaled {
		if err := journalStore.UpdateRouteOperation(journal); err != nil {
			return fail("journal", err)
		}
	}
	// The complete projection and following state commit in one SQLite transaction.
	if err = store.SaveComputerFollowing(next, candidate, expected); err != nil {
		return fail("persist", err)
	}
	journal.Status = "committed"
	journal.Phase = "committed"
	journal.UpdatedAt = time.Now().UTC()
	if journaled {
		if err := journalStore.UpdateRouteOperation(journal); err != nil {
			log.Printf("Computer configuration commit journal: %v", err)
		}
	}
	r.workspacesMu.Lock()
	r.workspaces = next
	r.computerFollowing = candidate
	if candidate.OwnerFingerprint == "" {
		r.computerCheck = ComputerFollowingCheck{}
	} else if state.OwnerFingerprint != candidate.OwnerFingerprint || !bytes.Equal(state.Snapshot, candidate.Snapshot) {
		r.computerCheck = ComputerFollowingCheck{Status: "current", CheckedAt: time.Now().UTC()}
	}
	r.workspacesMu.Unlock()
	return nil
}
func (r *Registry) StopFollowingComputer(expected uint64) error {
	return r.changeComputerFollowing(expected, func(previous []*models.Workspace, state models.ComputerFollowing) ([]*models.Workspace, models.ComputerFollowing, error) {
		if state.OwnerFingerprint == "" {
			return previous, state, nil
		}
		if _, err := cachedConfiguration(state); err != nil {
			return nil, state, err
		}
		managed := map[string]bool{}
		for id := range state.Originals {
			managed[id] = true
		}
		next := []*models.Workspace{}
		for _, ws := range previous {
			if !managed[ws.WorkspaceID] {
				next = append(next, cloneWorkspace(ws))
				continue
			}
			if original := state.Originals[ws.WorkspaceID]; original != nil {
				next = append(next, cloneWorkspace(original))
			} else {
				for _, local := range previous {
					if local.Session != nil && local.Session.EnvironmentID == ws.WorkspaceID && !managed[local.WorkspaceID] {
						return nil, state, fmt.Errorf("close local sessions in %s before stopping following", ws.WorkspaceID)
					}
				}
			}
		}
		return next, models.ComputerFollowing{Revision: state.Revision + 1}, nil
	})
}

// An opt-out owns its current configuration independently. Its endpoint
// references retain their original owners; detaching never adopts peer ports.
func (r *Registry) SetEnvironmentFollowing(ctx context.Context, id string, follow bool, expected, workspaceVersion uint64) error {
	if err := validation.Identifier("environment", id); err != nil {
		return err
	}
	owner, client, err := r.configurationClient()
	if err != nil {
		return err
	}
	state, _ := r.ComputerFollowing()
	if state.Revision != expected {
		return ErrInheritanceConflict
	}
	if state.OwnerFingerprint == "" {
		return errors.New("no computer is followed")
	}
	var latest *fleet.ConfigurationSnapshot
	if follow {
		latest, err = client.FetchConfiguration(ctx, state.OwnerFingerprint, "")
		if err != nil {
			return err
		}
		if latest == nil {
			return errors.New("missing computer configuration")
		}
	}
	change := func() error {
		return r.changeComputerFollowing(expected, func(previous []*models.Workspace, state models.ComputerFollowing) ([]*models.Workspace, models.ComputerFollowing, error) {
			if err := ctx.Err(); err != nil {
				return nil, state, err
			}
			if state.OwnerFingerprint == "" {
				return nil, state, errors.New("no computer is followed")
			}
			cache, err := cachedConfiguration(state)
			if err != nil {
				return nil, state, err
			}
			if latest != nil {
				cache = *latest
			}
			var source *fleet.ConfigurationEnvironment
			for i := range cache.Environments {
				if cache.Environments[i].Snapshot.Intent.Scope.WorkspaceID == id {
					source = &cache.Environments[i]
					break
				}
			}
			if source == nil {
				return nil, state, errors.New("environment is not in the source configuration")
			}
			prior := findWorkspace(previous, id)
			if (prior == nil && workspaceVersion != 0) || (prior != nil && (prior.Session != nil || prior.Route.RouteVersion != workspaceVersion)) {
				return nil, state, ErrInheritanceConflict
			}
			if follow == !state.Excluded[id] {
				return previous, state, nil
			}
			next := cloneWorkspaces(previous)
			var replacement *models.Workspace
			if follow {
				delete(state.Excluded, id)
				return r.computerConfigurationCandidate(ctx, owner, state.OwnerFingerprint, *latest, previous, state, true)
			} else {
				if prior == nil {
					return nil, state, fleet.ErrWorkspaceNotFound
				}
				replacement = cloneWorkspace(prior)
				replacement.Inheritance = nil
				operation := r.beginOperation("environment-unfollow", "")
				r.applyOperation(replacement, nil, operation)
				for _, app := range replacement.Applications {
					setAppOperation(replacement, app, operation)
				}
				if state.Excluded == nil {
					state.Excluded = map[string]bool{}
				}
				state.Excluded[id] = true
				delete(state.Originals, id)
				// Detach source sessions with the environment. Their peer endpoints
				// and pinned baselines remain intact, as do independent local sessions.
				for _, localID := range state.SessionIDs {
					if _, managed := state.Originals[localID]; !managed {
						continue
					}
					child := findWorkspace(next, localID)
					if child != nil && child.Session != nil && child.Session.EnvironmentID == id {
						child.Inheritance = nil
						delete(state.Originals, localID)
					}
				}
			}
			found := false
			for i, ws := range next {
				if ws.WorkspaceID == id {
					next[i] = replacement
					found = true
					break
				}
			}
			if !found {
				next = append(next, replacement)
			}
			state.Revision++
			return next, state, nil
		})
	}
	if follow {
		return client.WithTrustedPeer(state.OwnerFingerprint, change)
	}
	return change()
}

func (r *Registry) IsComputerFollowed(id string) bool {
	r.workspacesMu.RLock()
	defer r.workspacesMu.RUnlock()
	_, managed := r.computerFollowing.Originals[id]
	return r.computerFollowing.OwnerFingerprint != "" && managed
}

type peerConfigurationEditor interface {
	EditConfiguration(context.Context, string, fleet.ConfigurationEdit) error
}

// EditFollowedRoute writes on the source and then reads its new configuration.
// A lost acknowledgement is never retried as a toggle: the write is a desired
// mode plus a source-version check, so a retry cannot undo another person's edit.
func (r *Registry) EditFollowedRoute(ctx context.Context, id, app string, expected uint64, edit fleet.ConfigurationEdit) error {
	ws := r.GetWorkspaceView(id)
	if ws == nil || !r.IsComputerFollowed(id) || ws.Inheritance == nil {
		return errors.New("environment is not following a computer")
	}
	if ws.Route.RouteVersion != expected {
		return ErrInheritanceConflict
	}
	_, client, err := r.configurationClient()
	if err != nil {
		return err
	}
	editor, ok := client.(peerConfigurationEditor)
	if !ok {
		return errors.New("source configuration editing unavailable")
	}
	if edit.Action == "delete-environment" {
		for _, child := range r.GetWorkspaces() {
			if child.Session != nil && child.Session.EnvironmentID == id {
				return errors.New("close local routing sessions before deleting this environment")
			}
		}
	}
	edit.WorkspaceID = ws.Inheritance.Source.Scope.WorkspaceID
	edit.ApplicationID = app
	edit.ExpectedVersion = ws.Inheritance.Source.Version
	if err = editor.EditConfiguration(ctx, ws.Inheritance.Source.OwnerFingerprint, edit); err != nil {
		return err
	}
	if err := r.SyncComputerConfiguration(ctx); err != nil {
		return fmt.Errorf("source configuration saved; refreshing this computer failed: %w", err)
	}
	return nil
}
func (r *Registry) EditPeerConfiguration(ctx context.Context, edit fleet.ConfigurationEdit) error {
	return r.EditPeerConfigurationWithTrust(ctx, edit, func(commit func() error) error { return commit() })
}

// Hold trust only across a local commit; forwarding must never hold a manager
// lock while making another peer request.
func (r *Registry) EditPeerConfigurationWithTrust(ctx context.Context, edit fleet.ConfigurationEdit, guard func(func() error) error) error {
	if err := edit.Validate(); err != nil {
		return err
	}
	ws := r.GetWorkspaceView(edit.WorkspaceID)
	if ws == nil {
		return fleet.ErrWorkspaceNotFound
	}
	if ws.Route.RouteVersion != edit.ExpectedVersion {
		return fleet.ErrConfigurationConflict
	}
	if r.IsComputerFollowed(ws.WorkspaceID) {
		if err := guard(func() error { return ctx.Err() }); err != nil {
			return err
		}
		return r.EditFollowedRoute(ctx, ws.WorkspaceID, edit.ApplicationID, edit.ExpectedVersion, edit)
	}
	if ws.Inheritance != nil {
		return errors.New("edit this environment on its source computer")
	}
	return guard(func() error {
		if edit.Action == "delete-environment" {
			return r.deletePeerEnvironment(ctx, edit)
		}
		_, err := r.changeInheritance(ws.WorkspaceID, edit.ExpectedVersion, "source-edit", "", func(candidate *models.Workspace) (bool, error) {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			if candidate.Inheritance != nil {
				return false, errors.New("configuration changed; reload the environment")
			}
			if edit.Action != "" {
				return applySourceConfigurationAction(candidate, edit)
			}
			if edit.AllApplications {
				changed := false
				for _, app := range candidate.Applications {
					if app.HasLocal() && app.Active != (edit.Mode == models.RouteModeLocal) {
						app.Active = edit.Mode == models.RouteModeLocal
						changed = true
					}
				}
				return changed, nil
			}
			var app *models.Application
			for _, item := range candidate.Applications {
				if item.ID == edit.ApplicationID {
					app = item
					break
				}
			}
			if app == nil {
				return false, errors.New("application not found")
			}
			changed := false
			if edit.Mode != "" {
				local := edit.Mode == models.RouteModeLocal
				if local && !app.HasLocal() {
					return false, errors.New("source application has no local registration")
				}
				changed = app.Active != local
				app.Active = local
			}
			if edit.RemoteBaseURL != nil && app.RemoteBaseUrl != *edit.RemoteBaseURL {
				app.RemoteBaseUrl = *edit.RemoteBaseURL
				changed = true
			}
			if edit.RoutePattern != nil {
				before := ""
				if app.RoutePattern != nil {
					before = *app.RoutePattern
				}
				if before != *edit.RoutePattern {
					changed = true
					if *edit.RoutePattern == "" {
						app.RoutePattern = nil
					} else {
						value := *edit.RoutePattern
						app.RoutePattern = &value
					}
				}
			}
			return changed, nil
		})
		if errors.Is(err, ErrInheritanceConflict) {
			return fleet.ErrConfigurationConflict
		}
		return err
	})
}

func (r *Registry) toggleFollowedApplications(id, mode string) (*ToggleResult, *models.OperationMetadata, error) {
	ws := r.GetWorkspaceView(id)
	result := &ToggleResult{}
	for _, app := range ws.Applications {
		if app.HasLocal() && app.Active != (mode == models.RouteModeLocal) {
			result.SuccessCount++
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := r.EditFollowedRoute(ctx, id, "", ws.Route.RouteVersion, fleet.ConfigurationEdit{AllApplications: true, Mode: mode}); err != nil {
		return nil, nil, err
	}
	return result, r.GetWorkspaceView(id).LastOperation, nil
}

func validateComputerFollowingState(state models.ComputerFollowing, workspaces []*models.Workspace) error {
	if state.OwnerFingerprint == "" {
		if len(state.Snapshot) != 0 || len(state.Originals) != 0 || len(state.Excluded) != 0 || len(state.SessionIDs) != 0 {
			return errors.New("invalid detached computer following state")
		}
		return nil
	}
	if state.Revision == 0 {
		return errors.New("invalid computer following revision")
	}
	snapshot, err := cachedConfiguration(state)
	if err != nil {
		return err
	}
	for id, excluded := range state.Excluded {
		if !excluded {
			return errors.New("invalid computer following exclusion")
		}
		if err := validation.Identifier("excluded environment", id); err != nil {
			return err
		}
		if _, exists := state.Originals[id]; exists {
			return errors.New("excluded environment is still computer-managed")
		}
	}
	managedCount := 0
	for _, environment := range snapshot.Environments {
		if !state.Excluded[environment.Snapshot.Intent.Scope.WorkspaceID] {
			managedCount++
		}
	}
	for _, session := range snapshot.Sessions {
		if !state.Excluded[session.Session.EnvironmentID] {
			managedCount++
		}
	}
	if len(state.Originals) != managedCount {
		return errors.New("computer following membership does not match cache")
	}
	for _, environment := range snapshot.Environments {
		source := environment.Snapshot
		id := source.Intent.Scope.WorkspaceID
		if state.Excluded[id] {
			continue
		}
		original, exists := state.Originals[id]
		if !exists || (original != nil && (original.WorkspaceID != id || original.Session != nil)) {
			return errors.New("invalid saved original environment")
		}
		ws := findWorkspace(workspaces, id)
		if ws == nil || ws.Session != nil || ws.Inheritance == nil || ws.Inheritance.Source.OwnerFingerprint != state.OwnerFingerprint || ws.Inheritance.Source.Scope != source.Intent.Scope || ws.Inheritance.Source.Version != source.Version || ws.Inheritance.Source.Digest != source.Digest || len(ws.Inheritance.Overrides) != 0 {
			return errors.New("stored environment does not match followed computer configuration")
		}
	}
	seen := map[string]bool{}
	for source, local := range state.SessionIDs {
		if validation.Identifier("source session", source) != nil || validation.Identifier("local session", local) != nil || seen[local] {
			return errors.New("invalid followed session identity")
		}
		seen[local] = true
	}
	for _, session := range snapshot.Sessions {
		if state.Excluded[session.Session.EnvironmentID] {
			continue
		}
		source := session.Snapshot
		id := state.SessionIDs[source.Intent.Scope.WorkspaceID]
		original, exists := state.Originals[id]
		if id == "" || !exists || (original != nil && (original.WorkspaceID != id || original.Session == nil)) {
			return errors.New("invalid saved original session")
		}
		ws := findWorkspace(workspaces, id)
		if ws == nil || !reflect.DeepEqual(ws.Session, &session.Session) || ws.Inheritance == nil || ws.Inheritance.Source != fleet.SnapshotReference(source) || len(ws.Inheritance.Overrides) != 0 {
			return errors.New("stored session does not match followed computer configuration")
		}
	}
	return nil
}
