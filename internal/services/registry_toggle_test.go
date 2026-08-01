package services

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/thiagojdb/rementor/internal/models"
)

type mockRoutingProvider struct {
	lastWorkspaces []*models.Workspace
	snapshots      [][]bool
	err            error
}

func (m *mockRoutingProvider) LoadInitialConfig(workspaces []*models.Workspace) error {
	m.lastWorkspaces = cloneWorkspaces(workspaces)
	snapshot := make([]bool, 0)
	for _, ws := range workspaces {
		for _, app := range ws.Applications {
			snapshot = append(snapshot, app.Active)
		}
	}
	m.snapshots = append(m.snapshots, snapshot)
	return m.err
}

type fakeWorkspaceStore struct {
	workspaces       []*models.Workspace
	loadErr          error
	saveErr          error
	updateErr        error
	removeErr        error
	replaceErr       error
	saveCalls        int
	updateCalls      int
	removeCalls      int
	appendCalls      int
	replaceCalls     int
	lastUpdateApps   []models.ApplicationConfig
	lastUpdateDomain string
	lastUpdateRemote string
}

func (s *fakeWorkspaceStore) LoadWorkspaces() ([]*models.Workspace, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	return s.workspaces, nil
}

func (s *fakeWorkspaceStore) LoadState([]*models.Workspace) error {
	return nil
}

func (s *fakeWorkspaceStore) SaveState([]*models.Workspace) error {
	s.saveCalls++
	return s.saveErr
}

func (s *fakeWorkspaceStore) ReplaceWorkspaces(workspaces []*models.Workspace) error {
	s.replaceCalls++
	if s.replaceErr != nil {
		return s.replaceErr
	}
	s.workspaces = cloneWorkspaces(workspaces)
	return nil
}

func (s *fakeWorkspaceStore) AppendWorkspace(ws models.WorkspaceConfig) error {
	s.appendCalls++
	s.workspaces = append(s.workspaces, s.WorkspaceFromConfig(ws))
	return nil
}

func (s *fakeWorkspaceStore) WorkspaceFromConfig(ws models.WorkspaceConfig) *models.Workspace {
	name := ws.Name
	color := ws.Color
	out := &models.Workspace{
		WorkspaceID:  ws.ID,
		Type:         ws.Type,
		Name:         &name,
		Color:        &color,
		Applications: make([]*models.Application, 0, len(ws.Applications)),
	}
	if ws.Type != models.WorkspaceTypeLocalApps {
		out.RoutingConfig = &models.RoutingConfig{
			Mode:                 ws.Routing.Mode,
			LocalDomain:          ws.Routing.LocalDomain,
			DefaultRemoteBaseURL: ws.Routing.DefaultRemoteBaseURL,
		}
	}
	for _, app := range ws.Applications {
		out.Applications = append(out.Applications, &models.Application{
			ID:            app.ID,
			Name:          app.Name,
			Path:          app.Path,
			Domain:        app.Domain,
			RemoteBaseUrl: app.RemoteBaseUrl,
			Context:       app.Context,
			Health:        app.Health,
			Port:          app.Port,
			Active:        app.Active,
			RoutePattern:  app.RoutePattern,
			StripOrigin:   app.StripOrigin,
		})
	}
	out.SetDefaults()
	return out
}

func (s *fakeWorkspaceStore) UpdateWorkspaceApplications(wsID string, apps []models.ApplicationConfig, localDomain, defaultRemoteBaseURL string) error {
	s.updateCalls++
	s.lastUpdateApps = append([]models.ApplicationConfig(nil), apps...)
	s.lastUpdateDomain = localDomain
	s.lastUpdateRemote = defaultRemoteBaseURL
	if s.updateErr != nil {
		return s.updateErr
	}
	for _, ws := range s.workspaces {
		if ws.WorkspaceID != wsID {
			continue
		}
		ws.Applications = make([]*models.Application, 0, len(apps))
		for _, app := range apps {
			ws.Applications = append(ws.Applications, &models.Application{
				ID:            app.ID,
				Name:          app.Name,
				Path:          app.Path,
				Domain:        app.Domain,
				RemoteBaseUrl: app.RemoteBaseUrl,
				Context:       app.Context,
				Health:        app.Health,
				Port:          app.Port,
				Active:        app.Active,
				RoutePattern:  app.RoutePattern,
				StripOrigin:   app.StripOrigin,
			})
		}
		ws.RoutingConfig = &models.RoutingConfig{
			LocalDomain:          localDomain,
			DefaultRemoteBaseURL: defaultRemoteBaseURL,
		}
		ws.SetDefaults()
	}
	return nil
}

