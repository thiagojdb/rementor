package config

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/thiagojdb/rementor/internal/models"
	_ "modernc.org/sqlite"
)

// AppConfig holds the application configuration.
type AppConfig struct {
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
	cfg = applyEnvironment(cfg)
	Config = cfg
	return nil
}

func applyEnvironment(cfg AppConfig) AppConfig {
	if value := os.Getenv("REMENTOR_NGINX_CONF_DIR"); value != "" {
		cfg.NginxConfDir = filepath.Clean(value)
	}
	if value := os.Getenv("REMENTOR_NGINX_BINARY"); value != "" {
		cfg.NginxBinary = value
	}
	if value := os.Getenv("REMENTOR_DOMAIN"); value != "" {
		cfg.RementorDomain = value
	}
	return cfg
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
		SELECT workspace_id, id, active, route_pattern, route_version, operation_id, correlation_id, operation_kind, operation_created_at, operation_completed_at
		FROM applications
	`)
	if err != nil {
		return fmt.Errorf("failed to load application state: %w", err)
	}
	defer rows.Close()

	type state struct {
		active       bool
		routePattern *string
		route        models.RouteState
		operation    *models.OperationMetadata
	}
	states := make(map[string]map[string]state)
	for rows.Next() {
		var wsID, appID string
		var active int
		var routePattern sql.NullString
		var routeVersion int64
		var operationID, correlationID, operationKind string
		var operationCreatedAt, operationCompletedAt sql.NullString
		if err := rows.Scan(&wsID, &appID, &active, &routePattern, &routeVersion, &operationID, &correlationID, &operationKind, &operationCreatedAt, &operationCompletedAt); err != nil {
			return fmt.Errorf("failed to scan application state: %w", err)
		}
		if states[wsID] == nil {
			states[wsID] = make(map[string]state)
		}
		appState := state{active: active != 0, routePattern: nullStringPtr(routePattern)}
		appState.route.RouteVersion = uint64(routeVersion)
		appState.route.OperationID = operationID
		if operationID != "" {
			createdAt := parseStoredTime(operationCreatedAt)
			completedAt := parseStoredTime(operationCompletedAt)
			appState.operation = &models.OperationMetadata{OperationID: operationID, CorrelationID: correlationID, RouteVersion: uint64(routeVersion), Kind: operationKind, CreatedAt: createdAt, CompletedAt: completedAt}
		}
		states[wsID][appID] = appState
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
					app.Route = appState.route
					app.LastOperation = appState.operation
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
		SET active = ?, route_pattern = ?, route_version = ?, operation_id = ?, correlation_id = ?, operation_kind = ?, operation_created_at = ?, operation_completed_at = ?
		WHERE workspace_id = ? AND id = ?
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare state save: %w", err)
	}
	defer stmt.Close()
	workspaceStmt, err := tx.Prepare(`
		UPDATE workspaces
		SET route_version = ?, operation_id = ?, correlation_id = ?, operation_kind = ?, operation_created_at = ?, operation_completed_at = ?
		WHERE id = ?
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare workspace state save: %w", err)
	}
	defer workspaceStmt.Close()

	for _, ws := range workspaces {
		if _, err := workspaceStmt.Exec(ws.Route.RouteVersion, ws.Route.OperationID, operationCorrelation(ws.LastOperation), operationKind(ws.LastOperation), operationCreatedAt(ws.LastOperation), operationCompletedAt(ws.LastOperation), ws.WorkspaceID); err != nil {
			return fmt.Errorf("failed to save workspace state for %s: %w", ws.WorkspaceID, err)
		}
		for _, app := range ws.Applications {
			if _, err := stmt.Exec(boolInt(app.Active), ptrValue(app.RoutePattern), app.Route.RouteVersion, app.Route.OperationID, operationCorrelation(app.LastOperation), operationKind(app.LastOperation), operationCreatedAt(app.LastOperation), operationCompletedAt(app.LastOperation), ws.WorkspaceID, app.ID); err != nil {
				return fmt.Errorf("failed to save state for %s/%s: %w", ws.WorkspaceID, app.ID, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit state save: %w", err)
	}
	return nil
}

// BeginRouteOperation writes a route operation before the proxy is touched.
// It is the write-ahead portion of the route transaction and is deliberately
// separate from SaveState so a daemon restart can recover an operation that
// stopped after proxy reload but before the desired state commit.
func BeginRouteOperation(operation models.RouteOperationJournal) error {
	return saveRouteOperation(operation)
}

// UpdateRouteOperation advances a previously written route journal entry.
func UpdateRouteOperation(operation models.RouteOperationJournal) error {
	return saveRouteOperation(operation)
}

func saveRouteOperation(operation models.RouteOperationJournal) error {
	db, err := readyDB()
	if err != nil {
		return err
	}
	defer db.Close()

	prior, err := json.Marshal(operation.PriorState)
	if err != nil {
		return fmt.Errorf("marshal route operation prior state: %w", err)
	}
	candidate, err := json.Marshal(operation.CandidateState)
	if err != nil {
		return fmt.Errorf("marshal route operation candidate state: %w", err)
	}
	if operation.CreatedAt.IsZero() {
		operation.CreatedAt = time.Now().UTC()
	}
	if operation.UpdatedAt.IsZero() {
		operation.UpdatedAt = operation.CreatedAt
	}
	_, err = db.Exec(`
		INSERT INTO route_operations (
			operation_id, workspace_id, idempotency_key, fingerprint, correlation_id,
			expected_version, route_version, phase, status, error, degraded, rollback_status,
			created_at, updated_at, prior_state, candidate_state, result_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(operation_id) DO UPDATE SET
			workspace_id = excluded.workspace_id,
			idempotency_key = excluded.idempotency_key,
			fingerprint = excluded.fingerprint,
			correlation_id = excluded.correlation_id,
			expected_version = excluded.expected_version,
			route_version = excluded.route_version,
			phase = excluded.phase,
			status = excluded.status,
			error = excluded.error,
			degraded = excluded.degraded,
			rollback_status = excluded.rollback_status,
			created_at = excluded.created_at,
			updated_at = excluded.updated_at,
			prior_state = excluded.prior_state,
			candidate_state = excluded.candidate_state,
			result_json = excluded.result_json
	`, operation.OperationID, operation.WorkspaceID, operation.IdempotencyKey, operation.Fingerprint, operation.CorrelationID,
		operation.ExpectedVersion, operation.RouteVersion, operation.Phase, operation.Status, operation.Error, boolInt(operation.Degraded), operation.RollbackStatus,
		operation.CreatedAt.UTC().Format(time.RFC3339Nano), operation.UpdatedAt.UTC().Format(time.RFC3339Nano), prior, candidate, operation.Result)
	if err != nil {
		return fmt.Errorf("persist route operation %q: %w", operation.OperationID, err)
	}
	return nil
}

// LoadRouteOperations returns the durable route journal in update order.
func LoadRouteOperations() ([]models.RouteOperationJournal, error) {
	db, err := readyDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT operation_id, workspace_id, idempotency_key, fingerprint, correlation_id,
			expected_version, route_version, phase, status, error, degraded, rollback_status,
			created_at, updated_at, prior_state, candidate_state, result_json
		FROM route_operations ORDER BY updated_at, operation_id
	`)
	if err != nil {
		return nil, fmt.Errorf("load route operation journal: %w", err)
	}
	defer rows.Close()

	var operations []models.RouteOperationJournal
	for rows.Next() {
		var operation models.RouteOperationJournal
		var expectedVersion, routeVersion int64
		var degraded int
		var createdAt, updatedAt sql.NullString
		var prior, candidate []byte
		if err := rows.Scan(&operation.OperationID, &operation.WorkspaceID, &operation.IdempotencyKey, &operation.Fingerprint, &operation.CorrelationID,
			&expectedVersion, &routeVersion, &operation.Phase, &operation.Status, &operation.Error, &degraded, &operation.RollbackStatus,
			&createdAt, &updatedAt, &prior, &candidate, &operation.Result); err != nil {
			return nil, fmt.Errorf("scan route operation journal: %w", err)
		}
		operation.ExpectedVersion = uint64(expectedVersion)
		operation.RouteVersion = uint64(routeVersion)
		operation.Degraded = degraded != 0
		operation.CreatedAt = parseStoredTime(createdAt)
		operation.UpdatedAt = parseStoredTime(updatedAt)
		if len(prior) > 0 {
			if err := json.Unmarshal(prior, &operation.PriorState); err != nil {
				return nil, fmt.Errorf("decode route operation prior state: %w", err)
			}
		}
		if len(candidate) > 0 {
			if err := json.Unmarshal(candidate, &operation.CandidateState); err != nil {
				return nil, fmt.Errorf("decode route operation candidate state: %w", err)
			}
		}
		operations = append(operations, operation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate route operation journal: %w", err)
	}
	return operations, nil
}

// ReplaceWorkspaces atomically replaces the durable workspace projection.
// Callers construct and validate the complete desired state before invoking it.
func ReplaceWorkspaces(workspaces []*models.Workspace) error {
	db, err := readyDB()
	if err != nil {
		return err
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin workspace replacement: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM workspaces`); err != nil {
		return fmt.Errorf("failed to clear workspaces: %w", err)
	}
	for order, workspace := range workspaces {
		if err := insertWorkspaceConfigTx(tx, workspaceConfigFromWorkspace(workspace), order); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit workspace replacement: %w", err)
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
		route        models.RouteState
		operation    *models.OperationMetadata
	}
	oldStates := make(map[string]oldState)
	rows, err := db.Query(`
		SELECT id, active, route_pattern, strip_origin, route_version, operation_id, correlation_id, operation_kind, operation_created_at, operation_completed_at
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
		var routeVersion int64
		var operationID, correlationID, operationKind string
		var operationCreatedAt, operationCompletedAt sql.NullString
		if err := rows.Scan(&id, &active, &routePattern, &stripOrigin, &routeVersion, &operationID, &correlationID, &operationKind, &operationCreatedAt, &operationCompletedAt); err != nil {
			rows.Close()
			return fmt.Errorf("failed to scan existing application state: %w", err)
		}
		previousState := oldState{
			active: active != 0, routePattern: nullStringPtr(routePattern),
			stripOrigin: stripOrigin != 0,
		}
		previousState.route.RouteVersion = uint64(routeVersion)
		previousState.route.OperationID = operationID
		if operationID != "" {
			previousState.operation = &models.OperationMetadata{OperationID: operationID, CorrelationID: correlationID, RouteVersion: uint64(routeVersion), Kind: operationKind, CreatedAt: parseStoredTime(operationCreatedAt), CompletedAt: parseStoredTime(operationCompletedAt)}
		}
		oldStates[id] = previousState
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
			if app.RoutePattern == nil {
				app.RoutePattern = old.routePattern
			}
			app.Route = old.route
			app.LastOperation = old.operation
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
			sort_order INTEGER NOT NULL DEFAULT 0,
			route_version INTEGER NOT NULL DEFAULT 0,
			operation_id TEXT NOT NULL DEFAULT '',
			correlation_id TEXT NOT NULL DEFAULT '',
			operation_kind TEXT NOT NULL DEFAULT '',
			operation_created_at TEXT,
			operation_completed_at TEXT
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
			strip_origin INTEGER NOT NULL DEFAULT 0,
			sort_order INTEGER NOT NULL DEFAULT 0,
			route_version INTEGER NOT NULL DEFAULT 0,
			operation_id TEXT NOT NULL DEFAULT '',
			correlation_id TEXT NOT NULL DEFAULT '',
			operation_kind TEXT NOT NULL DEFAULT '',
			operation_created_at TEXT,
			operation_completed_at TEXT,
			PRIMARY KEY (workspace_id, id),
			FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS application_identities (
			app_id TEXT PRIMARY KEY,
			service_id TEXT NOT NULL,
			repository TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS application_aliases (
			alias TEXT PRIMARY KEY,
			app_id TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (app_id) REFERENCES application_identities(app_id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS route_operations (
			operation_id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			idempotency_key TEXT NOT NULL DEFAULT '',
			fingerprint TEXT NOT NULL DEFAULT '',
			correlation_id TEXT NOT NULL DEFAULT '',
			expected_version INTEGER NOT NULL DEFAULT 0,
			route_version INTEGER NOT NULL DEFAULT 0,
			phase TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			degraded INTEGER NOT NULL DEFAULT 0,
			rollback_status TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			prior_state BLOB,
			candidate_state BLOB,
			result_json BLOB
		)`,
		`CREATE INDEX IF NOT EXISTS route_operations_workspace_idx
			ON route_operations(workspace_id, updated_at)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS route_operations_idempotency_idx
			ON route_operations(workspace_id, idempotency_key)
			WHERE idempotency_key <> '' AND status = 'committed'`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("failed to initialize sqlite schema: %w", err)
		}
	}
	// Existing installations predate operation metadata. Keep the migration
	// additive so legacy SQLite files continue to load without a reset.
	for _, migration := range []struct {
		table  string
		column string
		def    string
	}{
		{"workspaces", "route_version", "INTEGER NOT NULL DEFAULT 0"},
		{"workspaces", "operation_id", "TEXT NOT NULL DEFAULT ''"},
		{"workspaces", "correlation_id", "TEXT NOT NULL DEFAULT ''"},
		{"workspaces", "operation_kind", "TEXT NOT NULL DEFAULT ''"},
		{"workspaces", "operation_created_at", "TEXT"},
		{"workspaces", "operation_completed_at", "TEXT"},
		{"applications", "route_version", "INTEGER NOT NULL DEFAULT 0"},
		{"applications", "operation_id", "TEXT NOT NULL DEFAULT ''"},
		{"applications", "correlation_id", "TEXT NOT NULL DEFAULT ''"},
		{"applications", "operation_kind", "TEXT NOT NULL DEFAULT ''"},
		{"applications", "operation_created_at", "TEXT"},
		{"applications", "operation_completed_at", "TEXT"},
		{"route_operations", "rollback_status", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := ensureColumn(db, migration.table, migration.column, migration.def); err != nil {
			return err
		}
	}
	return backfillApplicationIdentities(db)
}

func ensureColumn(db *sql.DB, table, column, definition string) error {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return fmt.Errorf("failed to inspect %s schema: %w", table, err)
	}
	defer rows.Close()
	var found bool
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("failed to inspect %s schema: %w", table, err)
		}
		if name == column {
			found = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to inspect %s schema: %w", table, err)
	}
	if found {
		return nil
	}
	if _, err := db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + definition); err != nil {
		return fmt.Errorf("failed to migrate %s.%s: %w", table, column, err)
	}
	return nil
}

