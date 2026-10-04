package persistence_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Schema migration tracking ────────────────────────────────────────────────

// TestInitDB_migrations_recorded checks that every entry in Migrations is
// recorded in the schema_migrations table exactly once after InitDB.
func TestInitDB_migrations_recorded(t *testing.T) {
	db, err := persistence.InitDB(t.TempDir())
	require.NoError(t, err)
	defer db.Close()

	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, len(persistence.Migrations), count,
		"every migration must be recorded in schema_migrations")
}

// TestInitDB_migrations_idempotent verifies that calling InitDB a second time
// on the same directory does not duplicate any migration records.
func TestInitDB_migrations_idempotent(t *testing.T) {
	dir := t.TempDir()

	db1, err := persistence.InitDB(dir)
	require.NoError(t, err)
	db1.Close()

	db2, err := persistence.InitDB(dir)
	require.NoError(t, err)
	defer db2.Close()

	var count int
	err = db2.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, len(persistence.Migrations), count,
		"re-initialising must not create duplicate migration rows")
}

// TestInitDB_schema_migrations_table_exists verifies the table itself is
// present and has the expected columns.
func TestInitDB_schema_migrations_table_exists(t *testing.T) {
	db, err := persistence.InitDB(t.TempDir())
	require.NoError(t, err)
	defer db.Close()

	// A successful query against the table proves both table and columns exist.
	row := db.QueryRow(`SELECT idx, applied_at FROM schema_migrations LIMIT 1`)
	var idx int
	var appliedAt sql.NullString
	// The scan may return sql.ErrNoRows if Migrations is empty; that is still fine.
	if scanErr := row.Scan(&idx, &appliedAt); scanErr != nil && scanErr != sql.ErrNoRows {
		t.Fatalf("schema_migrations table or columns missing: %v", scanErr)
	}
}

func TestInitDB_applies_migrations_to_legacy_database_without_records(t *testing.T) {
	dir := t.TempDir()
	legacy, err := sql.Open("sqlite", filepath.Join(dir, "workspace.db"))
	require.NoError(t, err)
	_, err = legacy.Exec(`
		PRAGMA foreign_keys = ON;
		CREATE TABLE vendors (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE projects (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			vendor_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			source_path TEXT,
			FOREIGN KEY (vendor_id) REFERENCES vendors(id) ON DELETE CASCADE,
			UNIQUE(vendor_id, name)
		);
		CREATE TABLE secrets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			key_name TEXT NOT NULL,
			encrypted_value TEXT NOT NULL,
			scoped_to_project_id INTEGER,
			FOREIGN KEY (scoped_to_project_id) REFERENCES projects(id) ON DELETE CASCADE
		);
		CREATE TABLE cases (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL,
			status TEXT NOT NULL DEFAULT 'PENDING',
			last_modified DATETIME DEFAULT CURRENT_TIMESTAMP,
			prd_json TEXT,
			FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
		);
		CREATE TABLE swarm_topologies (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER NOT NULL,
			version INTEGER NOT NULL DEFAULT 1,
			supervisor_name TEXT NOT NULL,
			checkpoint_type TEXT NOT NULL DEFAULT 'memory',
			FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
			UNIQUE(project_id, version)
		);
		CREATE TABLE run_event_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id INTEGER NOT NULL,
			event_type TEXT NOT NULL,
			payload TEXT NOT NULL,
			timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
			event_hash TEXT NOT NULL
		);
	`)
	require.NoError(t, err)
	require.NoError(t, legacy.Close())

	db, err := persistence.InitDB(dir)
	require.NoError(t, err)
	defer db.Close()

	assert.True(t, columnExists(t, db, "swarm_topologies", "runtime_type"))
	assert.True(t, columnExists(t, db, "projects", "webhook_url"))
	assert.True(t, columnExists(t, db, "projects", "default_model"))
	assert.True(t, columnExists(t, db, "projects", "budget_usd_per_run"))
	assert.True(t, columnExists(t, db, "cases", "deleted_at"))
	assert.True(t, columnExists(t, db, "run_event_logs", "git_commit_hash"))
	assert.True(t, indexExists(t, db, "uidx_secrets_global"))
	assert.True(t, indexExists(t, db, "uidx_secrets_project"))
	assert.True(t, columnExists(t, db, "projects", "blueprint_hash"))
	assert.True(t, columnExists(t, db, "cases", "blueprint_hash"))
	assert.True(t, columnExists(t, db, "cases", "blueprint_slug"))
	assert.True(t, columnExists(t, db, "blueprints", "git_sha"))

	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, len(persistence.Migrations), count)
}

func columnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	require.NoError(t, err)
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		require.NoError(t, rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk))
		if name == column {
			return true
		}
	}
	require.NoError(t, rows.Err())
	return false
}

func indexExists(t *testing.T, db *sql.DB, index string) bool {
	t.Helper()
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&name)
	if err == sql.ErrNoRows {
		return false
	}
	require.NoError(t, err)
	return true
}

// TestStore_error_paths_with_closed_db exercises error return paths in Store
// methods by closing the underlying DB.  Each call should return a non-nil
// error, covering the `if err != nil { return nil, err }` branches.
func TestStore_error_paths_with_closed_db(t *testing.T) {
	db, err := persistence.InitDB(t.TempDir())
	require.NoError(t, err)
	store := persistence.NewStore(db)
	db.Close() // all subsequent operations will fail

	_, err = store.ListVendors()
	assert.Error(t, err, "ListVendors on closed db must error")

	_, _, err = store.GetProjectConfig(1)
	assert.Error(t, err, "GetProjectConfig on closed db must error")

	_, err = store.ListAllSecrets()
	assert.Error(t, err, "ListAllSecrets on closed db must error")

	_, err = store.HasSuccessfulRunAtTopologyVersion(1, 1)
	assert.Error(t, err, "HasSuccessfulRunAtTopologyVersion on closed db must error")
}

// TestInitDB_commits_are_durable: every pooled connection runs in WAL mode with
// synchronous=FULL, so a decision that was acknowledged is on disk (F104).
func TestInitDB_commits_are_durable(t *testing.T) {
	db, err := persistence.InitDB(t.TempDir())
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	var conns []*sql.Conn
	for i := 0; i < 3; i++ { // distinct pooled connections
		c, err := db.Conn(ctx)
		require.NoError(t, err)
		conns = append(conns, c)
		defer func() { _ = c.Close() }()
	}
	for _, c := range conns {
		var sync int
		require.NoError(t, c.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&sync))
		assert.Equal(t, 2, sync, "synchronous=FULL")
		var mode string
		require.NoError(t, c.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode))
		assert.Equal(t, "wal", mode)
	}
}

// TestInitDB_refuses_a_workspace_from_a_newer_version: a database that has a
// migration this version does not know was written by a newer stAirCase;
// reading and writing it with older code could damage it.
func TestInitDB_refuses_a_workspace_from_a_newer_version(t *testing.T) {
	dir := t.TempDir()
	db, err := persistence.InitDB(dir)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO schema_migrations(idx) VALUES (?)`, len(persistence.Migrations))
	require.NoError(t, err)
	require.NoError(t, db.Close())

	_, err = persistence.InitDB(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "newer version of stAirCase")
}

// TestInitDB_database_is_private: the database holds every proposal, decision
// and ciphertext of the workspace, so it is readable by its owner only, also
// when the workspace folder already existed with wider permissions.
func TestInitDB_database_is_private(t *testing.T) {
	dir := t.TempDir() // 0700 by TempDir; make it as wide as a folder made by hand
	require.NoError(t, os.Chmod(dir, 0o755))
	db, err := persistence.InitDB(dir)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS probe(x)`) // WAL and shm files exist now
	require.NoError(t, err)
	require.NoError(t, db.Close())
	matches, err := filepath.Glob(filepath.Join(dir, "workspace.db*"))
	require.NoError(t, err)
	require.NotEmpty(t, matches)
	for _, m := range matches {
		info, err := os.Stat(m)
		require.NoError(t, err)
		assert.Zero(t, info.Mode().Perm()&0o077, "%s is %v", filepath.Base(m), info.Mode().Perm())
	}
}
