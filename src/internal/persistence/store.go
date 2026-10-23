package persistence

import (
	"database/sql"
	"sync"

	"github.com/b070nd/stAirCase/src/internal/domain"
)

// Store provides all persistence operations for stAirCase.
type Store struct {
	db *sql.DB
	// appendMu serializes the read-last-hash + insert in AppendEventLogChained
	// so two concurrent appenders (e.g. the IPC server goroutine and the
	// orchestrator run loop) cannot read the same prevHash and fork the
	// tamper-proof audit chain.
	appendMu sync.Mutex
}

// NewStore creates a Store backed by an open database connection.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// DB returns the underlying *sql.DB for use in tests and migration tooling.
// Production code should prefer the typed Store methods.
func (s *Store) DB() *sql.DB { return s.db }

// Vendor

// Run is a convenience alias so cmd packages can use persistence.Run directly.
type Run = domain.Run

// Project is a convenience alias so cmd packages can use persistence.Project directly.
type Project = domain.Project

// ProjectDependency is a convenience alias so cmd packages can use persistence.ProjectDependency directly.
type ProjectDependency = domain.ProjectDependency
