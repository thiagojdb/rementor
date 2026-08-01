package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestLoadAppliesRuntimeNginxOverridesWithoutPersistingThem(t *testing.T) {
	previousConfig := Config
	t.Cleanup(func() {
		Config = previousConfig
	})

	configHome := t.TempDir()
	dataHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("REMENTOR_NGINX_CONF_DIR", filepath.Join(configHome, "standalone-routes"))
	t.Setenv("REMENTOR_NGINX_BINARY", filepath.Join(configHome, "rementor-nginx"))
	t.Setenv("REMENTOR_DOMAIN", "dashboard.localhost")

	if err := Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if got, want := Config.NginxConfDir, filepath.Join(configHome, "standalone-routes"); got != want {
		t.Fatalf("expected runtime NginxConfDir %q, got %q", want, got)
	}
	if got, want := Config.NginxBinary, filepath.Join(configHome, "rementor-nginx"); got != want {
		t.Fatalf("expected runtime NginxBinary %q, got %q", want, got)
	}
	if got, want := Config.RementorDomain, "dashboard.localhost"; got != want {
		t.Fatalf("expected runtime RementorDomain %q, got %q", want, got)
	}

	db, err := openDB()
	if err != nil {
		t.Fatalf("openDB failed: %v", err)
	}
	defer db.Close()
	persisted, err := loadConfig(db)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	if persisted.NginxBinary != DefaultNginxBinary {
		t.Fatalf("runtime override was persisted: %q", persisted.NginxBinary)
	}
	if persisted.RementorDomain != DefaultRementorDomain {
		t.Fatalf("runtime domain override was persisted: %q", persisted.RementorDomain)
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

func TestRouteOperationJournalPersistsAndUpdates(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))

	created := time.Now().UTC().Truncate(time.Microsecond)
	operation := models.RouteOperationJournal{
		OperationID:     "op-test",
		WorkspaceID:     "demo",
		IdempotencyKey:  "request-1",
		Fingerprint:     "fingerprint",
		CorrelationID:   "corr-1",
		ExpectedVersion: 3,
		RouteVersion:    4,
		Phase:           "prepared",
		Status:          "prepared",
		CreatedAt:       created,
		UpdatedAt:       created,
		Result:          []byte(`{"status":"committed"}`),
	}
	if err := BeginRouteOperation(operation); err != nil {
		t.Fatalf("BeginRouteOperation failed: %v", err)
	}
	operation.Phase = "committed"
	operation.Status = "committed"
	operation.RollbackStatus = "not-needed"
	operation.UpdatedAt = created.Add(time.Second)
	if err := UpdateRouteOperation(operation); err != nil {
		t.Fatalf("UpdateRouteOperation failed: %v", err)
	}

	operations, err := LoadRouteOperations()
	if err != nil {
		t.Fatalf("LoadRouteOperations failed: %v", err)
	}
	if len(operations) != 1 {
		t.Fatalf("journal entries = %d, want 1", len(operations))
	}
	got := operations[0]
	if got.Status != "committed" || got.RollbackStatus != "not-needed" || got.RouteVersion != 4 || string(got.Result) != string(operation.Result) {
		t.Fatalf("journal entry = %#v", got)
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

func TestApplicationRouteMetadataPersistsAndMigratesLegacyFields(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))

	if err := AppendWorkspace(models.WorkspaceConfig{
		ID: "demo", Type: models.WorkspaceTypeRouting,
		Routing: models.RoutingConfig{LocalDomain: "api.localhost"},
		Applications: []models.ApplicationConfig{{
			ID: "portal", PublicPath: "/portal/", UpstreamContext: "/service-portal/", FrontendRoot: "/portal", FrontendRootSource: "registration", Port: 8080,
		}},
	}); err != nil {
		t.Fatalf("AppendWorkspace failed: %v", err)
	}
	workspaces, err := LoadWorkspaces()
	if err != nil {
		t.Fatalf("LoadWorkspaces failed: %v", err)
	}
	app := workspaces[0].Applications[0]
	if app.PublicPath != "/portal" || app.Path != "/portal" || app.UpstreamContext != "/service-portal" || app.Context != "/service-portal" || app.FrontendRoot != "/portal" {
		t.Fatalf("explicit metadata was not normalized/persisted: %#v", app)
	}

	if err := AppendWorkspace(models.WorkspaceConfig{
		ID: "legacy", Type: models.WorkspaceTypeRouting,
		Routing:      models.RoutingConfig{LocalDomain: "legacy.localhost"},
		Applications: []models.ApplicationConfig{{ID: "legacy-app", Path: "/legacy", Context: "/service-legacy", Port: 8081}},
	}); err != nil {
		t.Fatalf("AppendWorkspace legacy failed: %v", err)
	}
	workspaces, err = LoadWorkspaces()
	if err != nil {
		t.Fatalf("LoadWorkspaces after legacy append failed: %v", err)
	}
	legacy := workspaces[1].Applications[0]
	if legacy.PublicPath != "/legacy" || legacy.UpstreamContext != "/service-legacy" || legacy.IngressPath() != "/service-legacy" {
		t.Fatalf("legacy route metadata was not migrated: %#v", legacy)
	}
}

