package services

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thiagojdb/rementor/internal/config"
	"github.com/thiagojdb/rementor/internal/fleet"
	"github.com/thiagojdb/rementor/internal/models"
	"github.com/thiagojdb/rementor/internal/peers"
)

func inheritanceManagers(t *testing.T) (*peers.Manager, *peers.Manager) {
	t.Helper()
	create := func(name string) *peers.Manager {
		m, err := peers.Open(filepath.Join(t.TempDir(), name), name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(m.Close)
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := listener.Addr().String()
		listener.Close()
		if err := m.Configure(address, "https://"+address); err != nil {
			t.Fatal(err)
		}
		return m
	}
	receiver, owner := create("receiver"), create("owner")
	code, invitation, err := owner.CreateInvitation()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := receiver.Pair(context.Background(), code, invitation.Fingerprint); err != nil {
		t.Fatal(err)
	}
	return receiver, owner
}

func inheritanceRegistry(t *testing.T, client *peers.Manager) *Registry {
	t.Helper()
	r := &Registry{store: NewConfigWorkspaceStore(), routingProvider: &mockRoutingProvider{}}
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	if err := r.SetPeerWorkspaceClient(client.State().Fingerprint, client); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestLiveInheritancePersistsOverridesCachesAndPinnedSessions(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	receiver, owner := inheritanceManagers(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer upstream.Close()
	_, portText, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	port, _ := strconv.Atoi(portText)
	source := &Registry{workspaces: []*models.Workspace{{WorkspaceID: "source", Type: models.WorkspaceTypeRouting, RoutingConfig: &models.RoutingConfig{LocalDomain: "source.localhost", DefaultRemoteBaseURL: upstream.URL}, Applications: []*models.Application{{ID: "orders", PublicPath: "/orders", Port: port, Active: true}, {ID: "billing", PublicPath: "/billing", Port: port, Active: true}}}}, store: &fakeWorkspaceStore{}, routingProvider: &mockRoutingProvider{}}
	owner.SetWorkspaceSource(source)
	if err := config.AppendWorkspace(models.WorkspaceConfig{ID: "receiving", Type: models.WorkspaceTypeRouting, Routing: models.RoutingConfig{LocalDomain: "receiver.localhost", DefaultRemoteBaseURL: upstream.URL}, Applications: []models.ApplicationConfig{{ID: "orders", PublicPath: "/orders", Port: 9090}, {ID: "billing", PublicPath: "/billing", Port: 9091}}}); err != nil {
		t.Fatal(err)
	}
	r := inheritanceRegistry(t, receiver)
	scope := fleet.Scope{WorkspaceID: "source", EnvironmentID: "source"}
	first, err := r.FollowPeerWorkspace(context.Background(), "receiving", owner.State().Fingerprint, scope, 0, "follow")
	if err != nil || !first.Changed {
		t.Fatalf("follow failed: %v", err)
	}
	orders := first.Workspace.Applications[0]
	if orders.Port != 0 || orders.PeerEndpoint == nil || orders.PeerEndpoint.OwnerFingerprint != owner.State().Fingerprint || !orders.Active {
		t.Fatal("follow did not install owner-aware selection")
	}
	if !r.checkLocalApplicationHealth(orders) {
		t.Fatal("selected peer application was probed in the receiver namespace")
	}
	if _, err := r.FollowPeerWorkspace(context.Background(), "receiving", owner.State().Fingerprint, scope, 0, ""); !errors.Is(err, ErrInheritanceConflict) {
		t.Fatal("stale follow was accepted")
	}
	unchanged, err := r.SyncPeerWorkspace(context.Background(), "receiving")
	if err != nil || unchanged.Changed || unchanged.Workspace.Route.RouteVersion != first.Workspace.Route.RouteVersion {
		t.Fatalf("unchanged sync reloaded routes: %v", err)
	}
	// Stopping this computer's polling is not evidence that the source failed.
	// This also exercises the cancellation race after a successful live update.
	beforeCheck, _ := r.InheritanceStatus("receiving")
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := r.SyncPeerWorkspace(cancelled, "receiving"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled synchronization was accepted", err)
	}
	afterCheck, _ := r.InheritanceStatus("receiving")
	if afterCheck.Status != beforeCheck.Status || afterCheck.Source != beforeCheck.Source || afterCheck.Error != beforeCheck.Error || !afterCheck.CheckedAt.Equal(beforeCheck.CheckedAt) {
		t.Fatal("local cancellation overwrote verified source health", afterCheck)
	}
	local := models.RouteSelection{Mode: "local", Port: 9091}
	override, err := r.OverrideInheritedRoute("receiving", "billing", &local, first.Workspace.Route.RouteVersion, "billing-local")
	if err != nil {
		t.Fatal(err)
	}
	if override.Workspace.Applications[1].Port != 9091 || override.Workspace.Applications[1].PeerEndpoint != nil {
		t.Fatal("own override retained peer endpoint")
	}
	session, err := r.CreateRoutingSession("receiving", "feature", "session")
	if err != nil {
		t.Fatal(err)
	}
	if session.Inheritance != nil || session.Applications[0].PeerEndpoint != nil || session.Applications[0].Port != 0 || session.Applications[0].Active {
		t.Fatal("new session inherited live/local state instead of a pinned remote baseline")
	}
	if _, _, err := source.ToggleAppWithMetadata("source", "orders", "parent-remote"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.RunPeerSynchronization(ctx, 25*time.Millisecond); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for r.GetWorkspaceView("receiving").Applications[0].Active && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	updated := r.GetWorkspaceView("receiving")
	if updated.Applications[0].Active || updated.Applications[1].Port != 9091 || !updated.Applications[1].Active {
		t.Fatal("live update lost parent choice or local override")
	}
	if r.GetWorkspaceView(session.WorkspaceID).Session.BaselineVersion != session.Session.BaselineVersion {
		t.Fatal("peer polling refreshed a pinned session")
	}
	status, err := r.InheritanceStatus("receiving")
	if err != nil || status.Status != "current" || status.Source.Version <= first.Workspace.Inheritance.Source.Version {
		t.Fatalf("source status did not advance: %v %+v", err, status)
	}
	ownerState := owner.State()
	if err := owner.Configure("", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SyncPeerWorkspace(context.Background(), "receiving"); err == nil {
		t.Fatal("offline source reported a current fetch")
	}
	stale, _ := r.InheritanceStatus("receiving")
	if stale.Status != "stale" || stale.Source.Digest != updated.Inheritance.Source.Digest || stale.Error == "" {
		t.Fatal("offline source discarded cache or hid stale status")
	}
	local.Port = 9092
	offlineEdit, err := r.OverrideInheritedRoute("receiving", "billing", &local, updated.Route.RouteVersion, "offline-edit")
	if err != nil || offlineEdit.Workspace.Applications[1].Port != 9092 {
		t.Fatalf("offline override failed: %v", err)
	}
	restarted := inheritanceRegistry(t, receiver)
	loaded := restarted.GetWorkspaceView("receiving")
	if loaded.Inheritance == nil || loaded.Inheritance.Source != updated.Inheritance.Source || loaded.Applications[1].Port != 9092 {
		t.Fatal("restart lost relationship, cache or override")
	}
	unknown, _ := restarted.InheritanceStatus("receiving")
	if unknown.Status != "stale" || !unknown.CheckedAt.IsZero() {
		t.Fatal("restart claimed a current synchronization")
	}
	reset, err := restarted.OverrideInheritedRoute("receiving", "billing", nil, loaded.Route.RouteVersion, "reset")
	if err != nil || reset.Workspace.Applications[1].Port != 0 || reset.Workspace.Applications[1].PeerEndpoint == nil {
		t.Fatalf("offline reset did not resume cached parent: %v", err)
	}
	if err := owner.Configure(ownerState.Listen, ownerState.URL); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.SyncPeerWorkspace(context.Background(), "receiving"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := restarted.ToggleAppWithMetadata("receiving", "orders", "override-toggle"); err != nil {
		t.Fatal(err)
	}
	toggled := restarted.GetWorkspaceView("receiving")
	if toggled.Inheritance.Overrides["orders"].Mode != "local" {
		t.Fatal("ordinary route toggle was not recorded as an override")
	}
	if !restarted.checkLocalApplicationHealth(toggled.Applications[0]) {
		t.Fatal("explicit peer-local override could not use the owner's inactive registration")
	}
	if _, err := restarted.SyncPeerWorkspace(context.Background(), "receiving"); err != nil {
		t.Fatal(err)
	}
	if !restarted.GetWorkspaceView("receiving").Applications[0].Active {
		t.Fatal("sync erased toggle override")
	}
	stopped, err := restarted.StopFollowingWorkspace("receiving", toggled.Route.RouteVersion, "stop")
	if err != nil || stopped.Workspace.Inheritance != nil || stopped.Workspace.Applications[0].Port != 9090 || stopped.Workspace.Applications[0].Active {
		t.Fatalf("stop did not restore original registrations: %v", err)
	}
	journals, err := config.LoadRouteOperations()
	if err != nil || len(journals) < 5 {
		t.Fatalf("inheritance mutations were not journaled: %v", err)
	}
	for _, journal := range journals {
		if journal.Status != "committed" {
			t.Fatalf("unexpected journal status: %s", journal.Status)
		}
	}
}

type delayedInheritanceClient struct {
	PeerWorkspaceClient
	started chan struct{}
	release chan struct{}
}

func (c delayedInheritanceClient) FetchWorkspace(ctx context.Context, peer, id, digest string) (*fleet.Snapshot, error) {
	snapshot, err := c.PeerWorkspaceClient.FetchWorkspace(ctx, peer, id, digest)
	close(c.started)
	select {
	case <-c.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return snapshot, err
}

func TestInheritanceRejectsConcurrentEditsAndRevocationBeforeCommit(t *testing.T) {
	for _, action := range []string{"edit", "revoke"} {
		t.Run(action, func(t *testing.T) {
			receiver, owner := inheritanceManagers(t)
			source := &Registry{workspaces: []*models.Workspace{{WorkspaceID: "source", Type: models.WorkspaceTypeRouting, RoutingConfig: &models.RoutingConfig{LocalDomain: "source.localhost"}, Applications: []*models.Application{{ID: "orders", PublicPath: "/orders", Port: 8080, Active: true}}}}}
			owner.SetWorkspaceSource(source)
			store := &fakeWorkspaceStore{}
			r := &Registry{workspaces: []*models.Workspace{{WorkspaceID: "receiving", Type: models.WorkspaceTypeRouting, RoutingConfig: &models.RoutingConfig{LocalDomain: "receiver.localhost"}, Applications: []*models.Application{{ID: "orders", PublicPath: "/orders", Port: 9090}}}}, store: store, routingProvider: &mockRoutingProvider{}}
			if err := r.SetPeerWorkspaceClient(receiver.State().Fingerprint, receiver); err != nil {
				t.Fatal(err)
			}
			first, err := r.FollowPeerWorkspace(context.Background(), "receiving", owner.State().Fingerprint, fleet.Scope{WorkspaceID: "source", EnvironmentID: "source"}, 0, "")
			if err != nil {
				t.Fatal(err)
			}
			// Change only committed source intent/version before the delayed fetch.
			source.mutationMu.Lock()
			source.workspacesMu.Lock()
			source.workspaces[0].Route.RouteVersion = 1
			source.workspaces[0].Applications[0].Active = false
			source.workspacesMu.Unlock()
			source.mutationMu.Unlock()
			delayed := delayedInheritanceClient{PeerWorkspaceClient: receiver, started: make(chan struct{}), release: make(chan struct{})}
			if err := r.SetPeerWorkspaceClient(receiver.State().Fingerprint, delayed); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() { _, err := r.SyncPeerWorkspace(context.Background(), "receiving"); result <- err }()
			<-delayed.started
			if action == "edit" {
				selection := models.RouteSelection{Mode: "local", Port: 9091}
				if _, err := r.OverrideInheritedRoute("receiving", "orders", &selection, first.Workspace.Route.RouteVersion, ""); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := receiver.Revoke(owner.State().Fingerprint); err != nil {
					t.Fatal(err)
				}
			}
			close(delayed.release)
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("obsolete or revoked fetch committed")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("sync deadlocked with mutation/revocation")
			}
			current := r.GetWorkspaceView("receiving")
			if current.Inheritance.Source != first.Workspace.Inheritance.Source {
				t.Fatal("rejected sync replaced validated cache")
			}
			if action == "edit" && current.Applications[0].Port != 9091 {
				t.Fatal("sync erased concurrent override")
			}
		})
	}
}

func TestInheritanceApplyAndPersistenceFailuresRetainPriorProjection(t *testing.T) {
	for _, stage := range []string{"proxy", "persist"} {
		t.Run(stage, func(t *testing.T) {
			receiver, owner := inheritanceManagers(t)
			source := &Registry{workspaces: []*models.Workspace{{WorkspaceID: "source", Type: models.WorkspaceTypeRouting, RoutingConfig: &models.RoutingConfig{LocalDomain: "source.localhost"}, Applications: []*models.Application{{ID: "orders", PublicPath: "/orders", Port: 8080, Active: true}}}}}
			owner.SetWorkspaceSource(source)
			store := &fakeWorkspaceStore{}
			provider := &mockRoutingProvider{}
			r := &Registry{workspaces: []*models.Workspace{{WorkspaceID: "receiving", Type: models.WorkspaceTypeRouting, RoutingConfig: &models.RoutingConfig{LocalDomain: "receiver.localhost"}, Applications: []*models.Application{{ID: "orders", PublicPath: "/orders", Port: 9090}}}}, store: store, routingProvider: provider}
			if err := r.SetPeerWorkspaceClient(receiver.State().Fingerprint, receiver); err != nil {
				t.Fatal(err)
			}
			if stage == "proxy" {
				provider.err = fmt.Errorf("proxy failure")
			} else {
				store.replaceErr = fmt.Errorf("persistence failure")
			}
			if _, err := r.FollowPeerWorkspace(context.Background(), "receiving", owner.State().Fingerprint, fleet.Scope{WorkspaceID: "source", EnvironmentID: "source"}, 0, ""); err == nil {
				t.Fatal("failed transaction committed")
			}
			current := r.GetWorkspaceView("receiving")
			if current.Inheritance != nil || current.Applications[0].Port != 9090 || current.Applications[0].PeerEndpoint != nil {
				t.Fatal("failed follow changed prior projection")
			}
		})
	}
}
