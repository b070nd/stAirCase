package persistence

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	// modernc.org/sqlite is a pure-Go SQLite driver, required for CGO_ENABLED=0 builds
	_ "modernc.org/sqlite"
)

// InitDB ensures the directory exists, opens the SQLite database, runs migrations, and enables WAL.
func InitDB(workspaceDir string) (*sql.DB, error) {
	dbPath := filepath.Join(workspaceDir, "workspace.db")

	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create workspace dir: %w", err)
	}

	// Safely inject SQLite pragmas in the connection string.
	// busy_timeout(5000) prevents database locking errors during concurrent IPC writes.
	// journal_mode(WAL) & synchronous(NORMAL) ensures high-throughput safety.
	connStr := dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"

	db, err := sql.Open("sqlite", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Explicitly enforce foreign keys at the connection level
	if _, err := db.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
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
	if recorded == 0 && len(Migrations) > 0 {
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
			if _, err := db.Exec(m); err != nil {
				return nil, fmt.Errorf("apply migration %d: %w", i, err)
			}
			if _, err := db.Exec(`INSERT INTO schema_migrations(idx) VALUES (?)`, i); err != nil {
				return nil, fmt.Errorf("record migration %d: %w", i, err)
			}
		}
	}

	return db, nil
}