func TestApplicationIdentityAndAliasesPersistAcrossWorkspaces(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))

	identity := models.ApplicationConfig{
		ID: "rtc", AppID: "rtc", ServiceID: "reforma-tributaria-consumo", Repository: "front-giss-v2",
		Aliases: []string{"reforma-tributaria-consumo", "front_giss_v2"}, Path: "/rtc", Port: 8080,
	}
	for _, workspaceID := range []string{"desenvolvimento", "qualidade"} {
		if err := AppendWorkspace(models.WorkspaceConfig{
			ID: workspaceID, Type: models.WorkspaceTypeRouting,
			Routing:      models.RoutingConfig{LocalDomain: workspaceID + ".localhost"},
			Applications: []models.ApplicationConfig{identity},
		}); err != nil {
			t.Fatalf("AppendWorkspace(%s) failed: %v", workspaceID, err)
		}
	}

	workspaces, err := LoadWorkspaces()
	if err != nil {
		t.Fatalf("LoadWorkspaces failed: %v", err)
	}
	if len(workspaces) != 2 {
		t.Fatalf("expected two workspaces, got %d", len(workspaces))
	}
	for _, workspace := range workspaces {
		app := workspace.Applications[0]
		if app.CanonicalAppID() != "rtc" || app.ServiceID != "reforma-tributaria-consumo" || app.Repository != "front-giss-v2" {
			t.Fatalf("identity did not persist in %s: %#v", workspace.WorkspaceID, app)
		}
		if len(app.Aliases) != 2 || app.Aliases[0] != "front-giss-v2" || app.Aliases[1] != "reforma-tributaria-consumo" {
			t.Fatalf("aliases did not persist in %s: %#v", workspace.WorkspaceID, app.Aliases)
		}
	}
}

func TestApplicationBindingReusesIdentityMetadataWhenOmitted(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))

	if err := AppendWorkspace(models.WorkspaceConfig{
		ID: "desenvolvimento", Type: models.WorkspaceTypeRouting,
		Routing: models.RoutingConfig{LocalDomain: "desenvolvimento.localhost"},
		Applications: []models.ApplicationConfig{{
			ID: "rtc", AppID: "rtc", ServiceID: "reforma-tributaria-consumo", Repository: "front-giss-v2",
			Aliases: []string{"rtc-ui"}, Path: "/rtc", Port: 8080,
		}},
	}); err != nil {
		t.Fatalf("AppendWorkspace(desenvolvimento) failed: %v", err)
	}
	if err := AppendWorkspace(models.WorkspaceConfig{
		ID: "qualidade", Type: models.WorkspaceTypeRouting,
		Routing: models.RoutingConfig{LocalDomain: "qualidade.localhost"},
		Applications: []models.ApplicationConfig{{
			ID: "rtc", Path: "/rtc", Port: 8081,
		}},
	}); err != nil {
		t.Fatalf("AppendWorkspace(qualidade) failed: %v", err)
	}

	workspaces, err := LoadWorkspaces()
	if err != nil {
		t.Fatalf("LoadWorkspaces failed: %v", err)
	}
	if got := workspaces[1].Applications[0]; got.ServiceID != "reforma-tributaria-consumo" || got.Repository != "front-giss-v2" || len(got.Aliases) != 1 || got.Aliases[0] != "rtc-ui" {
		t.Fatalf("expected shared identity metadata on second binding, got %#v", got)
	}
}

func TestApplicationCanonicalIDCannotCollideWithAnotherIdentityAlias(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))

	if err := AppendWorkspace(models.WorkspaceConfig{
		ID: "desenvolvimento", Type: models.WorkspaceTypeRouting,
		Routing: models.RoutingConfig{LocalDomain: "desenvolvimento.localhost"},
		Applications: []models.ApplicationConfig{{
			ID: "frontend", Path: "/frontend", Port: 8080, Aliases: []string{"shared-ui"},
		}},
	}); err != nil {
		t.Fatalf("AppendWorkspace(frontend) failed: %v", err)
	}

	err := AppendWorkspace(models.WorkspaceConfig{
		ID: "qualidade", Type: models.WorkspaceTypeRouting,
		Routing: models.RoutingConfig{LocalDomain: "qualidade.localhost"},
		Applications: []models.ApplicationConfig{{
			ID: "shared-ui", Path: "/shared-ui", Port: 8081,
		}},
	})
	if !errors.Is(err, models.ErrAliasConflict) {
		t.Fatalf("expected canonical/alias collision, got %v", err)
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
				ID:            "api",
				Path:          "/api",
				Port:          8080,
				Active:        false,
				RouteOverride: true,
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
	if !app.RouteOverride {
		t.Fatalf("expected route override metadata to persist")
	}
}

