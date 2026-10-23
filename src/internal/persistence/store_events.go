package persistence

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/b070nd/stAirCase/src/internal/chain"
	"github.com/b070nd/stAirCase/src/internal/domain"
)

// GetLastEventHash returns the hash of the most recent log entry for a run,
// or an empty string if no entries exist yet (genesis / first entry).
func (s *Store) GetLastEventHash(runID int64) (string, error) {
	var hash string
	err := s.db.QueryRow(
		`SELECT event_hash FROM run_event_logs WHERE run_id = ? ORDER BY id DESC LIMIT 1`, runID,
	).Scan(&hash)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return hash, err
}

// AppendEventLog writes a tamper-proof entry: hash = SHA-256 over event type, payload, prevHash and gitCommitHash (chain.Version).
// gitCommitHash is stored per-entry so that inspect log can verify each entry
// with the exact value that was current when the entry was written - the hash
// changes from "" to the real commit hash at teardown, so a single run-level
// value cannot be used for verification of all entries.
func (s *Store) AppendEventLog(runID int64, eventType, payload, prevHash, gitCommitHash string) (*domain.RunEventLog, error) {
	if err := chain.CheckSize(eventType, payload); err != nil {
		return nil, err
	}
	eventHash := chain.HashV2(eventType, payload, prevHash, gitCommitHash)
	now := time.Now()
	res, err := s.db.Exec(
		`INSERT INTO run_event_logs (run_id, event_type, payload, timestamp, event_hash, git_commit_hash, hash_version)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		runID, eventType, payload, now, eventHash, gitCommitHash, chain.Version,
	)
	if err != nil {
		return nil, fmt.Errorf("append event log: %w", err)
	}
	id, _ := res.LastInsertId()
	return &domain.RunEventLog{
		ID:            id,
		RunID:         runID,
		EventType:     eventType,
		Payload:       payload,
		Timestamp:     now,
		EventHash:     eventHash,
		GitCommitHash: gitCommitHash,
		HashVersion:   chain.Version,
	}, nil
}

// AppendEventLogChained atomically reads the run's last event hash and appends a new entry chained to it. appendMu serializes the read
// and insert, so concurrent callers in this process cannot fork the chain. history.Writer uses these methods, and so should anything
// else; the lower-level AppendEventLog (explicit prevHash) is what they insert with, and tests use it to build chains. One process is
// enough because the no_concurrent_run gate allows only one run to write a workspace's event log at a time.
func (s *Store) AppendEventLogChained(runID int64, eventType, payload, gitCommitHash string) (*domain.RunEventLog, error) {
	s.appendMu.Lock()
	defer s.appendMu.Unlock()
	prevHash, err := s.GetLastEventHash(runID)
	if err != nil {
		return nil, fmt.Errorf("read last event hash: %w", err)
	}
	return s.AppendEventLog(runID, eventType, payload, prevHash, gitCommitHash)
}

// AppendEventLogChainedIf is AppendEventLogChained for an entry that must not be written once a condition stops holding: proceed is
// called under the append lock just before the insert, and if it returns an error nothing is written and (nil, that error) is returned.
// The check cannot be atomic with the insert, which is not interruptible, so an insert that has begun is written. The caller gets
// "checked under the lock, right before the write", with no wait for another writer in between.
func (s *Store) AppendEventLogChainedIf(runID int64, eventType, payload, gitCommitHash string, proceed func() error) (*domain.RunEventLog, error) {
	s.appendMu.Lock()
	defer s.appendMu.Unlock()
	if err := proceed(); err != nil {
		return nil, err
	}
	prevHash, err := s.GetLastEventHash(runID)
	if err != nil {
		return nil, fmt.Errorf("read last event hash: %w", err)
	}
	return s.AppendEventLog(runID, eventType, payload, prevHash, gitCommitHash)
}

func (s *Store) ListEventLogs(runID int64) ([]domain.RunEventLog, error) {
	rows, err := s.db.Query(
		`SELECT id, run_id, event_type, payload, timestamp, event_hash, git_commit_hash, hash_version
		 FROM run_event_logs WHERE run_id = ? ORDER BY id ASC`, runID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var logs []domain.RunEventLog
	for rows.Next() {
		var l domain.RunEventLog
		if err := rows.Scan(&l.ID, &l.RunID, &l.EventType, &l.Payload, &l.Timestamp, &l.EventHash, &l.GitCommitHash, &l.HashVersion); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

// VerifyChain recomputes every event hash for the run and returns the first
// position (1-based) where the stored hash does not match the recomputed value.
// Returns nil when the chain is intact.
//
// It is used by "staircase audit verify" and the tamper test.
func (s *Store) VerifyChain(runID int64) error {
	logs, err := s.ListEventLogs(runID)
	if err != nil {
		return fmt.Errorf("verify chain: list events: %w", err)
	}
	if err := chain.Verify(logs); err != nil {
		return fmt.Errorf("verify chain: %w", err)
	}
	return nil
}

// ListVerifiedEventLogs is the run's audit chain read once and verified as read: the
// slice returned is the slice that was verified, so a caller that goes on to
// believe it cannot be handed rows that changed between a check and a read.
func (s *Store) ListVerifiedEventLogs(runID int64) ([]domain.RunEventLog, error) {
	logs, err := s.ListEventLogs(runID)
	if err != nil {
		return nil, fmt.Errorf("verify chain: list events: %w", err)
	}
	if err := chain.Verify(logs); err != nil {
		return nil, fmt.Errorf("verify chain: %w", err)
	}
	return logs, nil
}
