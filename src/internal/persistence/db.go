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

	// The database holds every proposal, decision and ciphertext: owner-only,
	// whatever the folder's own mode or umask (SQLite gives its -wal and -shm files
	// the database's mode). An existing workspace is tightened too.
	if f, err := os.OpenFile(dbPath, os.O_CREATE|os.O_RDWR, 0o600); err == nil {
		_ = f.Close()
	}
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		_ = os.Chmod(p, 0o600) // best effort; a missing file or Windows is fine
	}

	// The pragmas are in the connection string so every pooled connection gets them:
	//   busy_timeout(5000)                    wait out brief write locks instead of failing.
	//   journal_mode(WAL), synchronous(FULL)  a commit is on disk before it returns: a decision is acknowledged to the agent only after
	//                                         it is on the audit chain, so it must survive a power cut.
	//   foreign_keys(1)                       enforcement is per connection; a one-shot PRAGMA covered only one.
	//   _txlock=immediate                     take the write lock at BEGIN, where busy_timeout applies. A deferred read-then-write
	//                                         transaction gets SQLITE_BUSY at once if another writer committed in between, so a
	//                                         concurrent run could be recorded as failed after succeeding.
	connStr := dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=foreign_keys(1)&_txlock=immediate"

	db, err := sql.Open("sqlite", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := refuseNewerWorkspace(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	freshSchema, err := isFreshSchema(db)
	if err != nil {
		return nil, fmt.Errorf("inspect schema: %w", err)
	}

	// Apply the full schema DDL
	if _, err := db.Exec(Schema); err != nil {
		return nil, fmt.Errorf("failed to apply schema: %w", err)
	}

	// Apply additive migrations once, tracked by zero-based index. A brand-new database already has every migration column in the base
	// schema, so all indices are pre-recorded; an existing one only runs and records the missing ones.
	var recorded int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&recorded); err != nil {
		return nil, fmt.Errorf("count schema_migrations: %w", err)
	}
	if freshSchema && recorded == 0 && len(Migrations) > 0 {
		// Fresh database: Schema already contains all columns - mark all as applied.
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

// refuseNewerWorkspace fails when the database records a migration this version
// does not have: it was written by a newer stAirCase.
func refuseNewerWorkspace(db *sql.DB) error {
	var newest sql.NullInt64
	if err := db.QueryRow(`SELECT MAX(idx) FROM schema_migrations`).Scan(&newest); err != nil {
		return nil // a new database has no such table yet
	}
	if newest.Valid && int(newest.Int64) >= len(Migrations) {
		return fmt.Errorf("this workspace was written by a newer version of stAirCase (its database has migration %d, this version knows %d): upgrade stAirCase",
			newest.Int64, len(Migrations))
	}
	return nil
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
