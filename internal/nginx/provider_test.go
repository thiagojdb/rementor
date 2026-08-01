package nginx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thiagojdb/rementor/internal/config"
	"github.com/thiagojdb/rementor/internal/models"
)

func TestRenderConfigActiveLocalRootApp(t *testing.T) {
	conf := renderTestConfig(t, &models.Workspace{
		WorkspaceID: "dev",
		Type:        "routing",
		RoutingConfig: &models.RoutingConfig{
			LocalDomain:          "api.localhost",
			DefaultRemoteBaseURL: "https://remote.example.test",
		},
		Applications: []*models.Application{{
			ID:            "web-frontend",
			Path:          "/",
			RemoteBaseUrl: "https://remote.example.test",
			Context:       "/portal",
			Port:          9311,
			Active:        true,
		}},
	})

	assertContains(t, conf, "server_name api.localhost;")
	assertContains(t, conf, "listen 127.0.0.1:80;")
	assertContains(t, conf, "listen [::1]:80;")
	assertNotContains(t, conf, "listen 127.0.0.1:8080;")
	assertContains(t, conf, "location = / {")
	assertContains(t, conf, "proxy_pass http://localhost:9311;")
	assertContains(t, conf, "location /portal/ {")
	assertContains(t, conf, "proxy_pass http://localhost:9311/;")
	assertNotContains(t, conf, "proxy_pass https://remote.example.test:443;")
}

func TestRenderConfigRemoteRootAppUsesContextAwareFallback(t *testing.T) {
	conf := renderTestConfig(t, &models.Workspace{
		WorkspaceID: "dev",
		Type:        "routing",
		RoutingConfig: &models.RoutingConfig{
			LocalDomain:          "api.localhost",
			DefaultRemoteBaseURL: "https://root.remote.example.test",
		},
		Applications: []*models.Application{{
			ID:            "web-frontend",
			Path:          "/",
			RemoteBaseUrl: "https://remote.example.test",
			Context:       "/portal",
			Port:          9311,
			Active:        false,
		}},
	})

	assertContains(t, conf, "location = / {")
	assertContains(t, conf, "rewrite ^ /portal/home break;")
	assertContains(t, conf, "location / {")
	assertContains(t, conf, "rewrite ^ /portal$uri break;")
	assertContains(t, conf, "proxy_pass https://remote.example.test:443;")
	assertNotContains(t, conf, "proxy_pass https://root.remote.example.test:443;")
	assertContains(t, conf, "proxy_set_header X-Forwarded-Host remote.example.test;")
	assertContains(t, conf, "proxy_ssl_server_name on;")
}

func TestRenderConfigIncludesBackendServicesUsingDefaultRemoteBaseURL(t *testing.T) {
	conf := renderTestConfig(t, &models.Workspace{
		WorkspaceID: "dev",
		Type:        "routing",
		RoutingConfig: &models.RoutingConfig{
			LocalDomain:          "api.localhost",
			DefaultRemoteBaseURL: "http://127.0.0.1:18080",
		},
		Applications: []*models.Application{
			{
				ID:            "web-frontend",
				Path:          "/",
				RemoteBaseUrl: "http://127.0.0.1:18081",
				Context:       "/portal",
				Port:          9311,
				Active:        true,
			},
			{
				ID:      "orders-api",
				Path:    "/orders",
				Context: "/service-orders",
				Port:    2444,
				Active:  false,
			},
		},
	})

	assertContains(t, conf, "location = /service-orders {")
	assertContains(t, conf, "location /service-orders/ {")
	assertContains(t, conf, "proxy_pass http://127.0.0.1:18080;")
}

