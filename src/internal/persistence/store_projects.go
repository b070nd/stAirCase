package persistence

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/b070nd/stAirCase/src/internal/domain"
)

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

// Project

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

// ProjectDependency

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

// Component

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

// Secret
