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

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Rementor-App-ID", "upstream-spoof")
		w.Header().Set("X-Rementor-Operation-ID", "upstream-spoof")
		w.Header().Set("X-Request-ID", "upstream-spoof")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "upstream failure")
	}))
	defer upstream.Close()

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
	conf, err := RenderConfig([]*models.Workspace{workspace}, "rementor.localhost")
	if err != nil {
		t.Fatalf("render config: %v", err)
	}

	dir := t.TempDir()
	routesPath := filepath.Join(dir, "routes.conf")
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

	client := &http.Client{Timeout: 500 * time.Millisecond}
	request := func(path string) *http.Response {
		var response *http.Response
		var lastErr error
		for attempt := 0; attempt < 30; attempt++ {
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
}

func proofFreeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate TCP port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}
