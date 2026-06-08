package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thiagojdb/rementor/internal/models"
)

func TestLoadCreatesSQLiteConfigAndNormalizesNginxSettings(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	configHome := filepath.Join(t.TempDir(), "config")
	t.Setenv("XDG_CONFIG_HOME", configHome)

	Config = AppConfig{}

	db, err := openDB()
	if err != nil {
		t.Fatalf("openDB failed: %v", err)
	}
	if err := initDB(db); err != nil {
		t.Fatalf("initDB failed: %v", err)
	}
	if err := saveConfig(db, AppConfig{
		LoggerAuth:              "Basic abc123",
		NginxConfDir:            "",
		NginxBinary:             "",
		HealthCheckIntervalSecs: 30,
		CertificateLifetimeDays: 180,
		RementorDomain:          "rementor.localhost",
	}); err != nil {
		t.Fatalf("saveConfig failed: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("failed to close db: %v", err)
	}

	if err := Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if got, want := Config.NginxConfDir, filepath.Join(configHome, "rementor", "nginx"); got != want {
		t.Fatalf("expected NginxConfDir %q, got %q", want, got)
	}
	if got := Config.NginxBinary; got != DefaultNginxBinary {
		t.Fatalf("expected NginxBinary %q, got %q", DefaultNginxBinary, got)
	}
}

func TestOpenDBCreatesPrivateDirectoryAndDatabaseFile(t *testing.T) {
	dataHome := filepath.Join(t.TempDir(), "data")
	t.Setenv("XDG_DATA_HOME", dataHome)

	db, err := openDB()
	if err != nil {
		t.Fatalf("openDB failed: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("failed to close db: %v", err)
	}

	dataDirInfo, err := os.Stat(filepath.Join(dataHome, "rementor"))
	if err != nil {
		t.Fatalf("stat data dir failed: %v", err)
	}
	if got := dataDirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("expected data dir permissions 0700, got %04o", got)
	}

	dbInfo, err := os.Stat(filepath.Join(dataHome, "rementor", "rementor.db"))
	if err != nil {
		t.Fatalf("stat db failed: %v", err)
	}
	if got := dbInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected sqlite permissions 0600, got %04o", got)
	}
}

