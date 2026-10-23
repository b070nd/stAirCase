package persistence

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/b070nd/stAirCase/src/internal/chain"
	"github.com/b070nd/stAirCase/src/internal/domain"
)

func (s *Store) CreateRun(caseID int64, topologyVersion int, gitBranch string) (*domain.Run, error) {
	// Enforce referential integrity: topology_version must correspond to a real
	// swarm_topologies row for the project that owns this case. 0 means the
	// case runs an agent harness, which has no topology.
	// SQLite cannot express this as a FK because topology_version is a semantic
	// version number, not a row ID, so we validate it here instead.
	var topoCount int
	err := s.db.QueryRow(
		`
		SELECT COUNT(*)
		FROM swarm_topologies st
		JOIN cases c ON c.project_id = st.project_id
		WHERE c.id = ? AND st.version = ?`,
		caseID, topologyVersion,
	).Scan(&topoCount)
	if err != nil {
		return nil, fmt.Errorf("create run: validate topology version: %w", err)
	}
	if topoCount == 0 && topologyVersion != 0 {
		return nil, fmt.Errorf("create run: topology version %d does not exist for the project of case %d", topologyVersion, caseID)
	}

	now := time.Now()
	res, err := s.db.Exec(
		`INSERT INTO runs (case_id, topology_version, status, start_time, git_branch) VALUES (?, ?, 'RUNNING', ?, ?)`,
		caseID, topologyVersion, now, gitBranch,
	)
	if err != nil {
		return nil, fmt.Errorf("create run: %w", err)
	}
	id, _ := res.LastInsertId()
	return &domain.Run{
		ID:              id,
		CaseID:          caseID,
		TopologyVersion: topologyVersion,
		Status:          domain.RunStatusRunning,
		StartTime:       now,
		GitBranch:       gitBranch,
	}, nil
}

func (s *Store) GetRun(runID int64) (*domain.Run, error) {
	r := &domain.Run{}
	var endTime sql.NullTime
	err := s.db.QueryRow(
		`SELECT id, case_id, topology_version, status, start_time, end_time, git_branch, COALESCE(git_commit_hash,'')
		 FROM runs WHERE id = ?`, runID,
	).Scan(&r.ID, &r.CaseID, &r.TopologyVersion, &r.Status, &r.StartTime, &endTime, &r.GitBranch, &r.GitCommitHash)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if endTime.Valid {
		r.EndTime = &endTime.Time
	}
	return r, err
}

func (s *Store) UpdateRunStatus(runID int64, status string, endTime *time.Time, commitHash string) error {
	_, err := s.db.Exec(
		`UPDATE runs SET status = ?, end_time = ?, git_commit_hash = ? WHERE id = ?`,
		status, endTime, commitHash, runID,
	)
	return err
}

