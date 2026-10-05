package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/thiagojdb/rementor/internal/config"
	"github.com/thiagojdb/rementor/internal/gen/rementor/v1/rementorv1connect"
	"github.com/thiagojdb/rementor/internal/nginx"
	"github.com/thiagojdb/rementor/internal/rpc"
	"github.com/thiagojdb/rementor/internal/services"
)

func TestRoutingSessionsThroughRPCAndRealNginx(t *testing.T) {
	binary, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("nginx not installed")
	}
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	t.Setenv("REMENTOR_NGINX_LISTEN_HOST", "127.0.0.1")
	t.Setenv("REMENTOR_NGINX_LISTEN_PORTS", strconv.Itoa(port))
	t.Setenv("REMENTOR_PUBLIC_PORT", strconv.Itoa(port))
	if err := config.Load(); err != nil {
		t.Fatal(err)
	}
	confDir := config.Config.NginxConfDir
	main := filepath.Join(dir, "nginx.conf")
	body := fmt.Sprintf("pid %s; error_log %s; events {} http { access_log off; include %s/*.conf; }", filepath.Join(dir, "nginx.pid"), filepath.Join(dir, "error.log"), confDir)
	if err := os.WriteFile(main, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-p", dir, "-c", main, "-g", "daemon off;")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Signal(syscall.SIGTERM); _ = cmd.Wait() })
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(filepath.Join(dir, "nginx.pid")); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	wrapper := filepath.Join(dir, "nginx-wrapper")
	if err := os.WriteFile(wrapper, []byte(fmt.Sprintf("#!/bin/sh\nexec '%s' -p '%s' -c '%s' \"$@\"\n", binary, dir, main)), 0700); err != nil {
		t.Fatal(err)
	}
	provider := nginx.NewRoutingProvider(confDir, wrapper)
	registry := services.GetRegistry()
	registry.SetRoutingProvider(provider)
	if err := registry.Load(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Stop)
	path, handler := rementorv1connect.NewControlPlaneServiceHandler(rpc.NewControlPlaneService(registry))
	mux := http.NewServeMux()
	mux.Handle("/rpc"+path, http.StripPrefix("/rpc", handler))
	server := httptest.NewServer(mux)
	defer server.Close()
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "remote:"+r.URL.Path) }))
	defer remote.Close()
	local := func(label string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Rementor-Session-ID", "spoof")
			fmt.Fprint(w, label+":"+r.URL.Path)
		}))
	}
	lx, ly := local("feature-x"), local("feature-y")
	defer lx.Close()
	defer ly.Close()
	localPort := func(s *httptest.Server) int { return s.Listener.Addr().(*net.TCPAddr).Port }
	ctx := context.Background()
	base := NewClient(server.URL)
	_, err = base.CreateWorkspace(ctx, CreateWorkspaceRequest{ID: "dev", Type: "routing", LocalDomain: "desenvolvimento.giss.localhost", DefaultRemoteBaseURL: remote.URL, Applications: []ApplicationConfigInput{
		{ID: "orders", Path: "/orders", PublicPath: "/orders", Context: "/orders", UpstreamContext: "/orders", Port: localPort(lx), Health: "health"},
		{ID: "portal", Path: "/", PublicPath: "/", Domain: "dev.giss.localhost", RemoteBaseUrl: remote.URL, Context: "/", UpstreamContext: "/", Health: "health"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := base.ToggleApplication(ctx, "dev", "orders"); err != nil {
		t.Fatal(err)
	}
	x, err := base.CreateRoutingSession(ctx, "dev", "feature-x")
	if err != nil {
		t.Fatal(err)
	}
	y, err := base.CreateRoutingSession(ctx, "dev", "feature-y")
	if err != nil {
		t.Fatal(err)
	}
	if x.Routing.LocalDomain != "feature-x.desenvolvimento.giss.localhost" || y.Routing.LocalDomain != "feature-y.desenvolvimento.giss.localhost" {
		t.Fatalf("session hosts = %q / %q", x.Routing.LocalDomain, y.Routing.LocalDomain)
	}
	portalHost := func(workspace WorkspaceDTO) string {
		t.Helper()
		for _, app := range workspace.Applications {
			if app.ID == "portal" {
				return app.Domain
			}
		}
		t.Fatal("missing portal application")
		return ""
	}
	if portalHost(x) != "feature-x.dev.giss.localhost" || portalHost(y) != "feature-y.dev.giss.localhost" {
		t.Fatalf("session application hosts = %q / %q", portalHost(x), portalHost(y))
	}
	cx, cy := NewClient(server.URL), NewClient(server.URL)
	cx.SessionID = x.ID
	cy.SessionID = y.ID
	for _, run := range []struct {
		client *Client
		port   int
	}{{cx, localPort(lx)}, {cy, localPort(ly)}} {
		result, err := run.client.UpsertApplication(ctx, "dev", ApplicationConfigInput{ID: "orders", Port: run.port})
		if err != nil {
			t.Fatal(err)
		}
		if result.Application.Active || result.Application.PublicPath != "/orders" {
			t.Fatal("registration activated or lost inherited route")
		}
		plan, err := run.client.PlanRoute(ctx, PlanRouteRequest{WorkspaceID: "dev", ApplicationRef: "orders", DesiredMode: "local"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := run.client.ApplyRoute(ctx, ApplyRouteRequest{WorkspaceID: "dev", ApplicationRef: "orders", Plan: &plan, DesiredMode: "local", IdempotencyKey: "same-key"}); err != nil {
			t.Fatal(err)
		}
	}
	request := func(host, path, want, session string) {
		t.Helper()
		req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
		req.Host = host
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 || string(data) != want {
			t.Fatalf("%s%s: status %d body %s want %s", host, path, res.StatusCode, data, want)
		}
		if res.Header.Get("X-Rementor-Environment") != "dev" || res.Header.Get("X-Rementor-Session-ID") != session {
			t.Fatalf("wrong routing proof: %v", res.Header)
		}
	}
	request(x.Routing.LocalDomain, "/orders", "feature-x:/orders", x.ID)
	request(y.Routing.LocalDomain, "/orders", "feature-y:/orders", y.ID)
	request("desenvolvimento.giss.localhost", "/orders", "feature-x:/orders", "")
	request(x.Routing.LocalDomain, "/billing", "remote:/billing", x.ID)
	request(portalHost(x), "/", "remote:/", x.ID)
	request(portalHost(y), "/", "remote:/", y.ID)
	url, err := cx.ResolveBrowserURL(ctx, "dev", "orders")
	if err != nil || !strings.Contains(url.URL, x.Routing.LocalDomain+":"+strconv.Itoa(port)) {
		t.Fatalf("session URL: %+v %v", url, err)
	}
	if _, err := cx.GetWorkspace(ctx, "wrong-environment"); err == nil {
		t.Fatal("mismatched selector accepted")
	}
	// MCP's session selector uses the same registered endpoint without switching defaults.
	mcp := &mcpServer{client: base, serverURL: server.URL}
	args, _ := json.Marshal(map[string]any{"name": "rementor.app_get", "arguments": map[string]any{"workspace": "dev", "session": x.ID, "app": "orders"}})
	if _, err := mcp.handleToolCall(args); err != nil {
		t.Fatal(err)
	}
	if base.SessionID != "" {
		t.Fatal("MCP modified another client's context")
	}

	// A stopped selected target must fail locally rather than silently use remote.
	lx.Close()
	unavailableReq, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/orders", port), nil)
	unavailableReq.Host = x.Routing.LocalDomain
	unavailable, err := http.DefaultClient.Do(unavailableReq)
	if err != nil {
		t.Fatal(err)
	}
	unavailable.Body.Close()
	if unavailable.StatusCode != http.StatusBadGateway || unavailable.Header.Get("X-Rementor-Effective-Mode") != "local" {
		t.Fatalf("unavailable local target fell back: %d %v", unavailable.StatusCode, unavailable.Header)
	}
	if _, err := base.DeleteWorkspaceWithMetadata(ctx, x.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := cx.ToggleApplication(ctx, "dev", "orders"); err == nil {
		t.Fatal("closed selector mutated base")
	}
	request(y.Routing.LocalDomain, "/orders", "feature-y:/orders", y.ID)
	loaded, err := config.LoadWorkspaces()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ws := range loaded {
		if ws.WorkspaceID == y.ID {
			found = true
			if ws.Session == nil || !ws.Applications[0].Active || ws.Applications[0].Port != localPort(ly) {
				t.Fatal("durable session state lost")
			}
		}
	}
	if !found {
		t.Fatal("session not persisted")
	}
}