func TestRenderConfigRejectsAccidentalRouteOwnershipConflict(t *testing.T) {
	_, err := RenderConfig([]*models.Workspace{{
		WorkspaceID: "dev",
		Type:        models.WorkspaceTypeRouting,
		RoutingConfig: &models.RoutingConfig{
			LocalDomain:          "api.localhost",
			DefaultRemoteBaseURL: "https://remote.example.test",
		},
		Applications: []*models.Application{
			{ID: "orders", Path: "/orders", RemoteBaseUrl: "https://orders.example.test"},
			{ID: "billing", Path: "/orders", RemoteBaseUrl: "https://billing.example.test"},
		},
	}}, "rementor.localhost")
	if err == nil || !strings.Contains(err.Error(), "route conflict") {
		t.Fatalf("RenderConfig error = %v, want an accidental route conflict", err)
	}
}

func TestRenderConfigRoutingAppDomainIncludesWorkspaceBackendRoutes(t *testing.T) {
	frontend := &models.Application{
		ID:            "web-frontend",
		Path:          "/",
		Domain:        "web.localhost",
		RemoteBaseUrl: "https://remote.example.test",
		Context:       "/portal",
		Port:          9311,
		Active:        false,
	}
	conf := renderTestConfig(t, &models.Workspace{
		WorkspaceID: "dev",
		Type:        "routing",
		RoutingConfig: &models.RoutingConfig{
			LocalDomain:          "api.localhost",
			DefaultRemoteBaseURL: "http://127.0.0.1:18080",
		},
		Applications: []*models.Application{
			frontend,
			{
				ID:      "customers-api",
				Path:    "/customers",
				Context: "/service-customers",
				Active:  false,
			},
		},
	})

	domainBlock := serverBlock(t, conf, "web.localhost")
	assertContains(t, domainBlock, "location /service-customers/ {")
	assertContains(t, domainBlock, "proxy_pass http://127.0.0.1:18080;")
	assertContains(t, domainBlock, "rewrite ^ /portal$uri break;")
}

func TestBaseConfigEnablesUnderscoreHeaders(t *testing.T) {
	conf := BaseConfig("/home/test/.config/rementor/nginx")
	assertContains(t, conf, "underscores_in_headers on;")
	assertContains(t, conf, "include /home/test/.config/rementor/nginx/*.conf;")
}

func TestRementorControlPlaneListensOnLocalhostPort80(t *testing.T) {
	conf := renderTestConfig(t, &models.Workspace{
		WorkspaceID: "dev",
		Type:        "routing",
		RoutingConfig: &models.RoutingConfig{
			LocalDomain:          "api.localhost",
			DefaultRemoteBaseURL: "http://127.0.0.1:18080",
		},
	})

	block := serverBlock(t, conf, "rementor.localhost")
	assertContains(t, block, "listen 127.0.0.1:80;")
	assertContains(t, block, "listen [::1]:80;")
	assertNotContains(t, block, "listen 127.0.0.1:8080;")
	assertContains(t, block, "proxy_pass http://localhost:9300;")
}

func TestRoutingDomainsListenOnLocalhostPort80(t *testing.T) {
	conf := renderTestConfig(t, &models.Workspace{
		WorkspaceID: "dev",
		Type:        "routing",
		RoutingConfig: &models.RoutingConfig{
			LocalDomain:          "api.localhost",
			DefaultRemoteBaseURL: "http://127.0.0.1:18080",
		},
	})

	block := serverBlock(t, conf, "api.localhost")
	assertContains(t, block, "listen 127.0.0.1:80;")
	assertContains(t, block, "listen [::1]:80;")
	assertNotContains(t, block, "listen 127.0.0.1:8080;")
}

func TestDockerDemoCanLimitNginxToUnprivilegedProxyPort(t *testing.T) {
	t.Setenv("REMENTOR_NGINX_LISTEN_HOST", "0.0.0.0")
	t.Setenv("REMENTOR_NGINX_LISTEN_PORTS", "8080")

	conf := renderTestConfig(t, &models.Workspace{
		WorkspaceID: "dev",
		Type:        "routing",
		RoutingConfig: &models.RoutingConfig{
			LocalDomain:          "api.localhost",
			DefaultRemoteBaseURL: "http://127.0.0.1:18080",
		},
	})

	block := serverBlock(t, conf, "api.localhost")
	assertContains(t, block, "listen 8080;")
	assertNotContains(t, block, "listen 127.0.0.1:80;")
}

