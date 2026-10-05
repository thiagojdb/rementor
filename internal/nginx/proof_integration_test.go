package nginx

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/thiagojdb/rementor/internal/models"
)

func TestGeneratedProxyProofHeadersAcrossModesAndErrors(t *testing.T) {
	nginxPath, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("nginx binary not available")
	}

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/billing/in-flight" {
			entered <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("X-Rementor-App-ID", "upstream-spoof")
		w.Header().Set("X-Rementor-Operation-ID", "upstream-spoof")
		w.Header().Set("X-Request-ID", "upstream-spoof")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "upstream failure")
	}))
	defer upstream.Close()
	defer close(release)

	port := proofFreeTCPPort(t)
	localPort := proofFreeTCPPort(t)
	t.Setenv("REMENTOR_NGINX_LISTEN_HOST", "127.0.0.1")
	t.Setenv("REMENTOR_NGINX_LISTEN_PORTS", fmt.Sprintf("%d", port))
	workspace := &models.Workspace{
		WorkspaceID: "dev",
		Type:        models.WorkspaceTypeRouting,
		RoutingConfig: &models.RoutingConfig{
			LocalDomain:          "api.localhost",
			DefaultRemoteBaseURL: upstream.URL,
		},
		Applications: []*models.Application{{
			ID:            "orders-api",
			AppID:         "orders-api",
			ServiceID:     "orders",
			Path:          "/orders",
			Context:       "/orders",
			RemoteBaseUrl: upstream.URL,
			Port:          localPort,
			Active:        true,
		}, {
			ID:            "billing-api",
			AppID:         "billing-api",
			ServiceID:     "billing",
			Path:          "/billing",
			Context:       "/billing",
			RemoteBaseUrl: upstream.URL,
		}},
	}
	// Enough identical remote targets to exercise compact regex locations and
	// their per-URI proof map, alongside an explicit local location.
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("remote-%d", i)
		workspace.Applications = append(workspace.Applications, &models.Application{
			ID: id, AppID: id, ServiceID: id, Path: "/" + id, Context: "/" + id, RemoteBaseUrl: upstream.URL,
		})
	}
	conf, err := RenderConfig([]*models.Workspace{workspace}, "rementor.localhost")
	if err != nil {
		t.Fatalf("render config: %v", err)
	}

	dir := t.TempDir()
	routesPath := filepath.Join(dir, workspacesConfigFile)
	if err := os.WriteFile(routesPath, []byte(conf), 0o644); err != nil {
		t.Fatalf("write routes: %v", err)
	}
	mainPath := filepath.Join(dir, "nginx.conf")
	main := fmt.Sprintf("pid %s;\nerror_log %s;\nevents {}\nhttp {\n    access_log off;\n    underscores_in_headers on;\n    include %s;\n}\n", filepath.Join(dir, "nginx.pid"), filepath.Join(dir, "error.log"), routesPath)
	if err := os.WriteFile(mainPath, []byte(main), 0o644); err != nil {
		t.Fatalf("write main config: %v", err)
	}

	cmd := exec.Command(nginxPath, "-p", dir, "-c", mainPath, "-g", "daemon off;")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("start nginx: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
	})

	client := &http.Client{Timeout: 500 * time.Millisecond, Transport: &http.Transport{DisableKeepAlives: true}}
	request := func(path string) *http.Response {
		var response *http.Response
		var lastErr error
		for attempt := 0; attempt < 80; attempt++ {
			url := fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
			req, reqErr := http.NewRequest(http.MethodGet, url, nil)
			if reqErr != nil {
				t.Fatal(reqErr)
			}
			req.Host = "api.localhost"
			req.Header.Set("X-Correlation-ID", "caller-1")
			response, lastErr = client.Do(req)
			if lastErr == nil {
				return response
			}
			time.Sleep(25 * time.Millisecond)
		}
		nginxLog, _ := os.ReadFile(filepath.Join(dir, "error.log"))
		t.Fatalf("request through nginx: %v\n%s", lastErr, nginxLog)
		return nil
	}
	assertResponse := func(path, app, service, mode string) {
		response := request(path)
		defer response.Body.Close()
		if response.StatusCode != http.StatusBadGateway {
			t.Fatalf("%s response status = %d, want 502", path, response.StatusCode)
		}
		if got := response.Header.Get("X-Rementor-App-ID"); got != app {
			t.Fatalf("%s app proof = %q, want %s", path, got, app)
		}
		if got := response.Header.Get("X-Rementor-Service-ID"); got != service {
			t.Fatalf("%s service proof = %q, want %s", path, got, service)
		}
		if got := response.Header.Get("X-Rementor-Effective-Mode"); got != mode {
			t.Fatalf("%s mode proof = %q, want %s", path, got, mode)
		}
		if got := response.Header.Get("X-Rementor-Correlation-ID"); got != "caller-1" {
			t.Fatalf("%s correlation proof = %q, want caller-1", path, got)
		}
		if got := response.Header.Get("X-Request-ID"); got != "caller-1" {
			t.Fatalf("%s request ID = %q, want caller-1", path, got)
		}
		if got := response.Header.Get("X-Rementor-Operation-ID"); got == "upstream-spoof" {
			t.Fatalf("%s upstream proof header was not overwritten", path)
		}
		if got := response.Header.Get("Access-Control-Expose-Headers"); !strings.Contains(got, "X-Rementor-App-ID") {
			t.Fatalf("%s CORS expose header = %q", path, got)
		}
	}

	assertResponse("/orders", "orders-api", "orders", "local")
	assertResponse("/billing", "billing-api", "billing", "remote")
	assertResponse("/unmatched", "unknown", "unknown", "fallback")
	assertResponse("/remote-0", "remote-0", "remote-0", "remote")
	assertResponse("/remote-0/child?query=1", "remote-0", "remote-0", "remote")
	assertResponse("/remote-01", "unknown", "unknown", "fallback")

	// Exercise the provider's actual apply boundary: returning success must
	// mean a fresh connection reaches the candidate worker, even with an
	// unhealthy upstream. No control-plane server is needed for the proof.
	setTestRementorDomain(t)
	wrapper := filepath.Join(dir, "nginx-wrapper")
	script := fmt.Sprintf("#!/bin/sh\nexec %q -p %q -c %q \"$@\"\n", nginxPath, dir, mainPath)
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	provider := &RoutingProvider{confDir: dir, binary: wrapper}
	// Keep a request active on the old worker while routes change.
	inFlight := make(chan error, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/billing/in-flight", port), nil)
		req.Host = "api.localhost"
		response, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err == nil {
			response.Body.Close()
		}
		inFlight <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("in-flight request did not reach upstream")
	}
	workspace.Applications[0].Active = false
	workspace.Route = models.RouteState{RouteVersion: 8, OperationID: "op-8"}
	if err := provider.ApplyRouting([]*models.Workspace{workspace}); err != nil {
		t.Fatal(err)
	}
	assertResponse("/orders", "orders-api", "orders", "remote")
	response := request("/orders")
	response.Body.Close()
	if got := response.Header.Get("X-Rementor-Route-Version"); got != "8" {
		t.Fatalf("successful apply served stale version %q", got)
	}
	// Reapplying identical bytes must not retire the only serving generation.
	if err := provider.ApplyRouting([]*models.Workspace{workspace}); err != nil {
		t.Fatal(err)
	}
	assertResponse("/orders", "orders-api", "orders", "remote")
	release <- struct{}{}
	select {
	case err := <-inFlight:
		if err != nil {
			t.Fatalf("reload interrupted in-flight request: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("in-flight request did not drain")
	}
	// A candidate rejected by the master must leave the previous route usable.
	previous := provider.lastRendered
	invalid := configDigestPattern.ReplaceAllString(previous, `X-Rementor-Config "`+strings.Repeat("a", 64)+`"`) + "\ninvalid_directive;\n"
	if err := provider.install(invalid, true); err == nil {
		t.Fatal("invalid configuration accepted")
	} else if !strings.Contains(err.Error(), "invalid_directive") {
		t.Fatalf("invalid configuration error = %v, want nginx validation detail", err)
	}
	if got, err := os.ReadFile(routesPath); err != nil || string(got) != previous {
		t.Fatal("failed apply did not restore generated config", err)
	}
	assertResponse("/orders", "orders-api", "orders", "remote")
}

func TestBaseConfigAcceptsManySessionHostnames(t *testing.T) {
	nginxPath, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("nginx binary not available")
	}

	dir := t.TempDir()
	includeDir := filepath.Join(dir, "routes")
	if err := os.MkdirAll(includeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var routes strings.Builder
	for i := 1; i <= 60; i++ {
		fmt.Fprintf(&routes, "server { listen 127.0.0.1:18082; server_name environment-%02d.giss.localhost; return 204; }\n", i)
	}
	routes.WriteString("server { listen 127.0.0.1:18082; server_name app-1234567890.feature-xml-0123456789.localhost; return 204; }\n")
	if err := os.WriteFile(filepath.Join(includeDir, workspacesConfigFile), []byte(routes.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	main := strings.Replace(BaseConfig(includeDir), "http {\n", "http {\n    access_log off;\n", 1)
	main = fmt.Sprintf("pid %s;\nerror_log %s;\n%s", filepath.Join(dir, "nginx.pid"), filepath.Join(dir, "error.log"), main)
	mainPath := filepath.Join(dir, "nginx.conf")
	if err := os.WriteFile(mainPath, []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, err := exec.Command(nginxPath, "-p", dir, "-c", mainPath, "-t").CombinedOutput(); err != nil {
		t.Fatalf("session hostname set did not validate:\n%s", out)
	}
}

func proofFreeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate TCP port: %v", err)
	}
	defer listener.Close()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected listener address type %T", listener.Addr())
	}
	return addr.Port
}
