package persistence

import (
	"github.com/b070nd/stAirCase/src/internal/domain"
)

// PrunableEventLogs returns the run_event_logs rows retention may remove to keep about keep rows: those of runs whose every row is among
// the oldest rows over that number, oldest first. A run goes whole or not at all, because a chain with its front cut off cannot be
// verified; a run still RUNNING, or with a row newer than the cut (a story accepted later), stays. It can return fewer rows than are
// over keep, or none.
func (s *Store) PrunableEventLogs(keep int64) ([]domain.RunEventLog, error) {
	var total int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM run_event_logs`).Scan(&total); err != nil {
		return nil, err
	}
	if total-keep <= 0 {
		return nil, nil
	}
	var cut int64 // the id of the newest row among the ones over keep
	if err := s.db.QueryRow(`SELECT id FROM run_event_logs ORDER BY id ASC LIMIT 1 OFFSET ?`, total-keep-1).Scan(&cut); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id, run_id, event_type, payload, timestamp, event_hash, git_commit_hash, hash_version
		FROM run_event_logs
		WHERE run_id IN (SELECT run_id FROM run_event_logs GROUP BY run_id HAVING MAX(id) <= ?)
		  AND run_id NOT IN (SELECT id FROM runs WHERE status = 'RUNNING')
		ORDER BY id ASC`, cut)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []domain.RunEventLog
	for rows.Next() {
		var e domain.RunEventLog
		if err := rows.Scan(&e.ID, &e.RunID, &e.EventType, &e.Payload, &e.Timestamp, &e.EventHash, &e.GitCommitHash, &e.HashVersion); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// PruneEventLogsOfRuns deletes every run_event_logs row of the given runs, which
// the caller has already archived.
func (s *Store) PruneEventLogsOfRuns(runIDs []int64) (int64, error) {
	var total int64
	for _, id := range runIDs {
		res, err := s.db.Exec(`DELETE FROM run_event_logs WHERE run_id = ?`, id)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

// DeleteFlaggedCases permanently removes cases where deleted_at IS NOT NULL.
// Cascades to runs and run_event_logs via FK ON DELETE CASCADE.
// Returns the number of cases deleted.
func (s *Store) DeleteFlaggedCases() (int64, error) {
	res, err := s.db.Exec(`DELETE FROM cases WHERE deleted_at IS NOT NULL`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// PruneEventLogs deletes the oldest run_event_log rows when the total exceeds
// maxRows, keeping the most recent entries. Returns the number of rows deleted.
func (s *Store) PruneEventLogs(maxRows int64) (int64, error) {
	res, err := s.db.Exec(`
		DELETE FROM run_event_logs
		WHERE id IN (
			SELECT id FROM run_event_logs
			ORDER BY id ASC
			LIMIT MAX(0, (SELECT COUNT(*) FROM run_event_logs) - ?)
		)`, maxRows)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// UserStory
