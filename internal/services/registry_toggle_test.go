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
	err            error
}

func (m *mockRoutingProvider) LoadInitialConfig(workspaces []*models.Workspace) error {
	m.lastWorkspaces = workspaces
	return m.err
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
	r := &Registry{
		workspaces:      []*models.Workspace{ws},
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
}