func (s *fakeWorkspaceStore) RemoveWorkspace(wsID string) error {
	s.removeCalls++
	if s.removeErr != nil {
		return s.removeErr
	}
	for i, ws := range s.workspaces {
		if ws.WorkspaceID == wsID {
			s.workspaces = append(s.workspaces[:i], s.workspaces[i+1:]...)
			break
		}
	}
	return nil
}

func TestToggleAppRollsBackWhenRoutingReloadFails(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	ws := &models.Workspace{
		WorkspaceID: "demo",
		Type:        models.WorkspaceTypeRouting,
		RoutingConfig: &models.RoutingConfig{
			LocalDomain:          "api.localhost",
			DefaultRemoteBaseURL: "https://remote.example.test",
		},
		Applications: []*models.Application{{
			ID: "orders-api", Path: "/orders", Port: 8081, Active: false,
		}},
	}
	r := &Registry{
		workspaces:      []*models.Workspace{ws},
		store:           &fakeWorkspaceStore{},
		routingProvider: &mockRoutingProvider{err: errors.New("nginx reload failed")},
		stopChan:        make(chan struct{}),
		subscribers:     make(map[string]int),
		healthStreams:   make(map[uint64]healthStream),
	}

	if _, err := r.ToggleApp("demo", "orders-api"); err == nil {
		t.Fatal("expected toggle to fail")
	}
	if ws.Applications[0].Active {
		t.Fatal("expected failed toggle to restore remote state")
	}
}

func TestFindAppResolvesCanonicalIDsAndAliases(t *testing.T) {
	ws := &models.Workspace{
		WorkspaceID: "demo", Type: models.WorkspaceTypeRouting,
		RoutingConfig: &models.RoutingConfig{LocalDomain: "api.localhost"},
		Applications:  []*models.Application{{ID: "rtc", AppID: "rtc", Aliases: []string{"front-giss-v2"}, Path: "/rtc"}},
	}
	r := &Registry{workspaces: []*models.Workspace{ws}}
	_, app, err := r.FindApp("demo", " FRONT_GISS_V2 ")
	if err != nil {
		t.Fatalf("FindApp alias failed: %v", err)
	}
	if app.CanonicalAppID() != "rtc" {
		t.Fatalf("resolved app = %q, want rtc", app.CanonicalAppID())
	}
}

func TestRegisterApplicationAliasRejectsConflicts(t *testing.T) {
	ws := &models.Workspace{
		WorkspaceID: "demo", Type: models.WorkspaceTypeRouting,
		RoutingConfig: &models.RoutingConfig{LocalDomain: "api.localhost"},
		Applications: []*models.Application{
			{ID: "rtc", AppID: "rtc", Path: "/rtc"},
			{ID: "billing", AppID: "billing", Path: "/billing", Aliases: []string{"billing-ui"}},
		},
	}
	r := &Registry{
		workspaces: []*models.Workspace{ws}, store: &fakeWorkspaceStore{}, routingProvider: &mockRoutingProvider{},
		stopChan: make(chan struct{}), subscribers: make(map[string]int), healthStreams: make(map[uint64]healthStream),
	}
	if _, err := r.RegisterApplicationAlias("demo", "rtc", "billing-ui"); !errors.Is(err, models.ErrAliasConflict) {
		t.Fatalf("expected alias conflict, got %v", err)
	}
}

func TestToggleAppRollsBackWhenStateSaveFails(t *testing.T) {
	ws := &models.Workspace{
		WorkspaceID: "demo",
		Type:        models.WorkspaceTypeRouting,
		RoutingConfig: &models.RoutingConfig{
			LocalDomain:          "api.localhost",
			DefaultRemoteBaseURL: "https://remote.example.test",
		},
		Applications: []*models.Application{{
			ID: "orders-api", Path: "/orders", Port: 8081, Active: false,
		}},
	}
	store := &fakeWorkspaceStore{saveErr: errors.New("sqlite write failed")}
	proxy := &mockRoutingProvider{}
	r := &Registry{
		workspaces:      []*models.Workspace{ws},
		store:           store,
		routingProvider: proxy,
		stopChan:        make(chan struct{}),
		subscribers:     make(map[string]int),
		healthStreams:   make(map[uint64]healthStream),
	}

	if _, err := r.ToggleApp("demo", "orders-api"); err == nil {
		t.Fatal("expected toggle to fail")
	}
	if ws.Applications[0].Active {
		t.Fatal("expected failed save to restore remote state")
	}
	if store.saveCalls != 1 {
		t.Fatalf("expected one state save attempt, got %d", store.saveCalls)
	}
	if got := len(proxy.snapshots); got != 2 {
		t.Fatalf("expected initial reload and rollback reload, got %d", got)
	}
	if !proxy.snapshots[0][0] {
		t.Fatal("expected first proxy reload to receive local active state")
	}
	if proxy.snapshots[1][0] {
		t.Fatal("expected rollback proxy reload to receive remote state")
	}
}