func TestUpdateWorkspaceApplicationsPreservesRoutingMetadata(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	pattern := "/orders/*"
	if err := AppendWorkspace(models.WorkspaceConfig{
		ID:   "demo",
		Type: models.WorkspaceTypeRouting,
		Routing: models.RoutingConfig{
			LocalDomain:          "api.localhost",
			DefaultRemoteBaseURL: "https://remote.example.test",
		},
		Applications: []models.ApplicationConfig{{
			ID: "orders-api", Path: "/orders", Port: 8081, Active: true,
			RoutePattern: &pattern, RouteOverride: true, StripOrigin: true,
		}},
	}); err != nil {
		t.Fatalf("AppendWorkspace failed: %v", err)
	}

	if err := UpdateWorkspaceApplications("demo", []models.ApplicationConfig{{
		ID: "orders-api", Name: "Orders", Path: "/orders", Port: 8082, RouteOverride: true,
	}}, "api.localhost", "https://remote.example.test"); err != nil {
		t.Fatalf("UpdateWorkspaceApplications failed: %v", err)
	}

	workspaces, err := LoadWorkspaces()
	if err != nil {
		t.Fatalf("LoadWorkspaces failed: %v", err)
	}
	app := workspaces[0].Applications[0]
	if !app.Active || !app.StripOrigin || !app.RouteOverride || app.RoutePattern == nil || *app.RoutePattern != pattern {
		t.Fatalf("runtime routing metadata was not preserved: %#v", app)
	}
}

func TestUpdateWorkspaceApplicationsPreservesOmittedRouteOverrideAndAcceptsExplicitFalse(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	if err := AppendWorkspace(models.WorkspaceConfig{
		ID: "demo", Type: models.WorkspaceTypeRouting,
		Routing:      models.RoutingConfig{LocalDomain: "api.localhost"},
		Applications: []models.ApplicationConfig{{ID: "orders-api", Path: "/orders", RouteOverride: true}},
	}); err != nil {
		t.Fatalf("AppendWorkspace failed: %v", err)
	}

	if err := UpdateWorkspaceApplications("demo", []models.ApplicationConfig{{
		ID: "orders-api", Path: "/orders", RouteOverride: false,
	}}, "api.localhost", ""); err != nil {
		t.Fatalf("omitted route override update failed: %v", err)
	}
	workspaces, err := LoadWorkspaces()
	if err != nil {
		t.Fatalf("LoadWorkspaces after omitted update failed: %v", err)
	}
	if !workspaces[0].Applications[0].RouteOverride {
		t.Fatal("omitted route override should preserve the stored true value")
	}

	if err := UpdateWorkspaceApplications("demo", []models.ApplicationConfig{{
		ID: "orders-api", Path: "/orders", RouteOverride: false, RouteOverrideSet: true,
	}}, "api.localhost", ""); err != nil {
		t.Fatalf("explicit false route override update failed: %v", err)
	}
	workspaces, err = LoadWorkspaces()
	if err != nil {
		t.Fatalf("LoadWorkspaces after explicit false update failed: %v", err)
	}
	if workspaces[0].Applications[0].RouteOverride {
		t.Fatal("explicit false route override should replace the stored true value")
	}
}

func TestReplaceWorkspacesAtomicallyReplacesWorkspaceProjection(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	if err := AppendWorkspace(models.WorkspaceConfig{
		ID: "old", Type: models.WorkspaceTypeRouting,
		Routing: models.RoutingConfig{LocalDomain: "old.localhost"},
	}); err != nil {
		t.Fatalf("AppendWorkspace failed: %v", err)
	}

	routePattern := "/orders/*"
	desired := []*models.Workspace{{
		WorkspaceID: "demo",
		Type:        models.WorkspaceTypeRouting,
		Name:        stringPtr("Demo"),
		RoutingConfig: &models.RoutingConfig{
			Mode: "path-based", LocalDomain: "api.localhost", DefaultRemoteBaseURL: "https://remote.example.test",
		},
		Applications: []*models.Application{{
			ID: "orders-api", Path: "/orders", Port: 8081, Active: true, RoutePattern: &routePattern,
		}},
	}}
	if err := ReplaceWorkspaces(desired); err != nil {
		t.Fatalf("ReplaceWorkspaces failed: %v", err)
	}

	workspaces, err := LoadWorkspaces()
	if err != nil {
		t.Fatalf("LoadWorkspaces failed: %v", err)
	}
	if len(workspaces) != 1 || workspaces[0].WorkspaceID != "demo" {
		t.Fatalf("expected only replacement workspace, got %#v", workspaces)
	}
	app := workspaces[0].Applications[0]
	if !app.Active || app.RoutePattern == nil || *app.RoutePattern != routePattern {
		t.Fatalf("expected replacement route state to persist, got %#v", app)
	}
}
