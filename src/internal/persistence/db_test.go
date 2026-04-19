package persistence_test

import (
	"database/sql"
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