// FinishRun atomically records a terminal run and its case outcome. Execution
// does not accept stories: a successful case with unverified work stays pending.
func (s *Store) FinishRun(runID int64, status string, endTime time.Time, commitHash string) error {
	if status != RunStatusSuccess && status != RunStatusFailed && status != RunStatusKilled && status != RunStatusAwaitingReview {
		return fmt.Errorf("finish run: invalid terminal status %q", status)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("finish run: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var caseID int64
	if err := tx.QueryRow(`SELECT case_id FROM runs WHERE id = ?`, runID).Scan(&caseID); err != nil {
		return fmt.Errorf("finish run: load case: %w", err)
	}
	caseStatus, err := caseStatusAfter(tx, caseID, status)
	if err != nil {
		return fmt.Errorf("finish run: %w", err)
	}
	if _, err := tx.Exec(`UPDATE runs SET status = ?, end_time = ?, git_commit_hash = ? WHERE id = ?`, status, endTime, commitHash, runID); err != nil {
		return fmt.Errorf("finish run: update run: %w", err)
	}
	if _, err := tx.Exec(`UPDATE cases SET status = ?, last_modified = ? WHERE id = ?`, caseStatus, endTime, caseID); err != nil {
		return fmt.Errorf("finish run: update case: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("finish run: commit: %w", err)
	}
	return nil
}

// caseStatusAfter is the single rule for a case's status after a terminal run:
// success completes the case only once every story is operator-accepted
// (IMPLEMENTED); otherwise it stays pending. Any other run outcome fails it.
func caseStatusAfter(tx *sql.Tx, caseID int64, runStatus string) (string, error) {
	if runStatus == RunStatusAwaitingReview { // the work waits for the owner's review: nothing is decided yet
		return CaseStatusPending, nil
	}
	if runStatus != RunStatusSuccess {
		return CaseStatusFailed, nil
	}
	var remaining int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM user_stories WHERE case_id = ? AND status != 'IMPLEMENTED'`, caseID).Scan(&remaining); err != nil {
		return "", fmt.Errorf("inspect stories: %w", err)
	}
	if remaining > 0 {
		return CaseStatusPending, nil
	}
	return CaseStatusCompleted, nil
}

// AuditEntry is an entry of the audit chain that is made before it is inserted: the event's name and its payload, as history shapes
// them. The store hashes it into the chain; it does not look inside.
type AuditEntry struct{ Type, Payload string }

// AcceptUserStory records an operator's acceptance of a story after they verified delivered work. It needs a successful run of the
// story's case and, in one transaction, marks the story IMPLEMENTED, appends the given entry (the story_accepted event, prepared by
// history) to that run's audit chain, bound to the run's commit, and recomputes the case status. It returns the run the acceptance was
// recorded against and the case status.
func (s *Store) AcceptUserStory(storyID int64, entry AuditEntry) (runID int64, caseStatus string, err error) {
	s.appendMu.Lock() // same serialization as AppendEventLogChained: no chain forks
	defer s.appendMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, "", fmt.Errorf("accept story: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var caseID int64
	if err := tx.QueryRow(`SELECT case_id FROM user_stories WHERE id = ?`, storyID).Scan(&caseID); errors.Is(err, sql.ErrNoRows) {
		return 0, "", fmt.Errorf("story %d not found", storyID)
	} else if err != nil {
		return 0, "", fmt.Errorf("accept story: load story: %w", err)
	}
	var commit string
	err = tx.QueryRow(`SELECT id, COALESCE(git_commit_hash,'') FROM runs WHERE case_id = ? AND status = ? ORDER BY id DESC LIMIT 1`,
		caseID, RunStatusSuccess).Scan(&runID, &commit)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", fmt.Errorf("case %d has no successful run - accept stories only after verifying delivered work", caseID)
	} else if err != nil {
		return 0, "", fmt.Errorf("accept story: load run: %w", err)
	}
	var prevHash string
	if err := tx.QueryRow(`SELECT event_hash FROM run_event_logs WHERE run_id = ? ORDER BY id DESC LIMIT 1`, runID).Scan(&prevHash); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, "", fmt.Errorf("accept story: read last event hash: %w", err)
	}
	if err := chain.CheckSize(entry.Type, entry.Payload); err != nil {
		return 0, "", fmt.Errorf("accept story: %w", err)
	}
	now := time.Now()
	if _, err := tx.Exec(`INSERT INTO run_event_logs (run_id, event_type, payload, timestamp, event_hash, git_commit_hash, hash_version) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		runID, entry.Type, entry.Payload, now, chain.HashV2(entry.Type, entry.Payload, prevHash, commit), commit, chain.Version); err != nil {
		return 0, "", fmt.Errorf("accept story: audit: %w", err)
	}
	if _, err := tx.Exec(`UPDATE user_stories SET status = ? WHERE id = ?`, StoryStatusImplemented, storyID); err != nil {
		return 0, "", fmt.Errorf("accept story: update story: %w", err)
	}
	if caseStatus, err = caseStatusAfter(tx, caseID, RunStatusSuccess); err != nil {
		return 0, "", fmt.Errorf("accept story: %w", err)
	}
	if _, err := tx.Exec(`UPDATE cases SET status = ?, last_modified = ? WHERE id = ?`, caseStatus, now, caseID); err != nil {
		return 0, "", fmt.Errorf("accept story: update case: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, "", fmt.Errorf("accept story: commit: %w", err)
	}
	return runID, caseStatus, nil
}

func (s *Store) ListRunsByCase(caseID int64) ([]domain.Run, error) {
	rows, err := s.db.Query(
		`SELECT id, case_id, topology_version, status, start_time, end_time, git_branch, COALESCE(git_commit_hash,'')
		 FROM runs WHERE case_id = ? ORDER BY id DESC`, caseID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanRuns(rows)
}

// ListRuns is every run of the workspace, newest first.
func (s *Store) ListRuns() ([]domain.Run, error) {
	rows, err := s.db.Query(
		`SELECT id, case_id, topology_version, status, start_time, end_time, git_branch, COALESCE(git_commit_hash,'')
		 FROM runs ORDER BY id DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanRuns(rows)
}

func (s *Store) ListRunsByStatus(status string) ([]domain.Run, error) {
	rows, err := s.db.Query(
		`SELECT id, case_id, topology_version, status, start_time, end_time, git_branch, COALESCE(git_commit_hash,'')
		 FROM runs WHERE status = ?`, status,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanRuns(rows)
}

func scanRuns(rows *sql.Rows) ([]domain.Run, error) {
	var runs []domain.Run
	for rows.Next() {
		var r domain.Run
		var endTime sql.NullTime
		if err := rows.Scan(&r.ID, &r.CaseID, &r.TopologyVersion, &r.Status,
			&r.StartTime, &endTime, &r.GitBranch, &r.GitCommitHash); err != nil {
			return nil, err
		}
		if endTime.Valid {
			r.EndTime = &endTime.Time
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// RunEventLog (SOC2 tamper-proof chain)

// ReopenRun puts a run that was interrupted (RUNNING, or KILLED by a cancellation) and has no commit back to RUNNING,
// for a continuation: its end time is cleared. It refuses any other run, so a finished run cannot be reopened.
func (s *Store) ReopenRun(runID int64) error {
	// An away run that awaits review is reopened when a resumed hosted session continues it (ADR 0008): its review is withdrawn.
	res, err := s.db.Exec(`UPDATE runs SET status = 'RUNNING', end_time = NULL
		WHERE id = ? AND status IN ('RUNNING', 'KILLED', 'AWAITING_REVIEW') AND COALESCE(git_commit_hash, '') = ''`, runID)
	if err != nil {
		return fmt.Errorf("reopen run: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("run #%d cannot be reopened: it is not an interrupted run without a commit", runID)
	}
	return nil
}

// KillStaleRuns marks any RUNNING run for caseID that started more than maxAge
// ago as KILLED. This prevents a crashed previous invocation from leaving a
// perpetual RUNNING record that blocks the noConcurrentRun quality gate.
func (s *Store) KillStaleRuns(caseID int64, maxAge time.Duration) (int64, error) {
	cutoff := time.Now().Add(-maxAge)
	res, err := s.db.Exec(
		`UPDATE runs SET status = 'KILLED', end_time = ?
		 WHERE case_id = ? AND status = 'RUNNING' AND start_time < ?`,
		time.Now(), caseID, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("kill stale runs: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// Type aliases (for callers that import persistence.Run etc.)
