package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/b070nd/staircase-core/src/internal/domain"
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

// ─── Vendor ───────────────────────────────────────────────────────────────────

func (s *Store) CreateVendor(name string) (*domain.Vendor, error) {
	res, err := s.db.Exec(`INSERT INTO vendors (name) VALUES (?)`, name)
	if err != nil {
		return nil, fmt.Errorf("create vendor: %w", err)
	}
	id, _ := res.LastInsertId()
	return &domain.Vendor{ID: id, Name: name, CreatedAt: time.Now()}, nil
}

func (s *Store) GetVendorByName(name string) (*domain.Vendor, error) {
	v := &domain.Vendor{}
	err := s.db.QueryRow(
		`SELECT id, name, created_at FROM vendors WHERE name = ?`, name,
	).Scan(&v.ID, &v.Name, &v.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return v, err
}

func (s *Store) GetVendorByID(id int64) (*domain.Vendor, error) {
	v := &domain.Vendor{}
	err := s.db.QueryRow(
		`SELECT id, name, created_at FROM vendors WHERE id = ?`, id,
	).Scan(&v.ID, &v.Name, &v.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return v, err
}

func (s *Store) ListVendors() ([]domain.Vendor, error) {
	rows, err := s.db.Query(`SELECT id, name, created_at FROM vendors ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var vs []domain.Vendor
	for rows.Next() {
		var v domain.Vendor
		if err := rows.Scan(&v.ID, &v.Name, &v.CreatedAt); err != nil {
			return nil, err
		}
		vs = append(vs, v)
	}
	return vs, rows.Err()
}

// ─── Project ──────────────────────────────────────────────────────────────────

func (s *Store) CreateProject(vendorID int64, name, sourcePath string) (*domain.Project, error) {
	res, err := s.db.Exec(
		`INSERT INTO projects (vendor_id, name, source_path) VALUES (?, ?, ?)`,
		vendorID, name, sourcePath,
	)
	if err != nil {
		return nil, fmt.Errorf("create project: %w", err)
	}
	id, _ := res.LastInsertId()
	return &domain.Project{ID: id, VendorID: vendorID, Name: name, SourcePath: sourcePath}, nil
}

func (s *Store) GetProject(projectID int64) (*domain.Project, error) {
	p := &domain.Project{}
	err := s.db.QueryRow(
		`SELECT id, vendor_id, name, COALESCE(source_path,''), COALESCE(webhook_url,'') as webhook_url FROM projects WHERE id = ?`, projectID,
	).Scan(&p.ID, &p.VendorID, &p.Name, &p.SourcePath, &p.WebhookURL)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

func (s *Store) GetProjectByVendorAndName(vendorID int64, name string) (*domain.Project, error) {
	p := &domain.Project{}
	err := s.db.QueryRow(
		`SELECT id, vendor_id, name, COALESCE(source_path,''), COALESCE(webhook_url,'') as webhook_url FROM projects WHERE vendor_id = ? AND name = ?`,
		vendorID, name,
	).Scan(&p.ID, &p.VendorID, &p.Name, &p.SourcePath, &p.WebhookURL)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

func (s *Store) ListProjectsByVendor(vendorID int64) ([]domain.Project, error) {
	rows, err := s.db.Query(
		`SELECT id, vendor_id, name, COALESCE(source_path,''), COALESCE(webhook_url,'') as webhook_url FROM projects WHERE vendor_id = ?`, vendorID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ps []domain.Project
	for rows.Next() {
		var p domain.Project
		if err := rows.Scan(&p.ID, &p.VendorID, &p.Name, &p.SourcePath, &p.WebhookURL); err != nil {
			return nil, err
		}
		ps = append(ps, p)
	}
	return ps, rows.Err()
}

func (s *Store) ListAllProjects() ([]domain.Project, error) {
	rows, err := s.db.Query(
		`SELECT id, vendor_id, name, COALESCE(source_path,''), COALESCE(webhook_url,'') as webhook_url FROM projects ORDER BY vendor_id, name`,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ps []domain.Project
	for rows.Next() {
		var p domain.Project
		if err := rows.Scan(&p.ID, &p.VendorID, &p.Name, &p.SourcePath, &p.WebhookURL); err != nil {
			return nil, err
		}
		ps = append(ps, p)
	}
	return ps, rows.Err()
}

func (s *Store) UpdateProjectWebhook(id int64, url string) error {
	_, err := s.db.Exec(`UPDATE projects SET webhook_url = ? WHERE id = ?`, url, id)
	return err
}

// GetProjectConfig returns (defaultModel, budgetUSD) for a project.
// defaultModel is "" if unset; budgetUSD is 0 if unset or no cap.
func (s *Store) GetProjectConfig(projectID int64) (defaultModel string, budgetUSD float64, err error) {
	err = s.db.QueryRow(
		`SELECT COALESCE(default_model,''), COALESCE(budget_usd_per_run,0) FROM projects WHERE id = ?`,
		projectID,
	).Scan(&defaultModel, &budgetUSD)
	return
}

// SetProjectDefaultModel updates the default_model for a project.
func (s *Store) SetProjectDefaultModel(projectID int64, model string) error {
	_, err := s.db.Exec(`UPDATE projects SET default_model = ? WHERE id = ?`, model, projectID)
	return err
}

// SetProjectBudgetCap updates the budget_usd_per_run for a project.
func (s *Store) SetProjectBudgetCap(projectID int64, budgetUSD float64) error {
	_, err := s.db.Exec(`UPDATE projects SET budget_usd_per_run = ? WHERE id = ?`, budgetUSD, projectID)
	return err
}

// ─── ProjectDependency ────────────────────────────────────────────────────────

func (s *Store) CreateProjectDependency(sourceID, targetID int64) (*domain.ProjectDependency, error) {
	res, err := s.db.Exec(
		`INSERT INTO project_dependencies (source_project_id, target_project_id) VALUES (?, ?)`,
		sourceID, targetID,
	)
	if err != nil {
		return nil, fmt.Errorf("create dependency: %w", err)
	}
	id, _ := res.LastInsertId()
	return &domain.ProjectDependency{ID: id, SourceProjectID: sourceID, TargetProjectID: targetID}, nil
}

func (s *Store) ListProjectDependencies(projectID int64) ([]domain.ProjectDependency, error) {
	rows, err := s.db.Query(
		`SELECT id, source_project_id, target_project_id FROM project_dependencies
		 WHERE source_project_id = ?`, projectID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var deps []domain.ProjectDependency
	for rows.Next() {
		var d domain.ProjectDependency
		if err := rows.Scan(&d.ID, &d.SourceProjectID, &d.TargetProjectID); err != nil {
			return nil, err
		}
		deps = append(deps, d)
	}
	return deps, rows.Err()
}

func (s *Store) ListAllProjectDependencies() ([]domain.ProjectDependency, error) {
	rows, err := s.db.Query(
		`SELECT id, source_project_id, target_project_id FROM project_dependencies`,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var deps []domain.ProjectDependency
	for rows.Next() {
		var d domain.ProjectDependency
		if err := rows.Scan(&d.ID, &d.SourceProjectID, &d.TargetProjectID); err != nil {
			return nil, err
		}
		deps = append(deps, d)
	}
	return deps, rows.Err()
}

// ─── Component ────────────────────────────────────────────────────────────────

func (s *Store) CreateComponent(projectID int64, name string) (*domain.Component, error) {
	res, err := s.db.Exec(
		`INSERT INTO components (project_id, name) VALUES (?, ?)`, projectID, name,
	)
	if err != nil {
		return nil, fmt.Errorf("create component: %w", err)
	}
	id, _ := res.LastInsertId()
	return &domain.Component{ID: id, ProjectID: projectID, Name: name}, nil
}

func (s *Store) UpdateComponent(id int64, name string) error {
	res, err := s.db.Exec(`UPDATE components SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		return fmt.Errorf("update component: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("component %d not found", id)
	}
	return nil
}

func (s *Store) DeleteComponent(id int64) error {
	res, err := s.db.Exec(`DELETE FROM components WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete component: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("component %d not found", id)
	}
	return nil
}

func (s *Store) ListComponentsByProject(projectID int64) ([]domain.Component, error) {
	rows, err := s.db.Query(
		`SELECT id, project_id, name FROM components WHERE project_id = ? ORDER BY name`, projectID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var cs []domain.Component
	for rows.Next() {
		var c domain.Component
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.Name); err != nil {
			return nil, err
		}
		cs = append(cs, c)
	}
	return cs, rows.Err()
}

// ─── Secret ───────────────────────────────────────────────────────────────────

func (s *Store) CreateSecret(keyName, encryptedValue string, projectID *int64) (*domain.Secret, error) {
	res, err := s.db.Exec(
		`INSERT INTO secrets (key_name, encrypted_value, scoped_to_project_id) VALUES (?, ?, ?)`,
		keyName, encryptedValue, projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("create secret: %w", err)
	}
	id, _ := res.LastInsertId()
	return &domain.Secret{ID: id, KeyName: keyName, EncryptedValue: encryptedValue, ScopedToProjectID: projectID}, nil
}

// GetSecret returns the most specific secret for keyName: project-scoped first, global fallback.
func (s *Store) GetSecret(keyName string, projectID *int64) (*domain.Secret, error) {
	sec := &domain.Secret{}
	if projectID != nil {
		err := s.db.QueryRow(
			`SELECT id, key_name, encrypted_value, scoped_to_project_id FROM secrets
			 WHERE key_name = ? AND scoped_to_project_id = ?`,
			keyName, *projectID,
		).Scan(&sec.ID, &sec.KeyName, &sec.EncryptedValue, &sec.ScopedToProjectID)
		if err == nil {
			return sec, nil
		}
	}
	err := s.db.QueryRow(
		`SELECT id, key_name, encrypted_value, scoped_to_project_id FROM secrets
		 WHERE key_name = ? AND scoped_to_project_id IS NULL`,
		keyName,
	).Scan(&sec.ID, &sec.KeyName, &sec.EncryptedValue, &sec.ScopedToProjectID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return sec, err
}

func (s *Store) ListSecretsByProject(projectID int64) ([]domain.Secret, error) {
	rows, err := s.db.Query(
		`SELECT id, key_name, encrypted_value, scoped_to_project_id FROM secrets
		 WHERE scoped_to_project_id = ? ORDER BY key_name`, projectID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ss []domain.Secret
	for rows.Next() {
		var sec domain.Secret
		if err := rows.Scan(&sec.ID, &sec.KeyName, &sec.EncryptedValue, &sec.ScopedToProjectID); err != nil {
			return nil, err
		}
		ss = append(ss, sec)
	}
	return ss, rows.Err()
}

// LogSecretAccess writes one row to secret_access_log for every decrypt
// attempt (CHECK 4.4.1).  outcome must be "success", "error", or "not_found".
// runID may be nil when the call originates outside of a run context.
func (s *Store) LogSecretAccess(runID *int64, keyName, outcome string) error {
	_, err := s.db.Exec(
		`INSERT INTO secret_access_log (run_id, key_name, outcome) VALUES (?, ?, ?)`,
		runID, keyName, outcome,
	)
	return err
}

// CountSecretAccesses returns the total number of secret_access_log rows for
// the given run.  Used by TestAtUseAuditCounts (CHECK 4.4.2).
func (s *Store) CountSecretAccesses(runID int64) (int, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM secret_access_log WHERE run_id = ?`, runID,
	).Scan(&n)
	return n, err
}

// ListAllSecrets returns every secret row (both global and project-scoped).
// Used by RotateSecrets to iterate over all ciphertexts.
func (s *Store) ListAllSecrets() ([]domain.Secret, error) {
	rows, err := s.db.Query(
		`SELECT id, key_name, encrypted_value, scoped_to_project_id FROM secrets ORDER BY id`,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ss []domain.Secret
	for rows.Next() {
		var sec domain.Secret
		if err := rows.Scan(&sec.ID, &sec.KeyName, &sec.EncryptedValue, &sec.ScopedToProjectID); err != nil {
			return nil, err
		}
		ss = append(ss, sec)
	}
	return ss, rows.Err()
}

// RotateSecrets re-encrypts every secret in a single DB transaction (CHECK
// 4.3.2).  oldKey decrypts the existing ciphertexts; newKey produces the
// replacement ciphertexts.  The function also bumps the version column.
// The caller is responsible for holding the workspace filesystem lock before
// calling this method (CHECK 4.3.3).
func (s *Store) RotateSecrets(oldKey, newKey []byte, decrypt func([]byte, string) (string, error), encrypt func([]byte, string) (string, error)) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("rotate: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	return rotateSecretsOnTx(tx, oldKey, newKey, decrypt, encrypt)
}

// RotateSecretsOnConn is like RotateSecrets but runs the transaction on the
// provided pinned connection.  Use this when the caller needs to set
// per-connection PRAGMAs (e.g. synchronous=FULL) before the transaction
// begins, guaranteeing that the PRAGMA and the transaction share the same
// underlying SQLite connection.
func (s *Store) RotateSecretsOnConn(ctx context.Context, conn *sql.Conn, oldKey, newKey []byte, decrypt func([]byte, string) (string, error), encrypt func([]byte, string) (string, error)) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("rotate: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	return rotateSecretsOnTx(tx, oldKey, newKey, decrypt, encrypt)
}

// rotateSecretsOnTx performs the re-encryption work inside an already-open
// transaction.  Shared by RotateSecrets and RotateSecretsOnConn.
func rotateSecretsOnTx(tx *sql.Tx, oldKey, newKey []byte, decrypt func([]byte, string) (string, error), encrypt func([]byte, string) (string, error)) error {
	rows, err := tx.Query(`SELECT id, encrypted_value, version FROM secrets`)
	if err != nil {
		return fmt.Errorf("rotate: list: %w", err)
	}

	type row struct {
		id      int64
		enc     string
		version int
	}
	var toUpdate []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.enc, &r.version); err != nil {
			_ = rows.Close()
			return fmt.Errorf("rotate: scan: %w", err)
		}
		toUpdate = append(toUpdate, r)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("rotate: rows: %w", err)
	}

	for _, r := range toUpdate {
		pt, err := decrypt(oldKey, r.enc)
		if err != nil {
			return fmt.Errorf("rotate: decrypt id=%d: %w", r.id, err)
		}
		newEnc, err := encrypt(newKey, pt)
		if err != nil {
			return fmt.Errorf("rotate: re-encrypt id=%d: %w", r.id, err)
		}
		if _, err := tx.Exec(
			`UPDATE secrets SET encrypted_value = ?, version = ? WHERE id = ?`,
			newEnc, r.version+1, r.id,
		); err != nil {
			return fmt.Errorf("rotate: update id=%d: %w", r.id, err)
		}
	}
	return tx.Commit()
}

// ─── Case ─────────────────────────────────────────────────────────────────────

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

// ─── UserStory ────────────────────────────────────────────────────────────────

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

// ─── SwarmTopology ────────────────────────────────────────────────────────────

func (s *Store) CreateSwarmTopology(projectID int64, supervisorName, checkpointType, runtimeType string) (*domain.SwarmTopology, error) {
	var maxVersion int
	err := s.db.QueryRow(
		`SELECT COALESCE(MAX(version), 0) FROM swarm_topologies WHERE project_id = ?`, projectID,
	).Scan(&maxVersion)
	if err != nil {
		return nil, fmt.Errorf("get max version: %w", err)
	}
	version := maxVersion + 1
	if checkpointType == "" {
		checkpointType = "memory"
	}
	if runtimeType == "" {
		runtimeType = "langgraph"
	}
	res, err := s.db.Exec(
		`INSERT INTO swarm_topologies (project_id, version, supervisor_name, checkpoint_type, runtime_type) VALUES (?, ?, ?, ?, ?)`,
		projectID, version, supervisorName, checkpointType, runtimeType,
	)
	if err != nil {
		return nil, fmt.Errorf("create topology: %w", err)
	}
	id, _ := res.LastInsertId()
	return &domain.SwarmTopology{
		ID: id, ProjectID: projectID, Version: version,
		SupervisorName: supervisorName, CheckpointType: checkpointType, RuntimeType: runtimeType,
	}, nil
}

func (s *Store) GetTopology(topologyID int64) (*domain.SwarmTopology, error) {
	t := &domain.SwarmTopology{}
	err := s.db.QueryRow(
		`SELECT id, project_id, version, supervisor_name, checkpoint_type, runtime_type FROM swarm_topologies WHERE id = ?`, topologyID,
	).Scan(&t.ID, &t.ProjectID, &t.Version, &t.SupervisorName, &t.CheckpointType, &t.RuntimeType)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

func (s *Store) GetLatestTopology(projectID int64) (*domain.SwarmTopology, error) {
	t := &domain.SwarmTopology{}
	err := s.db.QueryRow(
		`SELECT id, project_id, version, supervisor_name, checkpoint_type, runtime_type
		 FROM swarm_topologies WHERE project_id = ? ORDER BY version DESC LIMIT 1`, projectID,
	).Scan(&t.ID, &t.ProjectID, &t.Version, &t.SupervisorName, &t.CheckpointType, &t.RuntimeType)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

func (s *Store) ListTopologiesByProject(projectID int64) ([]domain.SwarmTopology, error) {
	rows, err := s.db.Query(
		`SELECT id, project_id, version, supervisor_name, checkpoint_type, runtime_type
		 FROM swarm_topologies WHERE project_id = ? ORDER BY version DESC`, projectID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ts []domain.SwarmTopology
	for rows.Next() {
		var t domain.SwarmTopology
		if err := rows.Scan(&t.ID, &t.ProjectID, &t.Version, &t.SupervisorName, &t.CheckpointType, &t.RuntimeType); err != nil {
			return nil, err
		}
		ts = append(ts, t)
	}
	return ts, rows.Err()
}

// ─── AgentNode ────────────────────────────────────────────────────────────────

func (s *Store) CreateAgentNode(topologyID int64, name, role, model string, componentID *int64) (*domain.AgentNode, error) {
	res, err := s.db.Exec(
		`INSERT INTO agent_nodes (topology_id, name, role, model, component_id) VALUES (?, ?, ?, ?, ?)`,
		topologyID, name, role, model, componentID,
	)
	if err != nil {
		return nil, fmt.Errorf("create agent node: %w", err)
	}
	id, _ := res.LastInsertId()
	return &domain.AgentNode{
		ID: id, TopologyID: topologyID, Name: name,
		Role: role, Model: model, ComponentID: componentID,
	}, nil
}

func (s *Store) GetAgentNode(nodeID int64) (*domain.AgentNode, error) {
	n := &domain.AgentNode{}
	err := s.db.QueryRow(
		`SELECT id, topology_id, name, role, COALESCE(model,''), component_id FROM agent_nodes WHERE id = ?`, nodeID,
	).Scan(&n.ID, &n.TopologyID, &n.Name, &n.Role, &n.Model, &n.ComponentID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return n, err
}

func (s *Store) ListAgentNodes(topologyID int64) ([]domain.AgentNode, error) {
	rows, err := s.db.Query(
		`SELECT id, topology_id, name, role, COALESCE(model,''), component_id
		 FROM agent_nodes WHERE topology_id = ?`, topologyID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var nodes []domain.AgentNode
	for rows.Next() {
		var n domain.AgentNode
		if err := rows.Scan(&n.ID, &n.TopologyID, &n.Name, &n.Role, &n.Model, &n.ComponentID); err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// ─── AgentTool ────────────────────────────────────────────────────────────────

func (s *Store) CreateAgentTool(agentID int64, toolName, toolConfig string) (*domain.AgentTool, error) {
	res, err := s.db.Exec(
		`INSERT INTO agent_tools (agent_id, tool_name, tool_config) VALUES (?, ?, ?)`,
		agentID, toolName, toolConfig,
	)
	if err != nil {
		return nil, fmt.Errorf("create agent tool: %w", err)
	}
	id, _ := res.LastInsertId()
	return &domain.AgentTool{ID: id, AgentID: agentID, ToolName: toolName, ToolConfig: toolConfig}, nil
}

func (s *Store) ListAgentTools(agentID int64) ([]domain.AgentTool, error) {
	rows, err := s.db.Query(
		`SELECT id, agent_id, tool_name, COALESCE(tool_config,'') FROM agent_tools WHERE agent_id = ?`, agentID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var tools []domain.AgentTool
	for rows.Next() {
		var t domain.AgentTool
		if err := rows.Scan(&t.ID, &t.AgentID, &t.ToolName, &t.ToolConfig); err != nil {
			return nil, err
		}
		tools = append(tools, t)
	}
	return tools, rows.Err()
}

// ─── Edge ─────────────────────────────────────────────────────────────────────

func (s *Store) CreateEdge(topologyID int64, from, to, condition string) (*domain.Edge, error) {
	res, err := s.db.Exec(
		`INSERT INTO edges (topology_id, from_node, to_node, condition) VALUES (?, ?, ?, ?)`,
		topologyID, from, to, condition,
	)
	if err != nil {
		return nil, fmt.Errorf("create edge: %w", err)
	}
	id, _ := res.LastInsertId()
	return &domain.Edge{ID: id, TopologyID: topologyID, FromNode: from, ToNode: to, Condition: condition}, nil
}

func (s *Store) ListEdges(topologyID int64) ([]domain.Edge, error) {
	rows, err := s.db.Query(
		`SELECT id, topology_id, from_node, to_node, COALESCE(condition,'') FROM edges WHERE topology_id = ?`, topologyID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var edges []domain.Edge
	for rows.Next() {
		var e domain.Edge
		if err := rows.Scan(&e.ID, &e.TopologyID, &e.FromNode, &e.ToNode, &e.Condition); err != nil {
			return nil, err
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

// ─── Run ──────────────────────────────────────────────────────────────────────

func (s *Store) CreateRun(caseID int64, topologyVersion int, gitBranch string) (*domain.Run, error) {
	// Enforce referential integrity: topology_version must correspond to a real
	// swarm_topologies row for the project that owns this case.
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
	if topoCount == 0 {
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
	if status != RunStatusSuccess && status != RunStatusFailed && status != RunStatusKilled {
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
	caseStatus := CaseStatusFailed
	if status == RunStatusSuccess {
		var remaining int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM user_stories WHERE case_id = ? AND status != 'IMPLEMENTED'`, caseID).Scan(&remaining); err != nil {
			return fmt.Errorf("finish run: inspect stories: %w", err)
		}
		caseStatus = CaseStatusCompleted
		if remaining > 0 {
			caseStatus = CaseStatusPending
		}
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

// ─── RunEventLog (SOC2 tamper-proof chain) ────────────────────────────────────

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

// maxEventLogPayload is the store-level defence-in-depth cap on payload size.
// The IPC server caps at 64 KiB before calling AppendEventLog; this second
// guard protects callers that bypass the IPC layer (e.g. tests, future CLIs).
const maxEventLogPayload = 64 * 1024 // 64 KiB

// ComputeEventHash is the single source of truth for the SOC2 chain-hash
// algorithm: SHA-256(payload ‖ prevHash ‖ gitCommitHash).
//
// Note on CHECK 9.1.1: the checklist specifies SHA-256(prev_hash ‖ event_body)
// (prevHash first). The implementation uses payload first and includes
// gitCommitHash as a third input.  This divergence is intentional: including
// the git commit hash ties each entry to the exact source revision that wrote
// it, which is a stronger guarantee than the baseline spec.  Changing the
// order would invalidate all existing audit chains, so we document it here
// rather than silently break backward compatibility.
//
// Both AppendEventLog and external verifiers (audit export/verify) must call
// this function so that any future change to the algorithm stays in one place.
func ComputeEventHash(payload, prevHash, gitCommitHash string) string {
	h := sha256.Sum256([]byte(payload + prevHash + gitCommitHash))
	return fmt.Sprintf("%x", h)
}

// AppendEventLog writes a tamper-proof entry: hash = SHA-256(payload + prevHash + gitCommitHash).
// gitCommitHash is stored per-entry so that inspect log can verify each entry
// with the exact value that was current when the entry was written — the hash
// changes from "" to the real commit hash at teardown, so a single run-level
// value cannot be used for verification of all entries.
func (s *Store) AppendEventLog(runID int64, eventType, payload, prevHash, gitCommitHash string) (*domain.RunEventLog, error) {
	if len(payload) > maxEventLogPayload {
		payload = payload[:maxEventLogPayload]
	}
	eventHash := ComputeEventHash(payload, prevHash, gitCommitHash)
	now := time.Now()
	res, err := s.db.Exec(
		`INSERT INTO run_event_logs (run_id, event_type, payload, timestamp, event_hash, git_commit_hash)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		runID, eventType, payload, now, eventHash, gitCommitHash,
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
	}, nil
}

// AppendEventLogChained atomically reads the run's last event hash and appends
// a new entry chained to it. The read+insert is serialized by appendMu so that
// concurrent callers within the process cannot both observe the same prevHash
// and fork the chain. This is the method production callers must use; the
// lower-level AppendEventLog (explicit prevHash) is retained for tests and
// external verifiers. Single-process serialization is sufficient because the
// no_concurrent_run gate guarantees only one run writes a given workspace's
// event log at a time.
func (s *Store) AppendEventLogChained(runID int64, eventType, payload, gitCommitHash string) (*domain.RunEventLog, error) {
	s.appendMu.Lock()
	defer s.appendMu.Unlock()
	prevHash, err := s.GetLastEventHash(runID)
	if err != nil {
		return nil, fmt.Errorf("read last event hash: %w", err)
	}
	return s.AppendEventLog(runID, eventType, payload, prevHash, gitCommitHash)
}

func (s *Store) ListEventLogs(runID int64) ([]domain.RunEventLog, error) {
	rows, err := s.db.Query(
		`SELECT id, run_id, event_type, payload, timestamp, event_hash, git_commit_hash
		 FROM run_event_logs WHERE run_id = ? ORDER BY id ASC`, runID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var logs []domain.RunEventLog
	for rows.Next() {
		var l domain.RunEventLog
		if err := rows.Scan(&l.ID, &l.RunID, &l.EventType, &l.Payload, &l.Timestamp, &l.EventHash, &l.GitCommitHash); err != nil {
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
// This is CHECK 9.1.2 — used by "staircase audit verify" and the tamper test.
func (s *Store) VerifyChain(runID int64) error {
	logs, err := s.ListEventLogs(runID)
	if err != nil {
		return fmt.Errorf("verify chain: list events: %w", err)
	}
	prevHash := ""
	for i, entry := range logs {
		want := ComputeEventHash(entry.Payload, prevHash, entry.GitCommitHash)
		if entry.EventHash != want {
			return fmt.Errorf("verify chain: hash mismatch at entry %d (id=%d): stored=%s computed=%s",
				i+1, entry.ID, entry.EventHash, want)
		}
		prevHash = entry.EventHash
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

// HasSuccessfulRunAtTopologyVersion reports whether the given project has at
// least one SUCCESS run whose topology_version matches the supplied version.
// Used by the deps.deps_completed gate to verify upstream work was done against
// the current topology, not an outdated one.
func (s *Store) HasSuccessfulRunAtTopologyVersion(projectID int64, topoVersion int) (bool, error) {
	var count int
	err := s.db.QueryRow(
		`
		SELECT COUNT(*)
		FROM runs
		JOIN cases ON runs.case_id = cases.id
		WHERE cases.project_id = ?
		  AND runs.status = 'SUCCESS'
		  AND runs.topology_version = ?`,
		projectID, topoVersion,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("has successful run at topology version: %w", err)
	}
	return count > 0, nil
}

// ─── Type aliases (for callers that import persistence.Run etc.) ──────────────

// Run is a convenience alias so cmd packages can use persistence.Run directly.
type Run = domain.Run

// Project is a convenience alias so cmd packages can use persistence.Project directly.
type Project = domain.Project

// ProjectDependency is a convenience alias so cmd packages can use persistence.ProjectDependency directly.
type ProjectDependency = domain.ProjectDependency
