package services

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/thiagojdb/rementor/internal/models"
)

func sessionRegistry(t *testing.T) *Registry {
	t.Helper()
	ws := &models.Workspace{WorkspaceID: "dev", Type: "routing", RoutingConfig: &models.RoutingConfig{LocalDomain: "api.localhost", DefaultRemoteBaseURL: "http://127.0.0.1:19001"}, Applications: []*models.Application{
		{ID: "orders", PublicPath: "/orders", UpstreamContext: "/orders", Port: 19002, Active: true, Health: "health"},
		{ID: "billing", PublicPath: "/billing", UpstreamContext: "/billing", Port: 19003, Health: "health"},
	}}
	ws.SetDefaults()
	return &Registry{workspaces: []*models.Workspace{ws}, store: &fakeWorkspaceStore{}, routingProvider: &mockRoutingProvider{}, subscribers: map[string]int{}}
}
func newSession(t *testing.T, r *Registry, name string) *models.Workspace {
	t.Helper()
	ws, err := r.CreateRoutingSession("dev", name, "")
	if err != nil {
		t.Fatal(err)
	}
	return ws
}
func registerPort(t *testing.T, r *Registry, id, app string, port int) {
	t.Helper()
	_, err := r.EditSessionApplication(id, app, "", func(a *models.ApplicationConfig) error { a.Port = port; return nil }, false)
	if err != nil {
		t.Fatal(err)
	}
}
func TestSessionsIsolateRegistrationTogglesAndSharedEndpoints(t *testing.T) {
	r := sessionRegistry(t)
	x := newSession(t, r, "feature-x")
	y := newSession(t, r, "feature-y")
	if x.GetLocalDomain() == y.GetLocalDomain() || x.EnvironmentID() != "dev" {
		t.Fatal("missing isolated identity")
	}
	for _, w := range []*models.Workspace{x, y} {
		for _, a := range w.Applications {
			if a.Active || a.Port != 0 {
				t.Fatal("inherited general local routing")
			}
		}
	}
	registerPort(t, r, x.WorkspaceID, "orders", 24001)
	registerPort(t, r, y.WorkspaceID, "orders", 24002)
	a, _, err := r.ToggleAppWithMetadata(x.WorkspaceID, "orders", "")
	if err != nil || !a.Active {
		t.Fatalf("unhealthy local toggle rejected: %v", err)
	}
	_, base, _ := r.GetApplicationView("dev", "orders")
	_, other, _ := r.GetApplicationView(y.WorkspaceID, "orders")
	if base.Port != 19002 || !base.Active || other.Active || other.Port != 24002 {
		t.Fatal("session changed other scope")
	}
	registerPort(t, r, y.WorkspaceID, "orders", 24001)
	x = r.GetWorkspaceView(x.WorkspaceID)
	if len(x.Applications[0].SharedWith) != 1 {
		t.Fatal("explicit shared endpoint not visible")
	}
	url, err := r.ResolveBrowserURL(x.WorkspaceID, "orders")
	if err != nil || url.Environment != "dev" || url.PublicHost != x.GetLocalDomain() {
		t.Fatalf("session URL/proof: %+v %v", url, err)
	}
	if _, err := r.ResolveSession("other", x.WorkspaceID); err == nil {
		t.Fatal("mismatched environment accepted")
	}
	if _, err := r.ResolveSession("dev", "missing"); err == nil {
		t.Fatal("stale session silently fell back")
	}
	if err := r.DeleteWorkspace("dev"); err == nil {
		t.Fatal("deleted parent of sessions")
	}
	if err := r.DeleteWorkspace(x.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if r.GetWorkspaceView(y.WorkspaceID) == nil || r.GetWorkspaceView("dev") == nil {
		t.Fatal("close deleted unrelated scope")
	}
}
func TestSessionConcurrentRegistrationAndRollback(t *testing.T) {
	r := sessionRegistry(t)
	x := newSession(t, r, "feature-x")
	y := newSession(t, r, "feature-y")
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := x.WorkspaceID
			if i%2 == 1 {
				id = y.WorkspaceID
			}
			app := string(rune('a' + i))
			_, err := r.EditSessionApplication(id, app, "", func(a *models.ApplicationConfig) error { a.PublicPath = "/" + app; a.Port = 25000 + i; return nil }, false)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(r.GetWorkspaceView(x.WorkspaceID).Applications) != 8 || len(r.GetWorkspaceView(y.WorkspaceID).Applications) != 8 {
		t.Fatal("concurrent registration lost updates")
	}
	store := r.store.(*fakeWorkspaceStore)
	store.replaceErr = errors.New("disk failure")
	_, err := r.CreateRoutingSession("dev", "fails", "")
	if err == nil {
		t.Fatal("expected persistence failure")
	}
	if len(r.GetWorkspaces()) != 3 || len(r.routingProvider.(*mockRoutingProvider).lastWorkspaces) != 3 {
		t.Fatal("rollback lost committed sessions")
	}
	store.replaceErr = nil
	r.routingProvider.(*mockRoutingProvider).err = errors.New("nginx failure")
	if _, err := r.CreateRoutingSession("dev", "fails", ""); err == nil {
		t.Fatal("expected routing failure")
	}
	if len(r.GetWorkspaces()) != 3 {
		t.Fatal("routing failure published candidate")
	}
}
func TestSessionRefreshPinsBaselinePreservesOverridesAndRequiresFreshPreview(t *testing.T) {
	r := sessionRegistry(t)
	x := newSession(t, r, "feature-x")
	registerPort(t, r, x.WorkspaceID, "orders", 24001)
	_, err := r.EditSessionApplication(x.WorkspaceID, "orders", "", func(a *models.ApplicationConfig) error { a.Health = "custom-health"; return nil }, false)
	if err != nil {
		t.Fatal(err)
	}
	base := r.GetWorkspaceView("dev")
	apps := applicationConfigs(base.Applications)
	apps[0].UpstreamContext = "/new-orders"
	apps[0].Context = "/new-orders"
	apps[0].Health = "new-health"
	if err := r.UpdateWorkspaceApplications("dev", apps, base.GetLocalDomain(), "http://127.0.0.1:19004"); err != nil {
		t.Fatal(err)
	}
	if r.GetWorkspaceView(x.WorkspaceID).GetDefaultRemoteBaseURL() == "http://127.0.0.1:19004" {
		t.Fatal("unpinned baseline")
	}
	preview, err := r.RefreshRoutingSession(x.WorkspaceID, "", "", false)
	if err != nil || len(preview.Conflicts) > 0 {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	if preview.Workspace.Applications[0].Health != "custom-health" || preview.Workspace.Applications[0].Port != 24001 || preview.Workspace.Applications[0].UpstreamContext != "/new-orders" {
		t.Fatal("three-way merge lost overrides or inherited update")
	}
	registerPort(t, r, x.WorkspaceID, "billing", 24002)
	if _, err := r.RefreshRoutingSession(x.WorkspaceID, preview.Token, "", true); err == nil {
		t.Fatal("accepted stale preview")
	}
	preview, err = r.RefreshRoutingSession(x.WorkspaceID, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.RefreshRoutingSession(x.WorkspaceID, preview.Token, "", true); err != nil {
		t.Fatal(err)
	}
	// Removing a registered application upstream requires explicit conflict resolution.
	if err := r.UpdateWorkspaceApplications("dev", apps[1:], base.GetLocalDomain(), base.GetDefaultRemoteBaseURL()); err != nil {
		t.Fatal(err)
	}
	preview, err = r.RefreshRoutingSession(x.WorkspaceID, "", "", false)
	if err != nil || len(preview.Conflicts) == 0 {
		t.Fatalf("removed customized app not reported: %+v %v", preview, err)
	}
	if _, err := r.RefreshRoutingSession(x.WorkspaceID, preview.Token, "", true); err == nil {
		t.Fatal("applied conflicting refresh")
	}
}
func TestSessionHostnameOwnershipAndStaleEditor(t *testing.T) {
	r := sessionRegistry(t)
	x := newSession(t, r, "feature-x")
	apps := applicationConfigs(x.Applications)
	if err := r.UpdateWorkspaceApplications(x.WorkspaceID, apps, "api.localhost", x.GetDefaultRemoteBaseURL()); err == nil {
		t.Fatal("claimed general hostname")
	}
	apps[0].Domain = x.GetLocalDomain()
	if err := r.UpdateWorkspaceApplications(x.WorkspaceID, apps, x.GetLocalDomain(), x.GetDefaultRemoteBaseURL()); err == nil {
		t.Fatal("duplicated session server hostname")
	}
	version := x.Route.RouteVersion
	registerPort(t, r, x.WorkspaceID, "orders", 24001)
	if _, err := r.UpdateWorkspaceApplicationsAtVersion(x.WorkspaceID, applicationConfigs(x.Applications), x.GetLocalDomain(), x.GetDefaultRemoteBaseURL(), "", &version); err == nil {
		t.Fatal("stale editor replaced registration")
	}
}

func TestSessionRefreshRemovesInheritedPattern(t *testing.T) {
	r := sessionRegistry(t)
	pattern := "/orders/*"
	if _, err := r.UpdateRoutePattern("dev", "orders", &pattern); err != nil {
		t.Fatal(err)
	}
	x := newSession(t, r, "pattern-change")
	if _, err := r.UpdateRoutePattern("dev", "orders", nil); err != nil {
		t.Fatal(err)
	}
	preview, err := r.RefreshRoutingSession(x.WorkspaceID, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.RefreshRoutingSession(x.WorkspaceID, preview.Token, "", true); err != nil {
		t.Fatal(err)
	}
	if r.GetWorkspaceView(x.WorkspaceID).Applications[0].RoutePattern != nil {
		t.Fatal("refresh restored removed inherited pattern")
	}
}

func TestSessionRefreshAfterMetadataReloadRemovesUnmodifiedApp(t *testing.T) {
	r := sessionRegistry(t)
	x := newSession(t, r, "reload")
	data, err := json.Marshal(r.workspaceSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	var restored []*models.Workspace
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	r.workspaces = restored
	base := r.GetWorkspaceView("dev")
	if err := r.UpdateWorkspaceApplications("dev", applicationConfigs(base.Applications[1:]), base.GetLocalDomain(), base.GetDefaultRemoteBaseURL()); err != nil {
		t.Fatal(err)
	}
	preview, err := r.RefreshRoutingSession(x.WorkspaceID, "", "", false)
	if err != nil || len(preview.Conflicts) > 0 {
		t.Fatalf("internal persistence flags became false overrides: %+v %v", preview, err)
	}
	if len(preview.Workspace.Applications) != 1 {
		t.Fatal("unmodified removed app retained")
	}
}
func TestSessionKeepsInheritedHostnamePatterns(t *testing.T) {
	r := sessionRegistry(t)
	base := r.workspaces[0]
	base.RoutingConfig.LocalDomain = "desenvolvimento.giss.localhost"
	base.Applications[0].PublicPath = ""
	base.Applications[0].Path = "/old"
	base.Applications[0].UpstreamContext = ""
	base.Applications[0].Context = "/legacy"
	base.Applications[0].LegacyPublicPath = true
	base.Applications[0].LegacyUpstreamContext = true
	base.Applications[0].Domain = "dev.giss.localhost"
	x := newSession(t, r, "feature-xml")
	if x.GetLocalDomain() != "feature-xml.desenvolvimento.giss.localhost" {
		t.Fatalf("session host = %q", x.GetLocalDomain())
	}
	if x.Session.HostPrefix != "feature-xml" {
		t.Fatalf("session prefix = %q", x.Session.HostPrefix)
	}
	if x.Applications[0].IngressPath() != "/legacy" || x.Applications[0].Domain != "feature-xml.dev.giss.localhost" {
		t.Fatalf("session did not preserve inherited hosts: %+v", x.Applications[0])
	}
	url, err := r.ResolveBrowserURL(x.WorkspaceID, "orders")
	if err != nil || url.PublicHost != "feature-xml.dev.giss.localhost" {
		t.Fatalf("wrong app-domain URL: %+v %v", url, err)
	}
}

func TestSessionRefreshFollowsParentHostnameChanges(t *testing.T) {
	r := sessionRegistry(t)
	base := r.workspaces[0]
	base.RoutingConfig.LocalDomain = "desenvolvimento.giss.localhost"
	base.Applications[0].Domain = "dev.giss.localhost"
	x := newSession(t, r, "feature-xml")
	base = r.GetWorkspaceView("dev")
	apps := applicationConfigs(base.Applications)
	apps[0].Domain = "preview.giss.localhost"
	if err := r.UpdateWorkspaceApplications("dev", apps, "next.giss.localhost", base.GetDefaultRemoteBaseURL()); err != nil {
		t.Fatal(err)
	}
	preview, err := r.RefreshRoutingSession(x.WorkspaceID, "", "", false)
	if err != nil || len(preview.Conflicts) > 0 {
		t.Fatalf("refresh preview: %+v %v", preview, err)
	}
	if preview.Workspace.GetLocalDomain() != "feature-xml.next.giss.localhost" || preview.Workspace.Applications[0].Domain != "feature-xml.preview.giss.localhost" {
		t.Fatalf("refreshed hosts = %q / %q", preview.Workspace.GetLocalDomain(), preview.Workspace.Applications[0].Domain)
	}
	foundHostnameChange := false
	for _, change := range preview.Changes {
		foundHostnameChange = foundHostnameChange || change == "session hostname updated"
	}
	if !foundHostnameChange {
		t.Fatalf("hostname update was not shown in refresh preview: %#v", preview.Changes)
	}
}

func TestCreateSessionRejectsDuplicateHostnamePrefix(t *testing.T) {
	r := sessionRegistry(t)
	newSession(t, r, "Feature XML")
	if _, err := r.CreateRoutingSession("dev", "feature-xml", ""); err == nil {
		t.Fatal("created a second session with the same hostname prefix")
	}
}

func TestMigrateSessionHostnamesKeepsExplicitApplicationOverride(t *testing.T) {
	base := &models.Workspace{WorkspaceID: "desenvolvimento", Type: models.WorkspaceTypeRouting, RoutingConfig: &models.RoutingConfig{LocalDomain: "desenvolvimento.giss.localhost"}, Applications: []*models.Application{{ID: "front", AppID: "front", Domain: "dev.giss.localhost"}}}
	base.SetDefaults()
	id := "feature-xml-0123456789"
	name := "feature xml"
	legacyDomain := legacySessionAppHost("front", id)
	session := &models.Workspace{WorkspaceID: id, Type: models.WorkspaceTypeRouting, Name: &name,
		RoutingConfig: &models.RoutingConfig{LocalDomain: id + ".localhost"},
		Session:       &models.RoutingSession{EnvironmentID: base.WorkspaceID, Baseline: []models.ApplicationConfig{{ID: "front", AppID: "front", Domain: legacyDomain}}},
		Applications:  []*models.Application{{ID: "front", AppID: "front", Domain: legacyDomain}, {ID: "custom", AppID: "custom", Domain: "custom.localhost"}},
	}
	session.SetDefaults()
	if !migrateSessionHostnames([]*models.Workspace{base, session}) {
		t.Fatal("legacy session hostname was not migrated")
	}
	if session.Session.HostPrefix != "feature-xml" || session.GetLocalDomain() != "feature-xml.desenvolvimento.giss.localhost" {
		t.Fatalf("main hostname migration = %#v", session)
	}
	if session.Applications[0].Domain != "feature-xml.dev.giss.localhost" || session.Session.Baseline[0].Domain != "feature-xml.dev.giss.localhost" {
		t.Fatalf("inherited application hostname migration = %#v", session)
	}
	if session.Applications[1].Domain != "custom.localhost" {
		t.Fatal("migration replaced an explicit application hostname")
	}
}