// backfillApplicationIdentities makes existing workspace-scoped application
// rows first-class identities without requiring a manual database migration.
func backfillApplicationIdentities(db *sql.DB) error {
	_, err := db.Exec(`
		INSERT INTO application_identities (app_id, service_id)
		SELECT DISTINCT id, id
		FROM applications
		WHERE id <> ''
		  AND NOT EXISTS (
			SELECT 1 FROM application_identities identities WHERE identities.app_id = applications.id
		  )
	`)
	if err != nil {
		return fmt.Errorf("failed to backfill application identities: %w", err)
	}
	return nil
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
		SELECT nginx_conf_dir, nginx_binary, health_check_interval_secs, certificate_lifetime_days, rementor_domain
		FROM app_config
		WHERE id = 1
	`).Scan(&cfg.NginxConfDir, &cfg.NginxBinary, &cfg.HealthCheckIntervalSecs, &cfg.CertificateLifetimeDays, &cfg.RementorDomain)
	if err != nil {
		return AppConfig{}, fmt.Errorf("failed to load sqlite config: %w", err)
	}
	return cfg, nil
}

func saveConfig(db *sql.DB, cfg AppConfig) error {
	_, err := db.Exec(`
		INSERT INTO app_config (
			id, nginx_conf_dir, nginx_binary, health_check_interval_secs, certificate_lifetime_days, rementor_domain
		) VALUES (1, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			nginx_conf_dir = excluded.nginx_conf_dir,
			nginx_binary = excluded.nginx_binary,
			health_check_interval_secs = excluded.health_check_interval_secs,
			certificate_lifetime_days = excluded.certificate_lifetime_days,
			rementor_domain = excluded.rementor_domain
	`, cfg.NginxConfDir, cfg.NginxBinary, cfg.HealthCheckIntervalSecs, cfg.CertificateLifetimeDays, cfg.RementorDomain)
	if err != nil {
		return fmt.Errorf("failed to save sqlite config: %w", err)
	}
	return nil
}

func defaultConfig() AppConfig {
	return AppConfig{
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
		SELECT id, type, name, color, routing_mode, local_domain, default_remote_base_url, route_version, operation_id, correlation_id, operation_kind, operation_created_at, operation_completed_at
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
		var routeVersion int64
		var operationID, correlationID, operationKind string
		var operationCreatedAt, operationCompletedAt sql.NullString
		if err := rows.Scan(&ws.ID, &ws.Type, &ws.Name, &ws.Color, &ws.Routing.Mode, &ws.Routing.LocalDomain, &ws.Routing.DefaultRemoteBaseURL, &routeVersion, &operationID, &correlationID, &operationKind, &operationCreatedAt, &operationCompletedAt); err != nil {
			return nil, fmt.Errorf("failed to scan sqlite workspace: %w", err)
		}
		ws.Route.RouteVersion = uint64(routeVersion)
		ws.Route.OperationID = operationID
		if operationID != "" {
			ws.LastOperation = &models.OperationMetadata{OperationID: operationID, CorrelationID: correlationID, RouteVersion: uint64(routeVersion), Kind: operationKind, CreatedAt: parseStoredTime(operationCreatedAt), CompletedAt: parseStoredTime(operationCompletedAt)}
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
			a.id, a.name, a.path, a.domain, a.remote_base_url, a.port, a.health, a.active, a.route_pattern, a.context, a.strip_origin,
			COALESCE(i.app_id, a.id), COALESCE(i.service_id, a.id), COALESCE(i.repository, ''),
			a.route_version, a.operation_id, a.correlation_id, a.operation_kind, a.operation_created_at, a.operation_completed_at
		FROM applications a
		LEFT JOIN application_identities i ON i.app_id = a.id
		WHERE a.workspace_id = ?
		ORDER BY a.sort_order, a.id
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
		var routeVersion int64
		var operationID, correlationID, operationKind string
		var operationCreatedAt, operationCompletedAt sql.NullString
		if err := rows.Scan(
			&app.ID, &app.Name, &app.Path, &app.Domain, &app.RemoteBaseUrl, &app.Port, &app.Health, &active,
			&routePattern, &app.Context, &app.StripOrigin, &app.AppID, &app.ServiceID, &app.Repository,
			&routeVersion, &operationID, &correlationID, &operationKind, &operationCreatedAt, &operationCompletedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan sqlite application: %w", err)
		}
		app.Active = active != 0
		app.RoutePattern = nullStringPtr(routePattern)
		app.Route.RouteVersion = uint64(routeVersion)
		app.Route.OperationID = operationID
		if operationID != "" {
			app.LastOperation = &models.OperationMetadata{OperationID: operationID, CorrelationID: correlationID, RouteVersion: uint64(routeVersion), Kind: operationKind, CreatedAt: parseStoredTime(operationCreatedAt), CompletedAt: parseStoredTime(operationCompletedAt)}
		}
		apps = append(apps, app)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate sqlite applications: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("failed to close sqlite applications for %q: %w", wsID, err)
	}
	for i := range apps {
		apps[i].Aliases, err = loadApplicationAliases(db, apps[i].AppID)
		if err != nil {
			return nil, err
		}
	}
	return apps, nil
}

func loadApplicationAliases(db *sql.DB, appID string) ([]string, error) {
	rows, err := db.Query(`SELECT alias FROM application_aliases WHERE app_id = ? ORDER BY alias`, appID)
	if err != nil {
		return nil, fmt.Errorf("failed to load aliases for %q: %w", appID, err)
	}
	defer rows.Close()
	var aliases []string
	for rows.Next() {
		var alias string
		if err := rows.Scan(&alias); err != nil {
			return nil, fmt.Errorf("failed to scan alias for %q: %w", appID, err)
		}
		aliases = append(aliases, alias)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate aliases for %q: %w", appID, err)
	}
	return aliases, nil
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
			WorkspaceID:   wsConfig.ID,
			Type:          wsConfig.Type,
			Name:          stringPtr(name),
			Color:         stringPtr(color),
			Applications:  convertAppConfigs(wsConfig.Applications),
			Route:         wsConfig.Route,
			LastOperation: wsConfig.LastOperation,
		}
		if wsConfig.Type != models.WorkspaceTypeLocalApps {
			ws.RoutingConfig = &wsConfig.Routing
		}
		ws.SetDefaults()
		workspaces = append(workspaces, ws)
	}
	return workspaces
}

func workspaceConfigFromWorkspace(workspace *models.Workspace) models.WorkspaceConfig {
	config := models.WorkspaceConfig{
		ID:            workspace.WorkspaceID,
		Type:          workspace.GetType(),
		Name:          workspace.NameOrID(),
		Applications:  make([]models.ApplicationConfig, 0, len(workspace.Applications)),
		Route:         workspace.Route,
		LastOperation: workspace.LastOperation,
	}
	if workspace.Color != nil {
		config.Color = *workspace.Color
	}
	if workspace.RoutingConfig != nil {
		config.Routing = *workspace.RoutingConfig
	}
	for _, app := range workspace.Applications {
		config.Applications = append(config.Applications, models.ApplicationConfig{
			ID: app.ID, AppID: app.CanonicalAppID(), ServiceID: app.ServiceID, Repository: app.Repository, Aliases: app.NormalizedAliases(), Name: app.Name, Path: app.Path, Domain: app.Domain,
			RemoteBaseUrl: app.RemoteBaseUrl, Port: app.Port, Health: app.Health,
			Active: app.Active, RoutePattern: app.RoutePattern, Context: app.Context,
			StripOrigin: app.StripOrigin,
			Route:       app.Route, LastOperation: app.LastOperation,
		})
	}
	return config
}

func convertAppConfigs(configs []models.ApplicationConfig) []*models.Application {
	var apps []*models.Application
	for _, appConfig := range configs {
		canonical := models.NormalizeIdentityToken(appConfig.CanonicalAppID())
		serviceID := models.NormalizeIdentityToken(appConfig.ServiceID)
		if serviceID == "" {
			serviceID = canonical
		}
		health := appConfig.Health
		if health == "" {
			health = models.DefaultHealthEndpoint
		}

		path := appConfig.Path
		if path != "" && path[0] != '/' {
			path = "/" + path
		}

		app := &models.Application{
			ID:            canonical,
			AppID:         canonical,
			ServiceID:     serviceID,
			Repository:    appConfig.Repository,
			Aliases:       appConfig.Aliases,
			Name:          appConfig.Name,
			Path:          path,
			Domain:        appConfig.Domain,
			RemoteBaseUrl: appConfig.RemoteBaseUrl,
			Context:       appConfig.Context,
			Health:        health,
			Port:          appConfig.Port,
			Active:        appConfig.Active,
			RoutePattern:  appConfig.RoutePattern,
			StripOrigin:   appConfig.StripOrigin,
			Route:         appConfig.Route,
			LastOperation: appConfig.LastOperation,
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
			id, type, name, color, routing_mode, local_domain, default_remote_base_url, sort_order,
			route_version, operation_id, correlation_id, operation_kind, operation_created_at, operation_completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, ws.ID, ws.Type, ws.Name, ws.Color, ws.Routing.Mode, ws.Routing.LocalDomain, ws.Routing.DefaultRemoteBaseURL, order,
		ws.Route.RouteVersion, ws.Route.OperationID, operationCorrelation(ws.LastOperation), operationKind(ws.LastOperation), operationCreatedAt(ws.LastOperation), operationCompletedAt(ws.LastOperation))
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
	appID := models.NormalizeIdentityToken(app.CanonicalAppID())
	if appID == "" {
		return fmt.Errorf("application %s/%s: canonical app ID is required", wsID, app.ID)
	}
	serviceID := models.NormalizeIdentityToken(app.ServiceID)
	if err := ensureApplicationIdentityTx(tx, appID, serviceID, app.Repository, app.Aliases); err != nil {
		return fmt.Errorf("failed to persist identity for %s/%s: %w", wsID, appID, err)
	}
	health := app.Health
	if health == "" {
		health = models.DefaultHealthEndpoint
	}

	_, err := tx.Exec(`
		INSERT INTO applications (
			workspace_id, id, name, path, domain, remote_base_url, port, health, active, route_pattern, context,
			strip_origin, sort_order, route_version, operation_id, correlation_id, operation_kind, operation_created_at, operation_completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, wsID, appID, app.Name, app.Path, app.Domain, app.RemoteBaseUrl, app.Port, health, boolInt(app.Active),
		ptrValue(app.RoutePattern), app.Context, boolInt(app.StripOrigin), order, app.Route.RouteVersion, app.Route.OperationID,
		operationCorrelation(app.LastOperation), operationKind(app.LastOperation), operationCreatedAt(app.LastOperation), operationCompletedAt(app.LastOperation))
	if err != nil {
		return fmt.Errorf("failed to insert application %s/%s: %w", wsID, appID, err)
	}
	return nil
}

func ensureApplicationIdentityTx(tx *sql.Tx, appID, serviceID, repository string, aliases []string) error {
	appID = models.NormalizeIdentityToken(appID)
	serviceID = models.NormalizeIdentityToken(serviceID)
	repository = models.NormalizeIdentityToken(repository)
	if appID == "" {
		return fmt.Errorf("application ID must not be empty")
	}
	var aliasOwner string
	err := tx.QueryRow(`SELECT app_id FROM application_aliases WHERE alias = ?`, appID).Scan(&aliasOwner)
	if err == nil && aliasOwner != appID {
		return &models.AliasConflictError{Alias: appID, ExistingAppID: aliasOwner, RequestedAppID: appID}
	}
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	var existingService, existingRepository string
	err = tx.QueryRow(`SELECT service_id, repository FROM application_identities WHERE app_id = ?`, appID).Scan(&existingService, &existingRepository)
	switch {
	case err == nil:
		// A binding may only carry its app ID when it reuses an identity from
		// another workspace. Preserve the globally registered service ID rather
		// than interpreting the omitted value as a request to change it.
		if serviceID == "" {
			serviceID = existingService
		}
		if existingService != serviceID {
			return fmt.Errorf("application %q already uses service ID %q", appID, existingService)
		}
		if repository == "" {
			repository = existingRepository
		}
		if _, err := tx.Exec(`UPDATE application_identities SET repository = ? WHERE app_id = ?`, repository, appID); err != nil {
			return err
		}
	case err == sql.ErrNoRows:
		if serviceID == "" {
			serviceID = appID
		}
		if _, err := tx.Exec(`INSERT INTO application_identities (app_id, service_id, repository) VALUES (?, ?, ?)`, appID, serviceID, repository); err != nil {
			return err
		}
	default:
		return err
	}
	for _, rawAlias := range aliases {
		alias := models.NormalizeIdentityToken(rawAlias)
		if alias == "" || alias == appID {
			continue
		}
		var owner string
		err := tx.QueryRow(`SELECT app_id FROM application_aliases WHERE alias = ?`, alias).Scan(&owner)
		if err == nil && owner != appID {
			return &models.AliasConflictError{Alias: alias, ExistingAppID: owner, RequestedAppID: appID}
		}
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		var canonicalOwner string
		err = tx.QueryRow(`SELECT app_id FROM application_identities WHERE app_id = ?`, alias).Scan(&canonicalOwner)
		if err == nil && canonicalOwner != appID {
			return &models.AliasConflictError{Alias: alias, ExistingAppID: canonicalOwner, RequestedAppID: appID}
		}
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO application_aliases (alias, app_id) VALUES (?, ?) ON CONFLICT(alias) DO UPDATE SET app_id = excluded.app_id`, alias, appID); err != nil {
			return err
		}
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

func parseStoredTime(value sql.NullString) time.Time {
	if !value.Valid || value.String == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func operationCorrelation(operation *models.OperationMetadata) string {
	if operation == nil {
		return ""
	}
	return operation.CorrelationID
}

func operationKind(operation *models.OperationMetadata) string {
	if operation == nil {
		return ""
	}
	return operation.Kind
}

func operationCreatedAt(operation *models.OperationMetadata) any {
	if operation == nil || operation.CreatedAt.IsZero() {
		return nil
	}
	return operation.CreatedAt.UTC().Format(time.RFC3339Nano)
}

func operationCompletedAt(operation *models.OperationMetadata) any {
	if operation == nil || operation.CompletedAt.IsZero() {
		return nil
	}
	return operation.CompletedAt.UTC().Format(time.RFC3339Nano)
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
