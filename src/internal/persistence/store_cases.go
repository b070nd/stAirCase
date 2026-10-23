package persistence

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/b070nd/stAirCase/src/internal/domain"
)

func (s *Store) CreateCase(projectID int64) (*domain.Case, error) {
	now := time.Now()
	res, err := s.db.Exec(
		`INSERT INTO cases (project_id, status, last_modified) VALUES (?, 'PENDING', ?)`,
		projectID, now,
	)
	if err != nil {
		return nil, fmt.Errorf("create case: %w", err)
	}
	id, _ := res.LastInsertId()
	return &domain.Case{ID: id, ProjectID: projectID, Status: domain.CaseStatusPending, LastModified: now}, nil
}

func (s *Store) GetCase(caseID int64) (*domain.Case, error) {
	c := &domain.Case{}
	var deletedAt sql.NullTime
	err := s.db.QueryRow(
		`SELECT id, project_id, status, last_modified, COALESCE(prd_json,''), deleted_at FROM cases WHERE id = ?`, caseID,
	).Scan(&c.ID, &c.ProjectID, &c.Status, &c.LastModified, &c.PrdJSON, &deletedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if deletedAt.Valid {
		c.DeletedAt = &deletedAt.Time
	}
	return c, err
}

func (s *Store) UpdateCaseStatus(caseID int64, status string) error {
	_, err := s.db.Exec(
		`UPDATE cases SET status = ?, last_modified = ? WHERE id = ?`,
		status, time.Now(), caseID,
	)
	return err
}

func (s *Store) SetCasePRD(caseID int64, prdJSON string) error {
	_, err := s.db.Exec(
		`UPDATE cases SET prd_json = ?, last_modified = ? WHERE id = ?`,
		prdJSON, time.Now(), caseID,
	)
	return err
}

// UpdateCasePRD is an alias for SetCasePRD kept for callers in compile.go.
func (s *Store) UpdateCasePRD(caseID int64, prd string) error {
	return s.SetCasePRD(caseID, prd)
}

func (s *Store) ListCasesByProject(projectID int64) ([]domain.Case, error) {
	rows, err := s.db.Query(
		`SELECT id, project_id, status, last_modified, COALESCE(prd_json,''), deleted_at
		 FROM cases WHERE project_id = ? ORDER BY id DESC`, projectID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var cs []domain.Case
	for rows.Next() {
		var c domain.Case
		var deletedAt sql.NullTime
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.Status, &c.LastModified, &c.PrdJSON, &deletedAt); err != nil {
			return nil, err
		}
		if deletedAt.Valid {
			c.DeletedAt = &deletedAt.Time
		}
		cs = append(cs, c)
	}
	return cs, rows.Err()
}

// FlagCaseDeleted marks a case for deferred deletion on next clean --aggressive.
func (s *Store) FlagCaseDeleted(id int64) error {
	now := time.Now()
	_, err := s.db.Exec(`UPDATE cases SET deleted_at = ? WHERE id = ?`, now, id)
	return err
}

// ListFlaggedCases returns the cases marked for deletion.
func (s *Store) ListFlaggedCases() ([]domain.Case, error) {
	rows, err := s.db.Query(`SELECT id, project_id, status, last_modified, COALESCE(prd_json,''), deleted_at
		FROM cases WHERE deleted_at IS NOT NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var cs []domain.Case
	for rows.Next() {
		var c domain.Case
		var deletedAt sql.NullTime
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.Status, &c.LastModified, &c.PrdJSON, &deletedAt); err != nil {
			return nil, err
		}
		if deletedAt.Valid {
			c.DeletedAt = &deletedAt.Time
		}
		cs = append(cs, c)
	}
	return cs, rows.Err()
}

func (s *Store) CreateUserStory(caseID int64, description string) (*domain.UserStory, error) {
	res, err := s.db.Exec(
		`INSERT INTO user_stories (case_id, description, status) VALUES (?, ?, 'PENDING')`,
		caseID, description,
	)
	if err != nil {
		return nil, fmt.Errorf("create user story: %w", err)
	}
	id, _ := res.LastInsertId()
	return &domain.UserStory{ID: id, CaseID: caseID, Description: description, Status: domain.StoryStatusPending}, nil
}

func (s *Store) UpdateUserStoryStatus(storyID int64, status string) error {
	res, err := s.db.Exec(`UPDATE user_stories SET status = ? WHERE id = ?`, status, storyID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("story %d not found", storyID)
	}
	return nil
}

// GetUserStory returns a story, or nil when it does not exist.
func (s *Store) GetUserStory(storyID int64) (*domain.UserStory, error) {
	var us domain.UserStory
	err := s.db.QueryRow(`SELECT id, case_id, description, status, COALESCE(custom_config,'') FROM user_stories WHERE id = ?`, storyID).
		Scan(&us.ID, &us.CaseID, &us.Description, &us.Status, &us.CustomConfig)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &us, err
}

func (s *Store) ListUserStoriesByCase(caseID int64) ([]domain.UserStory, error) {
	rows, err := s.db.Query(
		`SELECT id, case_id, description, status, COALESCE(custom_config,'') FROM user_stories WHERE case_id = ?`,
		caseID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var stories []domain.UserStory
	for rows.Next() {
		var us domain.UserStory
		if err := rows.Scan(&us.ID, &us.CaseID, &us.Description, &us.Status, &us.CustomConfig); err != nil {
			return nil, err
		}
		stories = append(stories, us)
	}
	return stories, rows.Err()
}

// SwarmTopology
