package config

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/thiagojdb/rementor/internal/models"
	_ "modernc.org/sqlite"
)

// AppConfig holds the application configuration.
type AppConfig struct {
	LoggerAuth              string `json:"loggerAuth"`
	NginxConfDir            string `json:"nginxConfDir"`
	NginxBinary             string `json:"nginxBinary"`
	HealthCheckIntervalSecs int64  `json:"healthCheckIntervalSecs"`
	CertificateLifetimeDays int    `json:"certificateLifetimeDays"`
	RementorDomain          string `json:"rementorDomain"`
}

const (
	DefaultHealthCheckIntervalSecs = 30
	DefaultCertificateLifetimeDays = 180
	DefaultRementorDomain          = "rementor.localhost"
	DefaultNginxBinary             = "nginx"
)

var (
	// Config holds the global application configuration.
	Config AppConfig
)

func getXDGDataHome() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return xdg
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share")
}

func getXDGCacheHome() string {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return xdg
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache")
}

func getXDGConfigHome() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return xdg
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config")
}

func GetDataDir() string {
	dir := filepath.Join(getXDGDataHome(), "rementor")
	_ = ensurePrivateDir(dir)
	return dir
}

func GetConfigDir() string {
	dir := filepath.Join(getXDGConfigHome(), "rementor")
	_ = ensurePrivateDir(dir)
	return dir
}

func GetNginxConfDir() string {
	dir := filepath.Join(GetConfigDir(), "nginx")
	_ = ensurePrivateDir(dir)
	return dir
}

func GetCacheDir() string {
	dir := filepath.Join(getXDGCacheHome(), "rementor")
	_ = ensurePrivateDir(dir)
	return dir
}

// GetDBFile returns the SQLite database used for all persistent app data.
func GetDBFile() string {
	return filepath.Join(GetDataDir(), "rementor.db")
}

func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

func Load() error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	if err := initDB(db); err != nil {
		return err
	}
	if err := ensureConfig(db); err != nil {
		return err
	}

	cfg, err := loadConfig(db)
	if err != nil {
		return err
	}
	cfg, changed := normalizeConfig(cfg)
	if changed {
		if err := saveConfig(db, cfg); err != nil {
			return err
		}
	}
	Config = cfg
	return nil
}

func LoadWorkspaces() ([]*models.Workspace, error) {
	db, err := readyDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	return loadWorkspacesFromDB(db)
}

