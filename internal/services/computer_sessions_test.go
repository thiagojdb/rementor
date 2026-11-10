package services

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/thiagojdb/rementor/internal/fleet"
	"github.com/thiagojdb/rementor/internal/models"
)

func TestComputerFollowingSynchronizesSessionsWithoutRebasingLocalSessions(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	receiver, owner := inheritanceManagers(t)
	base := &models.Workspace{WorkspaceID: "dev", Type: models.WorkspaceTypeRouting, RoutingConfig: &models.RoutingConfig{LocalDomain: "dev.localhost", DefaultRemoteBaseURL: "https://example.com"}, Applications: []*models.Application{{ID: "orders", PublicPath: "/orders", Domain: "api.dev.localhost", Port: 9101, Active: true}}}
	base.SetDefaults()
	source := &Registry{store: &fakeWorkspaceStore{}, routingProvider: &mockRoutingProvider{}, workspaces: []*models.Workspace{base}}
	if err := source.SetPeerWorkspaceClient(owner.State().Fingerprint, owner); err != nil {
		t.Fatal(err)
	}
	owner.SetWorkspaceSource(source)
	feature, err := source.CreateRoutingSession("dev", "feature", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.EditSessionApplication(feature.WorkspaceID, "orders", "", func(a *models.ApplicationConfig) error { a.Port = 9301; a.Active = true; return nil }, false); err != nil {
		t.Fatal(err)
	}
	if _, err := source.ToggleApp(feature.WorkspaceID, "orders"); err != nil {
		t.Fatal(err)
	}
	store := NewConfigWorkspaceStore()
	r := &Registry{store: store, routingProvider: &mockRoutingProvider{}, workspaces: []*models.Workspace{cloneWorkspace(base)}}
	if err := r.SetPeerWorkspaceClient(receiver.State().Fingerprint, receiver); err != nil {
		t.Fatal(err)
	}
	local, err := r.CreateRoutingSession("dev", "local-only", "")
	if err != nil {
		t.Fatal(err)
	}
	pinned := cloneSession(local.Session)
	// An already imported session has a different receiver ID. Reuse it rather
	// than introducing a duplicate hostname or replacing its browser identity.
	imported, err := r.CreateRoutingSession("dev", "feature", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.FollowPeerWorkspace(context.Background(), imported.WorkspaceID, owner.State().Fingerprint, peerScope(feature), imported.Route.RouteVersion, ""); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := source.ExportPeerConfiguration(); err != nil {
		t.Fatalf("export source sessions: %v", err)
	}
	if err := r.FollowComputer(ctx, owner.State().Fingerprint, 0); err != nil {
		t.Fatal(err)
	}
	state, _ := r.ComputerFollowing()
	got := r.GetWorkspaceView(imported.WorkspaceID)
	if got == nil || !r.IsComputerFollowed(got.WorkspaceID) || got.Session.EnvironmentID != "dev" || got.Applications[0].Port != 0 || got.Applications[0].PeerEndpoint == nil || got.Applications[0].PeerEndpoint.WorkspaceID != feature.WorkspaceID || got.GetLocalDomain() != "feature.dev.localhost" {
		t.Fatalf("follow did not adopt the source session with owner-aware targets: %+v", got)
	}
	if got.Session.BaselineVersion != feature.Session.BaselineVersion || got.Session.BaselineRemoteURL != feature.Session.BaselineRemoteURL || len(got.Session.Baseline) != len(feature.Session.Baseline) {
		t.Fatal("source session baseline metadata was not preserved")
	}
	if _, err := r.ToggleApp(imported.WorkspaceID, "orders"); err != nil {
		t.Fatal(err)
	}
	if source.GetWorkspaceView(feature.WorkspaceID).Applications[0].Active {
		t.Fatal("session edit did not reach source")
	}
	later, err := source.CreateRoutingSession("dev", "later", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SyncComputerConfiguration(ctx); err != nil {
		t.Fatal(err)
	}
	if r.GetWorkspaceView(later.WorkspaceID) == nil {
		t.Fatal("new source session did not appear automatically")
	}
	if !sameSessionBaseline(r.GetWorkspaceView(local.WorkspaceID).Session, pinned) {
		t.Fatal("source sync refreshed an independent local baseline")
	}
	// State and session projections must validate after restart.
	restarted := &Registry{store: store, routingProvider: &mockRoutingProvider{}}
	if err := restarted.Load(); err != nil {
		t.Fatal(err)
	}
	if err := restarted.SetPeerWorkspaceClient(receiver.State().Fingerprint, receiver); err != nil {
		t.Fatal(err)
	}
	if !restarted.IsComputerFollowed(imported.WorkspaceID) {
		t.Fatal("restart lost followed session membership")
	}
	if err := source.DeleteWorkspace(later.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if err := restarted.SyncComputerConfiguration(ctx); err != nil {
		t.Fatal(err)
	}
	if restarted.GetWorkspaceView(later.WorkspaceID) != nil {
		t.Fatal("closed source session stayed mirrored")
	}
	state, _ = restarted.ComputerFollowing()
	if err := restarted.SetEnvironmentFollowing(ctx, "dev", false, state.Revision, restarted.GetWorkspaceView("dev").Route.RouteVersion); err != nil {
		t.Fatal(err)
	}
	if restarted.IsComputerFollowed(imported.WorkspaceID) || restarted.GetWorkspaceView(imported.WorkspaceID).Inheritance != nil {
		t.Fatal("environment opt-out did not detach its mirrored sessions")
	}
	if !sameSessionBaseline(restarted.GetWorkspaceView(local.WorkspaceID).Session, pinned) {
		t.Fatal("opt-out changed local session baseline")
	}
	// Opting back in reuses the detached session rather than colliding with it.
	state, _ = restarted.ComputerFollowing()
	if err := restarted.SetEnvironmentFollowing(ctx, "dev", true, state.Revision, restarted.GetWorkspaceView("dev").Route.RouteVersion); err != nil {
		t.Fatal(err)
	}
	// Closing an adopted session on the source must not resurrect the original
	// individually imported session from the pre-follow backup.
	if err := restarted.EditFollowedRoute(ctx, imported.WorkspaceID, "", restarted.GetWorkspaceView(imported.WorkspaceID).Route.RouteVersion, fleet.ConfigurationEdit{Action: "delete-environment"}); err != nil {
		t.Fatal(err)
	}
	if restarted.GetWorkspaceView(imported.WorkspaceID) != nil || source.GetWorkspaceView(feature.WorkspaceID) != nil {
		t.Fatal("closing a source session resurrected an imported session")
	}
	state, _ = restarted.ComputerFollowing()
	if err := restarted.StopFollowingComputer(state.Revision); err != nil {
		t.Fatal(err)
	}
	if restarted.GetWorkspaceView(local.WorkspaceID) == nil || !sameSessionBaseline(restarted.GetWorkspaceView(local.WorkspaceID).Session, pinned) {
		t.Fatal("stop following lost independent session")
	}
}

func TestComputerFollowingRejectsSessionHostnameCollisionAtomically(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	receiver, owner := inheritanceManagers(t)
	base := &models.Workspace{WorkspaceID: "dev", Type: models.WorkspaceTypeRouting, RoutingConfig: &models.RoutingConfig{LocalDomain: "dev.localhost", DefaultRemoteBaseURL: "https://example.com"}}
	base.SetDefaults()
	source := &Registry{store: &fakeWorkspaceStore{}, routingProvider: &mockRoutingProvider{}, workspaces: []*models.Workspace{base}}
	if err := source.SetPeerWorkspaceClient(owner.State().Fingerprint, owner); err != nil {
		t.Fatal(err)
	}
	owner.SetWorkspaceSource(source)
	if _, err := source.CreateRoutingSession("dev", "collision", ""); err != nil {
		t.Fatal(err)
	}
	r := &Registry{store: &memoryComputerStore{}, routingProvider: &mockRoutingProvider{}, workspaces: []*models.Workspace{cloneWorkspace(base)}}
	if err := r.SetPeerWorkspaceClient(receiver.State().Fingerprint, receiver); err != nil {
		t.Fatal(err)
	}
	local, err := r.CreateRoutingSession("dev", "collision", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.FollowComputer(context.Background(), owner.State().Fingerprint, 0); err == nil {
		t.Fatal("following overwrote a local session hostname")
	}
	state, _ := r.ComputerFollowing()
	if state.OwnerFingerprint != "" || r.GetWorkspaceView(local.WorkspaceID).Inheritance != nil {
		t.Fatal("collision partially committed following")
	}
	// Existing configuration snapshots without sessions remain valid.
	intent, version, err := source.ExportPeerWorkspace("dev")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := fleet.NewSnapshot(owner.State().Fingerprint, version, intent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.NewConfiguration(owner.State().Fingerprint, nil, []fleet.ConfigurationEnvironment{{Snapshot: snapshot}}); err != nil {
		t.Fatal(err)
	}
}

func sameSessionBaseline(a, b *models.RoutingSession) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}