func TestRenderConfigRestrictsCORSOriginsToLocalDevelopment(t *testing.T) {
	conf := renderTestConfig(t, &models.Workspace{
		WorkspaceID: "dev",
		Type:        "routing",
		RoutingConfig: &models.RoutingConfig{
			LocalDomain:          "api.localhost",
			DefaultRemoteBaseURL: "http://127.0.0.1:18080",
		},
		Applications: []*models.Application{{
			ID:            "web",
			Path:          "/",
			RemoteBaseUrl: "http://127.0.0.1:18080",
			Context:       "/",
			Active:        false,
		}},
	})

	assertContains(t, conf, `set $rementor_cors_origin "";`)
	assertContains(t, conf, `set $rementor_cors_origin $http_origin;`)
	assertContains(t, conf, `add_header Access-Control-Allow-Origin $rementor_cors_origin always;`)
	assertContains(t, conf, `add_header Vary "Origin" always;`)
	assertNotContains(t, conf, `add_header Access-Control-Allow-Origin "*" always;`)
}

func TestGeneratedConfigParsesWithNginxWhenAvailable(t *testing.T) {
	nginxPath, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("nginx binary not available")
	}

	dir := t.TempDir()
	includeDir := filepath.Join(dir, "routes")
	if err := os.MkdirAll(includeDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	workspaceConf := renderTestConfig(t, &models.Workspace{
		WorkspaceID: "dev",
		Type:        "routing",
		RoutingConfig: &models.RoutingConfig{
			LocalDomain:          "api.localhost",
			DefaultRemoteBaseURL: "http://127.0.0.1:18080",
		},
		Applications: []*models.Application{
			{
				ID:            "web-frontend",
				Path:          "/",
				RemoteBaseUrl: "http://127.0.0.1:18081",
				Context:       "/portal",
				Active:        false,
			},
			{
				ID:      "accounts-api",
				Path:    "/accounts",
				Context: "/service-accounts",
				Active:  false,
			},
		},
	})
	if err := os.WriteFile(filepath.Join(includeDir, "workspaces.conf"), []byte(workspaceConf), 0o644); err != nil {
		t.Fatalf("write workspace config failed: %v", err)
	}
	mainConf := filepath.Join(dir, "nginx.conf")
	mainConfContent := `error_log ` + filepath.Join(dir, "error.log") + `;
events {}

http {
    access_log off;
    underscores_in_headers on;
    include ` + includeDir + `/*.conf;
}
`
	if err := os.WriteFile(mainConf, []byte(mainConfContent), 0o644); err != nil {
		t.Fatalf("write main config failed: %v", err)
	}

	cmd := exec.Command(nginxPath, "-t", "-p", dir, "-c", mainConf, "-g", "pid nginx.pid;")
	if out, err := cmd.CombinedOutput(); err != nil {
		if strings.Contains(string(out), "bind() to 127.0.0.1:80 failed (13: Permission denied)") {
			t.Skipf("nginx config parses, but test user cannot bind port 80:\n%s", string(out))
		}
		t.Fatalf("nginx config did not parse: %v\n%s", err, string(out))
	}
}