func LoadState(workspaces []*models.Workspace) error {
	db, err := readyDB()
	if err != nil {
		return err
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT workspace_id, id, active, route_pattern
		FROM applications
	`)
	if err != nil {
		return fmt.Errorf("failed to load application state: %w", err)
	}
	defer rows.Close()

	type state struct {
		active       bool
		routePattern *string
	}
	states := make(map[string]map[string]state)
	for rows.Next() {
		var wsID, appID string
		var active int
		var routePattern sql.NullString
		if err := rows.Scan(&wsID, &appID, &active, &routePattern); err != nil {
			return fmt.Errorf("failed to scan application state: %w", err)
		}
		if states[wsID] == nil {
			states[wsID] = make(map[string]state)
		}
		states[wsID][appID] = state{active: active != 0, routePattern: nullStringPtr(routePattern)}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to iterate application state: %w", err)
	}

	for _, ws := range workspaces {
		for _, app := range ws.Applications {
			if wsStates := states[ws.WorkspaceID]; wsStates != nil {
				if appState, ok := wsStates[app.ID]; ok {
					app.Active = appState.active
					app.RoutePattern = appState.routePattern
				}
			}
		}
	}
	return nil
}

func SaveState(workspaces []*models.Workspace) error {
	db, err := readyDB()
	if err != nil {
		return err
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin state save: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		UPDATE applications
		SET active = ?, route_pattern = ?
		WHERE workspace_id = ? AND id = ?
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare state save: %w", err)
	}
	defer stmt.Close()

	for _, ws := range workspaces {
		for _, app := range ws.Applications {
			if _, err := stmt.Exec(boolInt(app.Active), ptrValue(app.RoutePattern), ws.WorkspaceID, app.ID); err != nil {
				return fmt.Errorf("failed to save state for %s/%s: %w", ws.WorkspaceID, app.ID, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit state save: %w", err)
	}
	return nil
}

func AppendWorkspace(newConfig models.WorkspaceConfig) error {
	db, err := readyDB()
	if err != nil {
		return err
	}
	defer db.Close()

	return insertWorkspaceConfig(db, newConfig)
}

func UpdateWorkspaceApplications(wsID string, apps []models.ApplicationConfig, localDomain, defaultRemoteBaseUrl string) error {
	db, err := readyDB()
	if err != nil {
		return err
	}
	defer db.Close()

	var exists int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workspaces WHERE id = ?`, wsID).Scan(&exists); err != nil {
		return fmt.Errorf("failed to check workspace %q: %w", wsID, err)
	}
	if exists == 0 {
		return fmt.Errorf("workspace %q not found", wsID)
	}

	type oldState struct {
		active       bool
		routePattern *string
		stripOrigin  bool
		loggerConfig *models.LoggerConfig
	}
	oldStates := make(map[string]oldState)
	rows, err := db.Query(`
		SELECT id, active, route_pattern, strip_origin, logger_enabled, logger_endpoint,
			logger_auth_type, logger_auth_username, logger_auth_password, logger_auth_token,
			logger_use_project_config
		FROM applications
		WHERE workspace_id = ?
	`, wsID)
	if err != nil {
		return fmt.Errorf("failed to load existing application state: %w", err)
	}
	for rows.Next() {
		var id string
		var active int
		var stripOrigin int
		var routePattern sql.NullString
		var loggerEnabled, loggerUseProjectConfig sql.NullInt64
		var loggerEndpoint, loggerAuthType, loggerAuthUsername, loggerAuthPassword, loggerAuthToken sql.NullString
		if err := rows.Scan(
			&id, &active, &routePattern, &stripOrigin, &loggerEnabled, &loggerEndpoint,
			&loggerAuthType, &loggerAuthUsername, &loggerAuthPassword, &loggerAuthToken,
			&loggerUseProjectConfig,
		); err != nil {
			rows.Close()
			return fmt.Errorf("failed to scan existing application state: %w", err)
		}
		var loggerConfig *models.LoggerConfig
		if loggerEnabled.Valid || loggerEndpoint.Valid || loggerAuthType.Valid || loggerAuthUsername.Valid ||
			loggerAuthPassword.Valid || loggerAuthToken.Valid || loggerUseProjectConfig.Valid {
			loggerConfig = &models.LoggerConfig{
				Enabled:          loggerEnabled.Valid && loggerEnabled.Int64 != 0,
				Endpoint:         loggerEndpoint.String,
				AuthType:         loggerAuthType.String,
				AuthUsername:     loggerAuthUsername.String,
				AuthPassword:     loggerAuthPassword.String,
				AuthToken:        loggerAuthToken.String,
				UseProjectConfig: loggerUseProjectConfig.Valid && loggerUseProjectConfig.Int64 != 0,
			}
		}
		oldStates[id] = oldState{
			active: active != 0, routePattern: nullStringPtr(routePattern),
			stripOrigin: stripOrigin != 0, loggerConfig: loggerConfig,
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("failed to close existing application state rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to iterate existing application state: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin workspace update: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		UPDATE workspaces
		SET local_domain = ?, default_remote_base_url = ?
		WHERE id = ?
	`, localDomain, defaultRemoteBaseUrl, wsID); err != nil {
		return fmt.Errorf("failed to update workspace routing for %q: %w", wsID, err)
	}

	if _, err := tx.Exec(`DELETE FROM applications WHERE workspace_id = ?`, wsID); err != nil {
		return fmt.Errorf("failed to delete old applications for %q: %w", wsID, err)
	}
	for i, app := range apps {
		if old, ok := oldStates[app.ID]; ok {
			app.Active = old.active
			app.StripOrigin = old.stripOrigin
			if app.LoggerConfig == nil {
				app.LoggerConfig = old.loggerConfig
			}
			if app.RoutePattern == nil {
				app.RoutePattern = old.routePattern
			}
		}
		if err := insertApplicationConfig(tx, wsID, app, i); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit workspace update: %w", err)
	}
	return nil
}

func WorkspaceFromConfig(wsConfig models.WorkspaceConfig) *models.Workspace {
	workspaces := workspacesFromConfigs([]models.WorkspaceConfig{wsConfig})
	if len(workspaces) == 0 {
		return nil
	}
	return workspaces[0]
}

func RemoveWorkspace(wsID string) error {
	db, err := readyDB()
	if err != nil {
		return err
	}
	defer db.Close()

	res, err := db.Exec(`DELETE FROM workspaces WHERE id = ?`, wsID)
	if err != nil {
		return fmt.Errorf("failed to remove workspace %q: %w", wsID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to inspect remove workspace result: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("workspace %q not found", wsID)
	}
	return nil
}

func openDB() (*sql.DB, error) {
	dbFile := GetDBFile()
	if err := ensurePrivateDir(filepath.Dir(dbFile)); err != nil {
		return nil, fmt.Errorf("failed to secure sqlite directory: %w", err)
	}
	file, err := os.OpenFile(dbFile, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("failed to create sqlite database: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("failed to close sqlite database file: %w", err)
	}
	if err := os.Chmod(dbFile, 0o600); err != nil {
		return nil, fmt.Errorf("failed to secure sqlite database permissions: %w", err)
	}

	db, err := sql.Open("sqlite", dbFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to enable sqlite foreign keys: %w", err)
	}
	return db, nil
}

func readyDB() (*sql.DB, error) {
	db, err := openDB()
	if err != nil {
		return nil, err
	}
	if err := initDB(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := ensureConfig(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func initDB(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS app_config (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			logger_auth TEXT NOT NULL DEFAULT '',
			nginx_conf_dir TEXT NOT NULL DEFAULT '',
			nginx_binary TEXT NOT NULL DEFAULT '',
			health_check_interval_secs INTEGER NOT NULL,
			certificate_lifetime_days INTEGER NOT NULL,
			rementor_domain TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS workspaces (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			color TEXT NOT NULL DEFAULT '',
			routing_mode TEXT NOT NULL DEFAULT '',
			local_domain TEXT NOT NULL DEFAULT '',
			default_remote_base_url TEXT NOT NULL DEFAULT '',
			sort_order INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS applications (
			workspace_id TEXT NOT NULL,
			id TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			path TEXT NOT NULL DEFAULT '',
			domain TEXT NOT NULL DEFAULT '',
			remote_base_url TEXT NOT NULL DEFAULT '',
			port INTEGER NOT NULL DEFAULT 0,
			health TEXT NOT NULL DEFAULT '',
			active INTEGER NOT NULL DEFAULT 0,
			route_pattern TEXT,
			context TEXT NOT NULL DEFAULT '',
			logger_enabled INTEGER,
			logger_endpoint TEXT,
			logger_auth_type TEXT,
			logger_auth_username TEXT,
			logger_auth_password TEXT,
			logger_auth_token TEXT,
			logger_use_project_config INTEGER,
			strip_origin INTEGER NOT NULL DEFAULT 0,
			sort_order INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (workspace_id, id),
			FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("failed to initialize sqlite schema: %w", err)
		}
	}
	for _, stmt := range []string{
		`ALTER TABLE app_config ADD COLUMN nginx_conf_dir TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE app_config ADD COLUMN nginx_binary TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := db.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return fmt.Errorf("failed to migrate app_config: %w", err)
		}
	}
	if err := migrateAppConfigSchema(db); err != nil {
		return err
	}
	if err := migrateWorkspaceRemoteBaseURL(db); err != nil {
		return err
	}
	// Migrate: add strip_origin column for existing databases
	if _, err := db.Exec(`ALTER TABLE applications ADD COLUMN strip_origin INTEGER NOT NULL DEFAULT 0`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column name") {
			return fmt.Errorf("failed to migrate strip_origin column: %w", err)
		}
	}
	return nil
}

