package services

import (
	"context"
	"errors"
	"github.com/thiagojdb/rementor/internal/config"
	"github.com/thiagojdb/rementor/internal/fleet"
	"github.com/thiagojdb/rementor/internal/models"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestComputerFollowingMirrorsAllEnvironmentsAndPreservesLocalExtras(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	receiver, owner := inheritanceManagers(t)
	source := &Registry{store: &fakeWorkspaceStore{}, routingProvider: &mockRoutingProvider{}}
	if err := source.SetPeerWorkspaceClient(owner.State().Fingerprint, owner); err != nil {
		t.Fatal(err)
	}
	makeEnvironment := func(id string, port int) *models.Workspace {
		ws := &models.Workspace{WorkspaceID: id, Type: models.WorkspaceTypeRouting, RoutingConfig: &models.RoutingConfig{LocalDomain: id + ".localhost", DefaultRemoteBaseURL: "https://example.com"}, Applications: []*models.Application{{ID: "orders", PublicPath: "/orders", Health: "health", Port: port, Active: true}}}
		ws.SetDefaults()
		return ws
	}
	source.workspaces = []*models.Workspace{makeEnvironment("dev", 9101), makeEnvironment("qa", 9102)}
	// A source session is never attached or mirrored onto the receiving computer.
	source.workspaces = append(source.workspaces, &models.Workspace{WorkspaceID: "source-session", Session: &models.RoutingSession{EnvironmentID: "dev"}, Type: models.WorkspaceTypeRouting, RoutingConfig: &models.RoutingConfig{LocalDomain: "source-session.dev.localhost", DefaultRemoteBaseURL: "https://example.com"}})
	owner.SetWorkspaceSource(source)
	prior := makeEnvironment("dev", 9201)
	extra := makeEnvironment("extra", 9202)
	if err := config.ReplaceWorkspaces([]*models.Workspace{prior, extra}); err != nil {
		t.Fatal(err)
	}
	r := inheritanceRegistry(t, receiver)
	if err := r.FollowComputer(context.Background(), owner.State().Fingerprint, 0); err != nil {
		t.Fatal(err)
	}
	if len(r.GetWorkspaces()) != 3 || r.GetWorkspaceView("qa") == nil || r.GetWorkspaceView("source-session") != nil {
		t.Fatal("following did not mirror the complete environment set without sessions")
	}
	dev := r.GetWorkspaceView("dev")
	if dev.Applications[0].Port != 0 || dev.Applications[0].PeerEndpoint == nil || len(dev.Inheritance.Overrides) != 0 {
		t.Fatal("mirrored registration lost ownership or invented a route override")
	}
	if r.GetWorkspaceView("extra").Applications[0].Port != 9202 {
		t.Fatal("extra local environment was changed")
	}
	// Editing on the receiver updates the source, without any receiver override.
	if _, err := r.ToggleApp("dev", "orders"); err != nil {
		t.Fatal(err)
	}
	if source.GetWorkspaceView("dev").Applications[0].Active || r.GetWorkspaceView("dev").Applications[0].Active || len(r.GetWorkspaceView("dev").Inheritance.Overrides) != 0 {
		t.Fatal("toggle did not edit source configuration")
	}
	if _, err := r.ToggleAllToLocal("dev"); err != nil {
		t.Fatal(err)
	}
	if !source.GetWorkspaceView("dev").Applications[0].Active || !r.GetWorkspaceView("dev").Applications[0].Active {
		t.Fatal("toggle all did not edit source")
	}
	pattern := "/orders/*"
	if _, err := r.UpdateRoutePattern("dev", "orders", &pattern); err != nil {
		t.Fatal(err)
	}
	if got := source.GetWorkspaceView("dev").Applications[0].RoutePattern; got == nil || *got != pattern {
		t.Fatal("pattern edit did not reach source")
	}
	stale := r.GetWorkspaceView("dev")
	if _, err := source.ToggleApp("dev", "orders"); err != nil {
		t.Fatal(err)
	}
	if err := r.EditFollowedRoute(context.Background(), "dev", "orders", stale.Route.RouteVersion, fleet.ConfigurationEdit{Mode: models.RouteModeLocal}); !errors.Is(err, fleet.ErrConfigurationConflict) {
		t.Fatalf("stale edit must fail, got %v", err)
	}
	if source.GetWorkspaceView("dev").Applications[0].Active {
		t.Fatal("stale edit overwrote source")
	}
	if err := r.SyncComputerConfiguration(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(r.GetWorkspaceView("dev").Inheritance.Overrides) != 0 {
		t.Fatal("source edits introduced overrides")
	}
	if err := r.DeleteWorkspace("dev"); err == nil {
		t.Fatal("allowed local deletion of followed environment")
	}
	session, err := r.CreateRoutingSession("dev", "local feature", "")
	if err != nil {
		t.Fatal(err)
	}
	baseline := session.Session.BaselineVersion
	if session.Inheritance != nil {
		t.Fatal("local session started following source session")
	}
	source.workspaces = append(source.workspaces, makeEnvironment("staging", 9103))
	if err := r.SyncComputerConfiguration(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.GetWorkspaceView("staging") == nil || r.GetWorkspaceView(session.WorkspaceID).Session.BaselineVersion != baseline {
		t.Fatal("new source environment did not appear or refreshed pinned local session")
	}
	restarted := inheritanceRegistry(t, receiver)
	state, _ := restarted.ComputerFollowing()
	if state.OwnerFingerprint != owner.State().Fingerprint || restarted.GetWorkspaceView("staging") == nil {
		t.Fatal("restart lost whole-computer following")
	}
	if err := restarted.StopFollowingComputer(state.Revision); err != nil {
		t.Fatal(err)
	}
	if restarted.GetWorkspaceView("dev").Applications[0].Port != 9201 || restarted.GetWorkspaceView("extra").Applications[0].Port != 9202 || restarted.GetWorkspaceView("qa") != nil {
		t.Fatal("detach failed to restore preexisting configuration and preserve extras")
	}
	if restarted.GetWorkspaceView(session.WorkspaceID) == nil {
		t.Fatal("detach removed a local pinned session")
	}
}

type memoryComputerStore struct {
	fakeWorkspaceStore
	following models.ComputerFollowing
}

func (s *memoryComputerStore) LoadComputerFollowing() (models.ComputerFollowing, error) {
	return cloneFollowing(s.following), nil
}
func (s *memoryComputerStore) SaveComputerFollowing(workspaces []*models.Workspace, state models.ComputerFollowing, expected uint64) error {
	if expected != s.following.Revision {
		return ErrInheritanceConflict
	}
	if err := s.ReplaceWorkspaces(workspaces); err != nil {
		return err
	}
	s.following = cloneFollowing(state)
	return nil
}

func TestComputerFollowingForwardsSourceEditsWithoutHoldingPeerLock(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	receiver, middleManager := inheritanceManagers(t)
	_, sourceManager := inheritanceManagers(t)
	code, invitation, err := sourceManager.CreateInvitation()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := middleManager.Pair(context.Background(), code, invitation.Fingerprint); err != nil {
		t.Fatal(err)
	}
	ws := &models.Workspace{WorkspaceID: "dev", Type: models.WorkspaceTypeRouting, RoutingConfig: &models.RoutingConfig{LocalDomain: "dev.localhost", DefaultRemoteBaseURL: "https://example.com"}, Applications: []*models.Application{{ID: "orders", PublicPath: "/orders", Port: 9101, Active: true}}}
	ws.SetDefaults()
	source := &Registry{store: &fakeWorkspaceStore{}, routingProvider: &mockRoutingProvider{}, workspaces: []*models.Workspace{ws}}
	if err := source.SetPeerWorkspaceClient(sourceManager.State().Fingerprint, sourceManager); err != nil {
		t.Fatal(err)
	}
	sourceManager.SetWorkspaceSource(source)
	middle := &Registry{store: &memoryComputerStore{}, routingProvider: &mockRoutingProvider{}}
	if err := middle.SetPeerWorkspaceClient(middleManager.State().Fingerprint, middleManager); err != nil {
		t.Fatal(err)
	}
	middleManager.SetWorkspaceSource(middle)
	if err := middle.FollowComputer(context.Background(), sourceManager.State().Fingerprint, 0); err != nil {
		t.Fatal(err)
	}
	r := inheritanceRegistry(t, receiver)
	if err := r.FollowComputer(context.Background(), middleManager.State().Fingerprint, 0); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := r.EditFollowedRoute(ctx, "dev", "orders", r.GetWorkspaceView("dev").Route.RouteVersion, fleet.ConfigurationEdit{Mode: models.RouteModeRemote}); err != nil {
		t.Fatal(err)
	}
	for _, registry := range []*Registry{source, middle, r} {
		if registry.GetWorkspaceView("dev").Applications[0].Active {
			t.Fatal("transitive edit did not reach source and both followers")
		}
	}
	port := 9123
	if err := r.EditFollowedRoute(ctx, "dev", "orders", r.GetWorkspaceView("dev").Route.RouteVersion, fleet.ConfigurationEdit{Action: "register-port", RegistrationPort: &port}); err != nil {
		t.Fatal(err)
	}
	if source.GetWorkspaceView("dev").Applications[0].Port != port || middle.GetWorkspaceView("dev").Applications[0].Port != 0 || r.GetWorkspaceView("dev").Applications[0].Port != 0 {
		t.Fatal("transitive registration changed the wrong computer")
	}
	if err := middleManager.Revoke(receiver.State().Fingerprint); err != nil {
		t.Fatal(err)
	}
	if err := r.EditFollowedRoute(ctx, "dev", "orders", r.GetWorkspaceView("dev").Route.RouteVersion, fleet.ConfigurationEdit{Action: "delete-application"}); err == nil {
		t.Fatal("revoked peer edited source")
	}
	if source.GetWorkspaceView("dev").Applications[0].Active {
		t.Fatal("revoked edit changed source")
	}
}

func TestComputerFollowingPersistenceFailureKeepsPreviousConfiguration(t *testing.T) {
	receiver, owner := inheritanceManagers(t)
	ws := &models.Workspace{WorkspaceID: "dev", Type: models.WorkspaceTypeRouting, RoutingConfig: &models.RoutingConfig{LocalDomain: "dev.localhost", DefaultRemoteBaseURL: "https://example.com"}, Applications: []*models.Application{{ID: "orders", PublicPath: "/orders", Port: 9101, Active: true}}}
	ws.SetDefaults()
	source := &Registry{workspaces: []*models.Workspace{ws}, store: &fakeWorkspaceStore{}}
	if err := source.SetPeerWorkspaceClient(owner.State().Fingerprint, owner); err != nil {
		t.Fatal(err)
	}
	owner.SetWorkspaceSource(source)
	prior := cloneWorkspace(ws)
	prior.Applications[0].Port = 9201
	store := &memoryComputerStore{fakeWorkspaceStore: fakeWorkspaceStore{workspaces: []*models.Workspace{prior}, replaceErr: errors.New("write failed")}}
	provider := &mockRoutingProvider{}
	r := &Registry{workspaces: []*models.Workspace{prior}, store: store, routingProvider: provider}
	if err := r.SetPeerWorkspaceClient(receiver.State().Fingerprint, receiver); err != nil {
		t.Fatal(err)
	}
	if err := r.FollowComputer(context.Background(), owner.State().Fingerprint, 0); err == nil {
		t.Fatal("expected persistence failure")
	}
	state, _ := r.ComputerFollowing()
	if state.Revision != 0 || state.OwnerFingerprint != "" || r.GetWorkspaceView("dev").Applications[0].Port != 9201 || store.workspaces[0].Applications[0].Port != 9201 {
		t.Fatal("failed following replaced saved or live configuration")
	}
}

func TestEnvironmentOptOutKeepsCurrentSetupAcrossSyncRestartAndSourceRemoval(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	receiver, owner := inheritanceManagers(t)
	makeEnv := func(id string, port int) *models.Workspace {
		ws := &models.Workspace{WorkspaceID: id, Type: models.WorkspaceTypeRouting, RoutingConfig: &models.RoutingConfig{LocalDomain: id + ".localhost", DefaultRemoteBaseURL: "https://example.com"}, Applications: []*models.Application{{ID: "orders", PublicPath: "/orders", Port: port, Active: true}}}
		ws.SetDefaults()
		return ws
	}
	source := &Registry{store: &fakeWorkspaceStore{}, routingProvider: &mockRoutingProvider{}, workspaces: []*models.Workspace{makeEnv("dev", 9101), makeEnv("qa", 9102)}}
	if err := source.SetPeerWorkspaceClient(owner.State().Fingerprint, owner); err != nil {
		t.Fatal(err)
	}
	owner.SetWorkspaceSource(source)
	original := makeEnv("dev", 9201)
	if err := config.ReplaceWorkspaces([]*models.Workspace{original}); err != nil {
		t.Fatal(err)
	}
	r := inheritanceRegistry(t, receiver)
	ctx := context.Background()
	if err := r.FollowComputer(ctx, owner.State().Fingerprint, 0); err != nil {
		t.Fatal(err)
	}
	dev := r.GetWorkspaceView("dev")
	peerEndpoint := dev.Applications[0].PeerEndpoint.Clone()
	session, err := r.CreateRoutingSession("dev", "feature", "")
	if err != nil {
		t.Fatal(err)
	}
	baseline := session.Session.BaselineVersion
	state, _ := r.ComputerFollowing()
	if err := r.SetEnvironmentFollowing(ctx, "dev", false, state.Revision, dev.Route.RouteVersion); err != nil {
		t.Fatal(err)
	}
	local := r.GetWorkspaceView("dev")
	if r.IsComputerFollowed("dev") || !r.IsComputerFollowed("qa") || local.Inheritance != nil || local.Applications[0].Port != 0 || !reflect.DeepEqual(peerEndpoint, local.Applications[0].PeerEndpoint) {
		t.Fatal("opt-out changed setup or lost endpoint ownership")
	}
	if _, err := r.ToggleApp("dev", "orders"); err != nil {
		t.Fatal(err)
	}
	if !source.GetWorkspaceView("dev").Applications[0].Active {
		t.Fatal("local toggle edited source")
	}
	pattern := "/orders/*"
	if _, err := r.UpdateRoutePattern("dev", "orders", &pattern); err != nil {
		t.Fatal(err)
	}
	if source.GetWorkspaceView("dev").Applications[0].RoutePattern != nil {
		t.Fatal("local pattern edited source")
	}
	if _, err := source.ToggleApp("qa", "orders"); err != nil {
		t.Fatal(err)
	}
	if err := r.SyncComputerConfiguration(ctx); err != nil {
		t.Fatal(err)
	}
	if r.GetWorkspaceView("dev").Applications[0].Active || r.GetWorkspaceView("qa").Applications[0].Active {
		t.Fatal("sync replaced opt-out or failed to update followed environment")
	}
	restarted := inheritanceRegistry(t, receiver)
	state, _ = restarted.ComputerFollowing()
	if !state.Excluded["dev"] || restarted.IsComputerFollowed("dev") || restarted.GetWorkspaceView("dev").Inheritance != nil {
		t.Fatal("restart lost opt-out")
	}
	if restarted.GetWorkspaceView(session.WorkspaceID).Session.BaselineVersion != baseline {
		t.Fatal("opt-out refreshed local session")
	}
	savedDev := source.GetWorkspaceView("dev")
	source.workspaces = []*models.Workspace{source.GetWorkspaceView("qa")}
	if err := restarted.SyncComputerConfiguration(ctx); err != nil {
		t.Fatal(err)
	}
	if restarted.GetWorkspaceView("dev") == nil {
		t.Fatal("source removal removed opted-out environment")
	}
	source.workspaces = append(source.workspaces, savedDev)
	if err := restarted.SyncComputerConfiguration(ctx); err != nil {
		t.Fatal(err)
	}
	if restarted.IsComputerFollowed("dev") || restarted.GetWorkspaceView("dev").Applications[0].Active {
		t.Fatal("source readdition silently resumed following")
	}
	state, _ = restarted.ComputerFollowing()
	oldVersion := restarted.GetWorkspaceView("dev").Route.RouteVersion
	if _, err := restarted.ToggleApp("dev", "orders"); err != nil {
		t.Fatal(err)
	}
	if err := restarted.SetEnvironmentFollowing(ctx, "dev", true, state.Revision, oldVersion); !errors.Is(err, ErrInheritanceConflict) {
		t.Fatalf("resume overwrote concurrent local edit: %v", err)
	}
	if err := restarted.SetEnvironmentFollowing(ctx, "dev", true, state.Revision, restarted.GetWorkspaceView("dev").Route.RouteVersion); err != nil {
		t.Fatal(err)
	}
	if !restarted.IsComputerFollowed("dev") || restarted.GetWorkspaceView("dev").Applications[0].RoutePattern != nil {
		t.Fatal("resume did not replace local configuration with source")
	}
	state, _ = restarted.ComputerFollowing()
	if err := restarted.StopFollowingComputer(state.Revision); err != nil {
		t.Fatal(err)
	}
	if restarted.GetWorkspaceView("dev").Applications[0].RoutePattern == nil || !restarted.GetWorkspaceView("dev").Applications[0].Active {
		t.Fatal("computer detach did not restore resumed environment's local settings")
	}
}

func TestEnvironmentOptOutFailureAndStaleSourcePreserveState(t *testing.T) {
	receiver, owner := inheritanceManagers(t)
	ws := &models.Workspace{WorkspaceID: "dev", Type: models.WorkspaceTypeRouting, RoutingConfig: &models.RoutingConfig{LocalDomain: "dev.localhost", DefaultRemoteBaseURL: "https://example.com"}, Applications: []*models.Application{{ID: "orders", PublicPath: "/orders", Port: 9101, Active: true}}}
	ws.SetDefaults()
	source := &Registry{workspaces: []*models.Workspace{ws}, store: &fakeWorkspaceStore{}}
	if err := source.SetPeerWorkspaceClient(owner.State().Fingerprint, owner); err != nil {
		t.Fatal(err)
	}
	owner.SetWorkspaceSource(source)
	store := &memoryComputerStore{}
	provider := &mockRoutingProvider{}
	r := &Registry{store: store, routingProvider: provider}
	if err := r.SetPeerWorkspaceClient(receiver.State().Fingerprint, receiver); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := r.FollowComputer(ctx, owner.State().Fingerprint, 0); err != nil {
		t.Fatal(err)
	}
	state, _ := r.ComputerFollowing()
	version := r.GetWorkspaceView("dev").Route.RouteVersion
	store.replaceErr = errors.New("disk failure")
	if err := r.SetEnvironmentFollowing(ctx, "dev", false, state.Revision, version); err == nil {
		t.Fatal("persist failure accepted")
	}
	if !r.IsComputerFollowed("dev") || len(store.following.Excluded) != 0 {
		t.Fatal("failed opt-out changed membership")
	}
	store.replaceErr = nil
	r.computerCheck = ComputerFollowingCheck{Status: "stale", Error: "source unavailable"}
	if err := r.SetEnvironmentFollowing(ctx, "dev", false, state.Revision, version); err != nil {
		t.Fatal(err)
	}
	state, check := r.ComputerFollowing()
	if check.Status != "stale" {
		t.Fatal("local opt-out claimed source was synced")
	}
	detached := r.GetWorkspaceView("dev")
	if err := r.SetEnvironmentFollowing(ctx, "dev", true, state.Revision-1, detached.Route.RouteVersion); !errors.Is(err, ErrInheritanceConflict) {
		t.Fatalf("stale revision accepted: %v", err)
	}
	if err := owner.Revoke(receiver.State().Fingerprint); err != nil {
		t.Fatal(err)
	}
	if err := r.SetEnvironmentFollowing(ctx, "dev", true, state.Revision, detached.Route.RouteVersion); err == nil {
		t.Fatal("revoked peer resumed following")
	}
	if r.IsComputerFollowed("dev") {
		t.Fatal("failed resume changed membership")
	}
	if err := r.StopFollowingComputer(state.Revision); err != nil {
		t.Fatal(err)
	}
	if r.GetWorkspaceView("dev") == nil || r.GetWorkspaceView("dev").Inheritance != nil {
		t.Fatal("computer detach discarded opted-out environment")
	}
}

func TestOptedOutPeerEndpointSurvivesSourceSelectionChangesButPinsRegistration(t *testing.T) {
	receiver, owner := inheritanceManagers(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("owner endpoint")) }))
	defer backend.Close()
	_, portText, _ := net.SplitHostPort(backend.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	ws := &models.Workspace{WorkspaceID: "dev", Type: models.WorkspaceTypeRouting, RoutingConfig: &models.RoutingConfig{LocalDomain: "dev.localhost", DefaultRemoteBaseURL: "https://example.com"}, Applications: []*models.Application{{ID: "orders", PublicPath: "/orders", Port: port, Active: true}}}
	ws.SetDefaults()
	source := &Registry{workspaces: []*models.Workspace{ws}, store: &fakeWorkspaceStore{}, routingProvider: &mockRoutingProvider{}}
	if err := source.SetPeerWorkspaceClient(owner.State().Fingerprint, owner); err != nil {
		t.Fatal(err)
	}
	owner.SetWorkspaceSource(source)
	r := &Registry{store: &memoryComputerStore{}, routingProvider: &mockRoutingProvider{}}
	if err := r.SetPeerWorkspaceClient(receiver.State().Fingerprint, receiver); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := r.FollowComputer(ctx, owner.State().Fingerprint, 0); err != nil {
		t.Fatal(err)
	}
	state, _ := r.ComputerFollowing()
	if err := r.SetEnvironmentFollowing(ctx, "dev", false, state.Revision, r.GetWorkspaceView("dev").Route.RouteVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := source.ToggleApp("dev", "orders"); err != nil {
		t.Fatal(err)
	}
	if err := r.SyncComputerConfiguration(ctx); err != nil {
		t.Fatal(err)
	}
	local := r.GetWorkspaceView("dev")
	if !local.Applications[0].Active || local.Inheritance != nil {
		t.Fatal("source route selection replaced local choice")
	}
	endpoint := *local.Applications[0].PeerEndpoint
	response := httptest.NewRecorder()
	receiver.Forward(response, httptest.NewRequest("GET", "http://dev.localhost/orders", nil), endpoint)
	if response.Code != 200 || response.Body.String() != "owner endpoint" {
		t.Fatalf("retained endpoint unavailable after source toggle: %d %s", response.Code, response.Body.String())
	}
	// A different registration must not be adopted by a local environment.
	source.workspaces[0].Applications[0].Port = port + 1
	source.workspaces[0].Route.RouteVersion++
	if err := r.SyncComputerConfiguration(ctx); err != nil {
		t.Fatal(err)
	}
	if *r.GetWorkspaceView("dev").Applications[0].PeerEndpoint != endpoint {
		t.Fatal("changed registration was silently adopted")
	}
	response = httptest.NewRecorder()
	receiver.Forward(response, httptest.NewRequest("GET", "http://dev.localhost/orders", nil), endpoint)
	if response.Code != http.StatusConflict {
		t.Fatalf("changed registration did not fail as stale: %d", response.Code)
	}
}