func TestPublishHealthFansOutAndFiltersWorkspaces(t *testing.T) {
	r := &Registry{healthStreams: make(map[uint64]healthStream)}
	firstID, first := r.SubscribeHealth("demo")
	defer r.UnsubscribeHealth(firstID)
	secondID, second := r.SubscribeHealth("demo")
	defer r.UnsubscribeHealth(secondID)
	otherID, other := r.SubscribeHealth("other")
	defer r.UnsubscribeHealth(otherID)

	update := models.HealthUpdate{WsID: "demo", AppName: "orders-api"}
	r.publishHealth(update)

	for name, stream := range map[string]<-chan models.HealthUpdate{"first": first, "second": second} {
		select {
		case got := <-stream:
			if got.AppName != update.AppName {
				t.Fatalf("%s stream received wrong update: %#v", name, got)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s stream did not receive update", name)
		}
	}
	select {
	case got := <-other:
		t.Fatalf("other workspace received update: %#v", got)
	default:
	}
}

func (m *mockRoutingProvider) IsAvailable() bool { return true }
func (m *mockRoutingProvider) Close() error      { return nil }

func TestToggleAppPassesCorrectActiveState(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	ws := &models.Workspace{
		WorkspaceID: "dev",
		Type:        "routing",
		RoutingConfig: &models.RoutingConfig{
			LocalDomain:          "api.localhost",
			DefaultRemoteBaseURL: "https://remote.example.test",
		},
		Applications: []*models.Application{
			{
				ID:            "web-frontend",
				Path:          "/",
				RemoteBaseUrl: "https://remote.example.test",
				Context:       "/portal",
				Port:          9311,
				Active:        false,
			},
		},
	}

	mock := &mockRoutingProvider{}
	store := &fakeWorkspaceStore{}
	r := &Registry{
		workspaces:      []*models.Workspace{ws},
		store:           store,
		routingProvider: mock,
		stopChan:        make(chan struct{}),
		subscribers:     make(map[string]int),
		healthStreams:   make(map[uint64]healthStream),
	}

	// Toggle to local (Active=false → true)
	app, err := r.ToggleApp("dev", "web-frontend")
	if err != nil {
		t.Fatalf("ToggleApp failed: %v", err)
	}
	if !app.Active {
		t.Errorf("Expected app.Active=true after toggle, got false")
	}

	if mock.lastWorkspaces == nil {
		t.Fatal("LoadInitialConfig was not called")
	}

	foundApp := mock.lastWorkspaces[0].Applications[0]
	t.Logf("After toggle to local: app.Active=%v", foundApp.Active)
	if !foundApp.Active {
		t.Errorf("LoadInitialConfig received app.Active=false, expected true")
	}

	// Toggle back to remote (Active=true → false)
	app, err = r.ToggleApp("dev", "web-frontend")
	if err != nil {
		t.Fatalf("ToggleApp failed: %v", err)
	}
	if app.Active {
		t.Errorf("Expected app.Active=false after second toggle, got true")
	}

	foundApp = mock.lastWorkspaces[0].Applications[0]
	t.Logf("After toggle to remote: app.Active=%v", foundApp.Active)
	if foundApp.Active {
		t.Errorf("LoadInitialConfig received app.Active=true, expected false")
	}
	if store.saveCalls != 2 {
		t.Fatalf("expected two state saves, got %d", store.saveCalls)
	}
}

func TestUpdateWorkspaceApplicationsRollsBackProjectionWhenRoutingReloadFails(t *testing.T) {
	old := &models.Workspace{
		WorkspaceID: "demo",
		Type:        models.WorkspaceTypeRouting,
		RoutingConfig: &models.RoutingConfig{
			LocalDomain:          "api.localhost",
			DefaultRemoteBaseURL: "https://remote.example.test",
		},
		Applications: []*models.Application{{
			ID: "orders-api", Path: "/orders", Port: 8081, Active: false,
		}},
	}
	loaded := &models.Workspace{
		WorkspaceID: "demo",
		Type:        models.WorkspaceTypeRouting,
		RoutingConfig: &models.RoutingConfig{
			LocalDomain:          "api.localhost",
			DefaultRemoteBaseURL: "https://new.example.test",
		},
		Applications: []*models.Application{{
			ID: "orders-api", Path: "/new-orders", Port: 8082, Active: false,
		}},
	}
	store := &fakeWorkspaceStore{workspaces: []*models.Workspace{loaded}}
	r := &Registry{
		workspaces:      []*models.Workspace{old},
		store:           store,
		routingProvider: &mockRoutingProvider{err: errors.New("nginx reload failed")},
		stopChan:        make(chan struct{}),
		subscribers:     make(map[string]int),
		healthStreams:   make(map[uint64]healthStream),
	}

	err := r.UpdateWorkspaceApplications("demo", []models.ApplicationConfig{{
		ID: "orders-api", Path: "/new-orders", Port: 8082,
	}}, "api.localhost", "https://new.example.test")
	if err == nil {
		t.Fatal("expected update to fail")
	}
	if got := r.FindWorkspace("demo"); got != old {
		t.Fatal("expected failed routing reload to restore old in-memory workspace")
	}
	if store.replaceCalls != 0 {
		t.Fatalf("expected routing failure to skip persistence, got %d replacements", store.replaceCalls)
	}
}

func TestUpdateWorkspaceApplicationsRestoresRoutingWhenPersistenceFails(t *testing.T) {
	old := &models.Workspace{
		WorkspaceID: "demo",
		Type:        models.WorkspaceTypeRouting,
		RoutingConfig: &models.RoutingConfig{
			LocalDomain:          "api.localhost",
			DefaultRemoteBaseURL: "https://remote.example.test",
		},
		Applications: []*models.Application{{
			ID: "orders-api", Path: "/orders", Port: 8081, Active: false,
		}},
	}
	store := &fakeWorkspaceStore{replaceErr: errors.New("sqlite write failed")}
	proxy := &mockRoutingProvider{}
	r := &Registry{
		workspaces:      []*models.Workspace{old},
		store:           store,
		routingProvider: proxy,
		stopChan:        make(chan struct{}),
		subscribers:     make(map[string]int),
		healthStreams:   make(map[uint64]healthStream),
	}

	err := r.UpdateWorkspaceApplications("demo", []models.ApplicationConfig{{
		ID: "orders-api", Path: "/new-orders", Port: 8082,
	}}, "api.localhost", "https://new.example.test")
	if err == nil {
		t.Fatal("expected update to fail")
	}
	if got := r.FindWorkspace("demo"); got != old {
		t.Fatal("expected failed persistence to leave old in-memory workspace")
	}
	if store.replaceCalls != 1 {
		t.Fatalf("expected one persistence attempt, got %d", store.replaceCalls)
	}
	if got := len(proxy.snapshots); got != 2 {
		t.Fatalf("expected candidate apply plus routing restore, got %d proxy calls", got)
	}
	if got := proxy.lastWorkspaces[0].Applications[0].Path; got != "/orders" {
		t.Fatalf("expected proxy rollback to restore old route, got %q", got)
	}
}

func TestCreateWorkspaceDoesNotPersistWhenRoutingFails(t *testing.T) {
	store := &fakeWorkspaceStore{}
	r := &Registry{
		store:           store,
		routingProvider: &mockRoutingProvider{err: errors.New("nginx reload failed")},
		stopChan:        make(chan struct{}),
		subscribers:     make(map[string]int),
		healthStreams:   make(map[uint64]healthStream),
	}

	_, err := r.CreateWorkspace(models.WorkspaceConfig{
		ID: "demo", Type: models.WorkspaceTypeRouting,
		Routing: models.RoutingConfig{LocalDomain: "api.localhost"},
	})
	if err == nil {
		t.Fatal("expected workspace creation to fail")
	}
	if store.replaceCalls != 0 {
		t.Fatalf("expected routing failure to skip persistence, got %d replacements", store.replaceCalls)
	}
	if r.FindWorkspace("demo") != nil {
		t.Fatal("expected failed creation to leave runtime unchanged")
	}
}

func TestDeleteWorkspaceRestoresRoutingWhenPersistenceFails(t *testing.T) {
	workspace := &models.Workspace{
		WorkspaceID: "demo",
		Type:        models.WorkspaceTypeRouting,
		RoutingConfig: &models.RoutingConfig{
			LocalDomain: "api.localhost",
		},
	}
	store := &fakeWorkspaceStore{replaceErr: errors.New("sqlite write failed")}
	proxy := &mockRoutingProvider{}
	r := &Registry{
		workspaces:      []*models.Workspace{workspace},
		store:           store,
		routingProvider: proxy,
		stopChan:        make(chan struct{}),
		subscribers:     map[string]int{"demo": 1},
		healthStreams:   make(map[uint64]healthStream),
	}

	if err := r.DeleteWorkspace("demo"); err == nil {
		t.Fatal("expected workspace deletion to fail")
	}
	if got := r.FindWorkspace("demo"); got != workspace {
		t.Fatal("expected failed deletion to leave runtime unchanged")
	}
	if store.replaceCalls != 1 {
		t.Fatalf("expected one persistence attempt, got %d", store.replaceCalls)
	}
	if got := len(proxy.snapshots); got != 2 {
		t.Fatalf("expected candidate apply plus routing restore, got %d proxy calls", got)
	}
	if r.subscribers["demo"] != 1 {
		t.Fatal("expected failed deletion to preserve subscribers")
	}
}