func migrateWorkspaceRemoteBaseURL(db *sql.DB) error {
	hasNew, err := tableHasColumn(db, "workspaces", "default_remote_base_url")
	if err != nil {
		return err
	}
	if hasNew {
		return nil
	}

	legacyColumn := "production" + "_base"
	hasOld, err := tableHasColumn(db, "workspaces", legacyColumn)
	if err != nil {
		return err
	}
	if !hasOld {
		if _, err := db.Exec(`ALTER TABLE workspaces ADD COLUMN default_remote_base_url TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("failed to add default remote base URL column: %w", err)
		}
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin workspace remote URL migration: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`ALTER TABLE workspaces ADD COLUMN default_remote_base_url TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("failed to add default remote base URL column: %w", err)
	}
	copyLegacySQL := fmt.Sprintf(`UPDATE workspaces SET default_remote_base_url = %s WHERE default_remote_base_url = ''`, legacyColumn)
	if _, err := tx.Exec(copyLegacySQL); err != nil {
		return fmt.Errorf("failed to copy legacy remote base URLs: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit workspace remote URL migration: %w", err)
	}
	return nil
}

func migrateAppConfigSchema(db *sql.DB) error {
	legacyColumn := "cad" + "dy_admin_url"
	hasLegacy, err := tableHasColumn(db, "app_config", legacyColumn)
	if err != nil {
		return err
	}
	if !hasLegacy {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin app_config migration: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`CREATE TABLE app_config_new (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		logger_auth TEXT NOT NULL DEFAULT '',
		nginx_conf_dir TEXT NOT NULL DEFAULT '',
		nginx_binary TEXT NOT NULL DEFAULT '',
		health_check_interval_secs INTEGER NOT NULL,
		certificate_lifetime_days INTEGER NOT NULL,
		rementor_domain TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("failed to create migrated app_config table: %w", err)
	}

	if _, err := tx.Exec(`
		INSERT INTO app_config_new (
			id, logger_auth, nginx_conf_dir, nginx_binary, health_check_interval_secs, certificate_lifetime_days, rementor_domain
		)
		SELECT
			id,
			logger_auth,
			COALESCE(NULLIF(nginx_conf_dir, ''), ?),
			COALESCE(NULLIF(nginx_binary, ''), ?),
			health_check_interval_secs,
			certificate_lifetime_days,
			rementor_domain
		FROM app_config
	`, GetNginxConfDir(), DefaultNginxBinary); err != nil {
		return fmt.Errorf("failed to copy app_config rows: %w", err)
	}

	if _, err := tx.Exec(`DROP TABLE app_config`); err != nil {
		return fmt.Errorf("failed to drop old app_config table: %w", err)
	}
	if _, err := tx.Exec(`ALTER TABLE app_config_new RENAME TO app_config`); err != nil {
		return fmt.Errorf("failed to rename migrated app_config table: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit app_config migration: %w", err)
	}
	return nil
}

func tableHasColumn(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return false, fmt.Errorf("failed to inspect table %q: %w", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false, fmt.Errorf("failed to scan table info for %q: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("failed to iterate table info for %q: %w", table, err)
	}
	return false, nil
}

func ensureConfig(db *sql.DB) error {
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM app_config`).Scan(&count); err != nil {
		return fmt.Errorf("failed to inspect sqlite config: %w", err)
	}
	if count > 0 {
		return nil
	}
	return saveConfig(db, defaultConfig())
}

func loadConfig(db *sql.DB) (AppConfig, error) {
	var cfg AppConfig
	err := db.QueryRow(`
		SELECT logger_auth, nginx_conf_dir, nginx_binary, health_check_interval_secs, certificate_lifetime_days, rementor_domain
		FROM app_config
		WHERE id = 1
	`).Scan(&cfg.LoggerAuth, &cfg.NginxConfDir, &cfg.NginxBinary, &cfg.HealthCheckIntervalSecs, &cfg.CertificateLifetimeDays, &cfg.RementorDomain)
	if err != nil {
		return AppConfig{}, fmt.Errorf("failed to load sqlite config: %w", err)
	}
	return cfg, nil
}

func saveConfig(db *sql.DB, cfg AppConfig) error {
	_, err := db.Exec(`
		INSERT INTO app_config (
			id, logger_auth, nginx_conf_dir, nginx_binary, health_check_interval_secs, certificate_lifetime_days, rementor_domain
		) VALUES (1, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			logger_auth = excluded.logger_auth,
			nginx_conf_dir = excluded.nginx_conf_dir,
			nginx_binary = excluded.nginx_binary,
			health_check_interval_secs = excluded.health_check_interval_secs,
			certificate_lifetime_days = excluded.certificate_lifetime_days,
			rementor_domain = excluded.rementor_domain
	`, cfg.LoggerAuth, cfg.NginxConfDir, cfg.NginxBinary, cfg.HealthCheckIntervalSecs, cfg.CertificateLifetimeDays, cfg.RementorDomain)
	if err != nil {
		return fmt.Errorf("failed to save sqlite config: %w", err)
	}
	return nil
}

func defaultConfig() AppConfig {
	return AppConfig{
		LoggerAuth:              "",
		NginxConfDir:            GetNginxConfDir(),
		NginxBinary:             DefaultNginxBinary,
		HealthCheckIntervalSecs: DefaultHealthCheckIntervalSecs,
		CertificateLifetimeDays: DefaultCertificateLifetimeDays,
		RementorDomain:          DefaultRementorDomain,
	}
}

func normalizeConfig(cfg AppConfig) (AppConfig, bool) {
	changed := false
	if cfg.HealthCheckIntervalSecs < 1 {
		cfg.HealthCheckIntervalSecs = DefaultHealthCheckIntervalSecs
		changed = true
	}
	if cfg.CertificateLifetimeDays < 1 {
		cfg.CertificateLifetimeDays = DefaultCertificateLifetimeDays
		changed = true
	}
	if cfg.NginxConfDir == "" {
		cfg.NginxConfDir = GetNginxConfDir()
		changed = true
	}
	if cfg.NginxBinary == "" {
		cfg.NginxBinary = DefaultNginxBinary
		changed = true
	}
	if cfg.RementorDomain == "" {
		cfg.RementorDomain = DefaultRementorDomain
		changed = true
	}
	return cfg, changed
}

func loadWorkspacesFromDB(db *sql.DB) ([]*models.Workspace, error) {
	rows, err := db.Query(`
		SELECT id, type, name, color, routing_mode, local_domain, default_remote_base_url
		FROM workspaces
		ORDER BY sort_order, id
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to load sqlite workspaces: %w", err)
	}
	defer rows.Close()

	var configs []models.WorkspaceConfig
	for rows.Next() {
		var ws models.WorkspaceConfig
		if err := rows.Scan(&ws.ID, &ws.Type, &ws.Name, &ws.Color, &ws.Routing.Mode, &ws.Routing.LocalDomain, &ws.Routing.DefaultRemoteBaseURL); err != nil {
			return nil, fmt.Errorf("failed to scan sqlite workspace: %w", err)
		}
		configs = append(configs, ws)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate sqlite workspaces: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("failed to close sqlite workspaces: %w", err)
	}
	for i := range configs {
		apps, err := loadApplicationConfigs(db, configs[i].ID)
		if err != nil {
			return nil, err
		}
		configs[i].Applications = apps
	}
	return workspacesFromConfigs(configs), nil
}

func loadApplicationConfigs(db *sql.DB, wsID string) ([]models.ApplicationConfig, error) {
	rows, err := db.Query(`
		SELECT
			id, name, path, domain, remote_base_url, port, health, active, route_pattern, context,
			logger_enabled, logger_endpoint, logger_auth_type, logger_auth_username, logger_auth_password,
			logger_auth_token, logger_use_project_config, strip_origin
		FROM applications
		WHERE workspace_id = ?
		ORDER BY sort_order, id
	`, wsID)
	if err != nil {
		return nil, fmt.Errorf("failed to load sqlite applications for %q: %w", wsID, err)
	}
	defer rows.Close()

	var apps []models.ApplicationConfig
	for rows.Next() {
		var app models.ApplicationConfig
		var active int
		var routePattern sql.NullString
		var loggerEnabled, loggerUseProjectConfig sql.NullInt64
		var loggerEndpoint, loggerAuthType, loggerAuthUsername, loggerAuthPassword, loggerAuthToken sql.NullString
		if err := rows.Scan(
			&app.ID, &app.Name, &app.Path, &app.Domain, &app.RemoteBaseUrl, &app.Port, &app.Health, &active,
			&routePattern, &app.Context, &loggerEnabled, &loggerEndpoint, &loggerAuthType, &loggerAuthUsername,
			&loggerAuthPassword, &loggerAuthToken, &loggerUseProjectConfig, &app.StripOrigin,
		); err != nil {
			return nil, fmt.Errorf("failed to scan sqlite application: %w", err)
		}
		app.Active = active != 0
		app.RoutePattern = nullStringPtr(routePattern)
		if loggerEnabled.Valid || loggerEndpoint.Valid || loggerAuthType.Valid || loggerAuthUsername.Valid || loggerAuthPassword.Valid || loggerAuthToken.Valid || loggerUseProjectConfig.Valid {
			app.LoggerConfig = &models.LoggerConfig{
				Enabled:          loggerEnabled.Valid && loggerEnabled.Int64 != 0,
				Endpoint:         loggerEndpoint.String,
				AuthType:         loggerAuthType.String,
				AuthUsername:     loggerAuthUsername.String,
				AuthPassword:     loggerAuthPassword.String,
				AuthToken:        loggerAuthToken.String,
				UseProjectConfig: loggerUseProjectConfig.Valid && loggerUseProjectConfig.Int64 != 0,
			}
		}
		apps = append(apps, app)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate sqlite applications: %w", err)
	}
	return apps, nil
}

func workspacesFromConfigs(configs []models.WorkspaceConfig) []*models.Workspace {
	var workspaces []*models.Workspace
	for _, wsConfig := range configs {
		name := wsConfig.Name
		if name == "" {
			name = wsConfig.ID
		}
		color := wsConfig.Color
		if color == "" {
			color = "bg-blue-500"
		}

		ws := &models.Workspace{
			WorkspaceID:  wsConfig.ID,
			Type:         wsConfig.Type,
			Name:         stringPtr(name),
			Color:        stringPtr(color),
			Applications: convertAppConfigs(wsConfig.Applications),
		}
		if wsConfig.Type != models.WorkspaceTypeLocalApps {
			ws.RoutingConfig = &wsConfig.Routing
		}
		ws.SetDefaults()
		workspaces = append(workspaces, ws)
	}
	return workspaces
}

func convertAppConfigs(configs []models.ApplicationConfig) []*models.Application {
	var apps []*models.Application
	for _, appConfig := range configs {
		health := appConfig.Health
		if health == "" {
			health = models.DefaultHealthEndpoint
		}

		path := appConfig.Path
		if path != "" && path[0] != '/' {
			path = "/" + path
		}

		app := &models.Application{
			ID:            appConfig.ID,
			Name:          appConfig.Name,
			Path:          path,
			Domain:        appConfig.Domain,
			RemoteBaseUrl: appConfig.RemoteBaseUrl,
			Context:       appConfig.Context,
			Health:        health,
			Port:          appConfig.Port,
			Active:        appConfig.Active,
			RoutePattern:  appConfig.RoutePattern,
			LoggerConfig:  appConfig.LoggerConfig,
			StripOrigin:   appConfig.StripOrigin,
		}
		apps = append(apps, app)
	}
	return apps
}

func insertWorkspaceConfig(db *sql.DB, ws models.WorkspaceConfig) error {
	var nextOrder int
	if err := db.QueryRow(`SELECT COALESCE(MAX(sort_order), -1) + 1 FROM workspaces`).Scan(&nextOrder); err != nil {
		return fmt.Errorf("failed to choose workspace order: %w", err)
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin workspace insert: %w", err)
	}
	defer tx.Rollback()
	if err := insertWorkspaceConfigTx(tx, ws, nextOrder); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit workspace insert: %w", err)
	}
	return nil
}

func insertWorkspaceConfigTx(tx *sql.Tx, ws models.WorkspaceConfig, order int) error {
	if ws.Type == "" {
		ws.Type = models.WorkspaceTypeRouting
	}
	_, err := tx.Exec(`
		INSERT INTO workspaces (
			id, type, name, color, routing_mode, local_domain, default_remote_base_url, sort_order
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, ws.ID, ws.Type, ws.Name, ws.Color, ws.Routing.Mode, ws.Routing.LocalDomain, ws.Routing.DefaultRemoteBaseURL, order)
	if err != nil {
		return fmt.Errorf("failed to insert workspace %q: %w", ws.ID, err)
	}
	for i, app := range ws.Applications {
		if err := insertApplicationConfig(tx, ws.ID, app, i); err != nil {
			return err
		}
	}
	return nil
}

func insertApplicationConfig(tx *sql.Tx, wsID string, app models.ApplicationConfig, order int) error {
	health := app.Health
	if health == "" {
		health = models.DefaultHealthEndpoint
	}

	var loggerEnabled, loggerUseProjectConfig any
	var loggerEndpoint, loggerAuthType, loggerAuthUsername, loggerAuthPassword, loggerAuthToken any
	if app.LoggerConfig != nil {
		loggerEnabled = boolInt(app.LoggerConfig.Enabled)
		loggerEndpoint = nullIfEmpty(app.LoggerConfig.Endpoint)
		loggerAuthType = nullIfEmpty(app.LoggerConfig.AuthType)
		loggerAuthUsername = nullIfEmpty(app.LoggerConfig.AuthUsername)
		loggerAuthPassword = nullIfEmpty(app.LoggerConfig.AuthPassword)
		loggerAuthToken = nullIfEmpty(app.LoggerConfig.AuthToken)
		loggerUseProjectConfig = boolInt(app.LoggerConfig.UseProjectConfig)
	}

	_, err := tx.Exec(`
		INSERT INTO applications (
			workspace_id, id, name, path, domain, remote_base_url, port, health, active, route_pattern, context,
			logger_enabled, logger_endpoint, logger_auth_type, logger_auth_username, logger_auth_password,
			logger_auth_token, logger_use_project_config, strip_origin, sort_order
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, wsID, app.ID, app.Name, app.Path, app.Domain, app.RemoteBaseUrl, app.Port, health, boolInt(app.Active),
		ptrValue(app.RoutePattern), app.Context, loggerEnabled, loggerEndpoint, loggerAuthType, loggerAuthUsername,
		loggerAuthPassword, loggerAuthToken, loggerUseProjectConfig, boolInt(app.StripOrigin), order)
	if err != nil {
		return fmt.Errorf("failed to insert application %s/%s: %w", wsID, app.ID, err)
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullStringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func ptrValue(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func stringPtr(s string) *string {
	return &s
}