func TestLoadInitialConfigRejectsNginxThatDoesNotLoadGeneratedConfig(t *testing.T) {
	confDir := filepath.Join(t.TempDir(), "routes")
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		t.Fatalf("mkdir routes: %v", err)
	}
	target := filepath.Join(confDir, workspacesConfigFile)
	previous := []byte("# previous config\n")
	if err := os.WriteFile(target, previous, 0o644); err != nil {
		t.Fatalf("write previous config: %v", err)
	}

	logPath := filepath.Join(t.TempDir(), "nginx.log")
	t.Setenv("FAKE_NGINX_LOG", logPath)
	t.Setenv("FAKE_NGINX_TARGET", filepath.Join(t.TempDir(), "different.conf"))
	provider := &RoutingProvider{confDir: confDir, binary: fakeNginx(t)}
	setTestRementorDomain(t)

	err := provider.LoadInitialConfig(nil)
	if err == nil {
		t.Fatal("expected unloaded generated config to be rejected")
	}
	assertContains(t, err.Error(), "nginx does not load generated config")
	assertContains(t, err.Error(), "include "+filepath.Join(confDir, "*.conf")+";")

	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("read restored config: %v", readErr)
	}
	if string(got) != string(previous) {
		t.Fatalf("generated config was not rolled back: got %q, want %q", got, previous)
	}
	assertCommandNotRun(t, logPath, "-s reload")
}

func TestLoadInitialConfigReloadsNginxWhenGeneratedConfigIsLoaded(t *testing.T) {
	confDir := filepath.Join(t.TempDir(), "routes")
	target := filepath.Join(confDir, workspacesConfigFile)
	logPath := filepath.Join(t.TempDir(), "nginx.log")
	t.Setenv("FAKE_NGINX_LOG", logPath)
	t.Setenv("FAKE_NGINX_TARGET", target)
	provider := &RoutingProvider{confDir: confDir, binary: fakeNginx(t)}
	setTestRementorDomain(t)

	if err := provider.LoadInitialConfig(nil); err != nil {
		t.Fatalf("LoadInitialConfig failed: %v", err)
	}

	assertCommandRun(t, logPath, "-t")
	assertCommandRun(t, logPath, "-T")
	assertCommandRun(t, logPath, "-s reload")
}

func fakeNginx(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nginx")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_NGINX_LOG"
if [ "$1" = "-T" ]; then
    printf '# configuration file %s:\n' "$FAKE_NGINX_TARGET"
fi
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake nginx: %v", err)
	}
	return path
}

func setTestRementorDomain(t *testing.T) {
	t.Helper()
	previous := config.Config.RementorDomain
	config.Config.RementorDomain = "rementor.localhost"
	t.Cleanup(func() {
		config.Config.RementorDomain = previous
	})
}

func assertCommandRun(t *testing.T, logPath, command string) {
	t.Helper()
	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read command log: %v", err)
	}
	for _, line := range strings.Split(string(got), "\n") {
		if line == command {
			return
		}
	}
	t.Fatalf("expected command %q in log:\n%s", command, got)
}

func assertCommandNotRun(t *testing.T, logPath, command string) {
	t.Helper()
	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read command log: %v", err)
	}
	for _, line := range strings.Split(string(got), "\n") {
		if line == command {
			t.Fatalf("did not expect command %q in log:\n%s", command, got)
		}
	}
}

func renderTestConfig(t *testing.T, workspaces ...*models.Workspace) string {
	t.Helper()
	conf, err := RenderConfig(workspaces, "rementor.localhost")
	if err != nil {
		t.Fatalf("RenderConfig failed: %v", err)
	}
	return conf
}

func serverBlock(t *testing.T, conf, serverName string) string {
	t.Helper()
	marker := "server_name " + serverName + ";"
	idx := strings.Index(conf, marker)
	if idx < 0 {
		t.Fatalf("server %q not found in config:\n%s", serverName, conf)
	}
	start := strings.LastIndex(conf[:idx], "server {")
	if start < 0 {
		t.Fatalf("server block start not found for %q", serverName)
	}
	next := strings.Index(conf[idx:], "\nserver {")
	if next < 0 {
		return conf[start:]
	}
	return conf[start : idx+next]
}

func assertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected config to contain %q, got:\n%s", needle, haystack)
	}
}

func assertNotContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Fatalf("expected config not to contain %q, got:\n%s", needle, haystack)
	}
}
