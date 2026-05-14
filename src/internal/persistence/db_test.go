package persistence_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/b070nd/staircase-core/src/internal/persistence"
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