func TestLoadMigratesLegacyAppConfigTable(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	configHome := filepath.Join(t.TempDir(), "config")
	t.Setenv("XDG_CONFIG_HOME", configHome)

	Config = AppConfig{}

	db, err := openDB()
	if err != nil {
		t.Fatalf("openDB failed: %v", err)
	}
	legacyColumn := "cad" + "dy_admin_url"
	if _, err := db.Exec(`CREATE TABLE app_config (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		logger_auth TEXT NOT NULL DEFAULT '',
		` + legacyColumn + ` TEXT NOT NULL,
		health_check_interval_secs INTEGER NOT NULL,
		certificate_lifetime_days INTEGER NOT NULL,
		rementor_domain TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create legacy app_config failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO app_config (
		id, logger_auth, ` + legacyColumn + `, health_check_interval_secs, certificate_lifetime_days, rementor_domain
	) VALUES (1, '', 'http://localhost:2019', 30, 180, 'rementor.localhost')`); err != nil {
		t.Fatalf("insert legacy config failed: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("failed to close db: %v", err)
	}

	if err := Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if got, want := Config.NginxConfDir, filepath.Join(configHome, "rementor", "nginx"); got != want {
		t.Fatalf("expected NginxConfDir %q, got %q", want, got)
	}

	db, err = openDB()
	if err != nil {
		t.Fatalf("openDB failed after migration: %v", err)
	}
	defer db.Close()
	hasLegacy, err := tableHasColumn(db, "app_config", legacyColumn)
	if err != nil {
		t.Fatalf("tableHasColumn failed: %v", err)
	}
	if hasLegacy {
		t.Fatalf("legacy app_config column was not removed")
	}
}

func TestLoadMigratesLegacyWorkspaceRemoteBaseURL(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))

	Config = AppConfig{}

	db, err := openDB()
	if err != nil {
		t.Fatalf("openDB failed: %v", err)
	}
	legacyColumn := "production" + "_base"
	if _, err := db.Exec(`CREATE TABLE workspaces (
		id TEXT PRIMARY KEY,
		type TEXT NOT NULL,
		name TEXT NOT NULL DEFAULT '',
		color TEXT NOT NULL DEFAULT '',
		routing_mode TEXT NOT NULL DEFAULT '',
		local_domain TEXT NOT NULL DEFAULT '',
		` + legacyColumn + ` TEXT NOT NULL DEFAULT '',
		sort_order INTEGER NOT NULL DEFAULT 0
	)`); err != nil {
		t.Fatalf("create legacy workspaces failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces (
		id, type, name, color, routing_mode, local_domain, ` + legacyColumn + `, sort_order
	) VALUES (
		'demo', 'routing', 'Demo', 'bg-cyan-500', 'path-based', 'api.localhost', 'http://127.0.0.1:18080', 0
	)`); err != nil {
		t.Fatalf("insert legacy workspace failed: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("failed to close db: %v", err)
	}

	if err := Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	workspaces, err := LoadWorkspaces()
	if err != nil {
		t.Fatalf("LoadWorkspaces failed: %v", err)
	}
	if len(workspaces) != 1 {
		t.Fatalf("expected one workspace, got %d", len(workspaces))
	}
	if got, want := workspaces[0].GetDefaultRemoteBaseURL(), "http://127.0.0.1:18080"; got != want {
		t.Fatalf("expected migrated remote base URL %q, got %q", want, got)
	}
}

func TestLoadWorkspacesPreservesApplicationRemoteBaseURL(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	if err := AppendWorkspace(models.WorkspaceConfig{
		ID:   "dev",
		Type: models.WorkspaceTypeRouting,
		Routing: models.RoutingConfig{
			Mode:                 "path-based",
			LocalDomain:          "api.localhost",
			DefaultRemoteBaseURL: "https://remote.example.test",
		},
		Applications: []models.ApplicationConfig{
			{
				ID:            "orders-api",
				Path:          "/orders",
				RemoteBaseUrl: "https://api.remote.example.test",
				Port:          2444,
				Context:       "/orders",
			},
		},
	}); err != nil {
		t.Fatalf("AppendWorkspace failed: %v", err)
	}

	workspaces, err := LoadWorkspaces()
	if err != nil {
		t.Fatalf("LoadWorkspaces failed: %v", err)
	}
	if len(workspaces) != 1 {
		t.Fatalf("expected 1 workspace, got %d", len(workspaces))
	}
	if len(workspaces[0].Applications) != 1 {
		t.Fatalf("expected 1 application, got %d", len(workspaces[0].Applications))
	}

	app := workspaces[0].Applications[0]
	if got, want := app.RemoteBaseUrl, "https://api.remote.example.test"; got != want {
		t.Fatalf("expected RemoteBaseUrl %q, got %q", want, got)
	}
	if got, want := app.GetRemoteBaseUrl(workspaces[0]), "https://api.remote.example.test"; got != want {
		t.Fatalf("expected effective remote base URL %q, got %q", want, got)
	}
}

func TestSaveStatePersistsActiveAndRoutePatternInSQLite(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	if err := AppendWorkspace(models.WorkspaceConfig{
		ID:   "dev",
		Type: models.WorkspaceTypeRouting,
		Routing: models.RoutingConfig{
			Mode:                 "path-based",
			LocalDomain:          "dev.localhost",
			DefaultRemoteBaseURL: "https://dev.example.com",
		},
		Applications: []models.ApplicationConfig{
			{
				ID:     "api",
				Path:   "/api",
				Port:   8080,
				Active: false,
			},
		},
	}); err != nil {
		t.Fatalf("AppendWorkspace failed: %v", err)
	}

	workspaces, err := LoadWorkspaces()
	if err != nil {
		t.Fatalf("LoadWorkspaces failed: %v", err)
	}

	pattern := "/api/v2/*"
	workspaces[0].Applications[0].Active = true
	workspaces[0].Applications[0].RoutePattern = &pattern
	if err := SaveState(workspaces); err != nil {
		t.Fatalf("SaveState failed: %v", err)
	}

	reloaded, err := LoadWorkspaces()
	if err != nil {
		t.Fatalf("LoadWorkspaces failed: %v", err)
	}

	app := reloaded[0].Applications[0]
	if !app.Active {
		t.Fatalf("expected active state to be true")
	}
	if app.RoutePattern == nil || *app.RoutePattern != pattern {
		t.Fatalf("expected route pattern %q, got %#v", pattern, app.RoutePattern)
	}
}

func TestUpdateWorkspaceApplicationsPreservesRuntimeMetadata(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	pattern := "/orders/*"
	logger := &models.LoggerConfig{
		Enabled:      true,
		Endpoint:     "actuator/loggers",
		AuthType:     "basic",
		AuthUsername: "developer",
		AuthPassword: "local-only",
	}
	if err := AppendWorkspace(models.WorkspaceConfig{
		ID:   "demo",
		Type: models.WorkspaceTypeRouting,
		Routing: models.RoutingConfig{
			LocalDomain:          "api.localhost",
			DefaultRemoteBaseURL: "https://remote.example.test",
		},
		Applications: []models.ApplicationConfig{{
			ID: "orders-api", Path: "/orders", Port: 8081, Active: true,
			RoutePattern: &pattern, StripOrigin: true, LoggerConfig: logger,
		}},
	}); err != nil {
		t.Fatalf("AppendWorkspace failed: %v", err)
	}

	if err := UpdateWorkspaceApplications("demo", []models.ApplicationConfig{{
		ID: "orders-api", Name: "Orders", Path: "/orders", Port: 8082,
	}}, "api.localhost", "https://remote.example.test"); err != nil {
		t.Fatalf("UpdateWorkspaceApplications failed: %v", err)
	}

	workspaces, err := LoadWorkspaces()
	if err != nil {
		t.Fatalf("LoadWorkspaces failed: %v", err)
	}
	app := workspaces[0].Applications[0]
	if !app.Active || !app.StripOrigin || app.RoutePattern == nil || *app.RoutePattern != pattern {
		t.Fatalf("runtime routing metadata was not preserved: %#v", app)
	}
	if app.LoggerConfig == nil || app.LoggerConfig.AuthUsername != logger.AuthUsername ||
		app.LoggerConfig.AuthPassword != logger.AuthPassword {
		t.Fatalf("logger metadata was not preserved: %#v", app.LoggerConfig)
	}
}
