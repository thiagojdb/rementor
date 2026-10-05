package services

import (
	"github.com/thiagojdb/rementor/internal/models"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFirstFailedHealthCheckPublishesKnownStatus(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer upstream.Close()
	_, portText, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	port, _ := strconv.Atoi(portText)
	app := &models.Application{ID: "orders", PublicPath: "/orders", Port: port, Health: "/health", RemoteBaseUrl: upstream.URL}
	ws := &models.Workspace{WorkspaceID: "dev", Type: models.WorkspaceTypeRouting, RoutingConfig: &models.RoutingConfig{LocalDomain: "dev.localhost", DefaultRemoteBaseURL: upstream.URL}, Applications: []*models.Application{app}}
	r := &Registry{workspaces: []*models.Workspace{ws}, subscribers: map[string]int{"dev": 1}, healthStreams: make(map[uint64]healthStream)}
	id, updates := r.SubscribeHealth("dev")
	defer r.UnsubscribeHealth(id)
	r.refreshAllHealth()
	deadline := time.Now().Add(2 * time.Second)
	for app.Runtime.GetHealthLast() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if app.Runtime.GetHealthLast() == nil {
		t.Fatal("probe did not finish")
	}
	select {
	case got := <-updates:
		if got.LocalOk || got.RemoteOk || got.LocalChecked.IsZero() || got.RemoteChecked.IsZero() {
			t.Fatal("failed probe must publish checked unhealthy status", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first failed health check left subscribed UI status unknown")
	}
}

func TestHealthResultSurvivesUnrelatedRoutingMutation(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		<-release
		w.WriteHeader(200)
	}))
	defer upstream.Close()
	_, portText, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	port, _ := strconv.Atoi(portText)
	ws := &models.Workspace{WorkspaceID: "dev", Type: models.WorkspaceTypeRouting, RoutingConfig: &models.RoutingConfig{LocalDomain: "dev.localhost", DefaultRemoteBaseURL: upstream.URL}, Applications: []*models.Application{{ID: "orders", PublicPath: "/orders", Port: port, Health: "health", RemoteBaseUrl: upstream.URL}}}
	r := &Registry{workspaces: []*models.Workspace{ws}, store: &fakeWorkspaceStore{}, routingProvider: &mockRoutingProvider{}, subscribers: map[string]int{"dev": 1}, healthStreams: make(map[uint64]healthStream)}
	done := make(chan struct{})
	go func() { r.refreshAllHealth(); close(done) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("probe did not start")
	}
	if _, _, err := r.UpdateRoutePatternWithMetadata("dev", "orders", stringPtrForTest("/orders/*"), ""); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	<-done
	deadline := time.Now().Add(2 * time.Second)
	for r.GetWorkspaceView("dev").Applications[0].Runtime.GetHealthLast() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	app := r.GetWorkspaceView("dev").Applications[0]
	if app.Runtime.GetHealthLast() == nil || !app.Runtime.GetHealthOk() {
		t.Fatal("completed health result was lost when routing replaced the workspace projection")
	}
}
