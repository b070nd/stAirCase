package persistence

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	// modernc.org/sqlite is a pure-Go SQLite driver, required for CGO_ENABLED=0 builds
	_ "modernc.org/sqlite"
)

// InitDB ensures the directory exists, opens the SQLite database, runs migrations, and enables WAL.
func InitDB(workspaceDir string) (*sql.DB, error) {
	dbPath := filepath.Join(workspaceDir, "workspace.db")

	if err := os.MkdirAll(workspaceDir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create workspace dir: %w", err)
	}

	// Inject SQLite pragmas in the connection string so the driver applies them
	// to EVERY pooled connection, not just the first one.
	//   busy_timeout(5000) — wait out brief write locks instead of erroring.
	//   journal_mode(WAL) & synchronous(NORMAL) — high-throughput durability.
	//   foreign_keys(1) — FK enforcement is per-connection in SQLite; a one-shot
	//     `PRAGMA foreign_keys=ON` only covered the single connection it ran on,
	//     leaving other pooled connections unenforced. Setting it in the DSN
	//     guarantees every connection enforces referential integrity.
	connStr := dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)"

	db, err := sql.Open("sqlite", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	freshSchema, err := isFreshSchema(db)
	if err != nil {
		return nil, fmt.Errorf("inspect schema: %w", err)
	}

	// Apply the full schema DDL
	if _, err := db.Exec(Schema); err != nil {
		return nil, fmt.Errorf("failed to apply schema: %w", err)
	}

	// Apply additive migrations exactly once, tracked by zero-based index.
	//
	// On a brand-new database the base Schema DDL already incorporates every
	// migration column, so we pre-seed schema_migrations with all indices to
	// skip redundant ALTER TABLE calls.  On an existing database some rows will
	// already be present; we only execute and record the missing ones.
	var recorded int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&recorded); err != nil {
		return nil, fmt.Errorf("count schema_migrations: %w", err)
	}
	if freshSchema && recorded == 0 && len(Migrations) > 0 {
		// Fresh database: Schema already contains all columns — mark all as applied.
		for i := range Migrations {
			if _, err := db.Exec(`INSERT OR IGNORE INTO schema_migrations(idx) VALUES (?)`, i); err != nil {
				return nil, fmt.Errorf("seed migration %d: %w", i, err)
			}
		}
	} else {
		// Existing database: apply only migrations whose index is not yet recorded.
		for i, m := range Migrations {
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE idx = ?`, i).Scan(&count); err != nil {
				return nil, fmt.Errorf("check migration %d: %w", i, err)
			}
			if count > 0 {
				continue // already applied
			}
			if _, err := db.Exec(m); err != nil && !isAlreadyAppliedMigrationError(err) {
				return nil, fmt.Errorf("apply migration %d: %w", i, err)
			}
			if _, err := db.Exec(`INSERT INTO schema_migrations(idx) VALUES (?)`, i); err != nil {
				return nil, fmt.Errorf("record migration %d: %w", i, err)
			}
		}
	}

	return db, nil
}

func isFreshSchema(db *sql.DB) (bool, error) {
	var count int
	err := db.QueryRow(`
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'table'
		  AND name NOT LIKE 'sqlite_%'
	`).Scan(&count)
	if err != nil {
		return false, err
	}
	return count == 0, nil
}

func isAlreadyAppliedMigrationError(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "duplicate column name")
}
