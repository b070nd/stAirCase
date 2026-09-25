package persistence

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/b070nd/stAirCase/src/internal/domain"
)

// ImportBlueprint stores a snapshot. It reports false when the hash was
// already imported: a snapshot never changes, so the first import stands.
func (s *Store) ImportBlueprint(b domain.Blueprint) (bool, error) {
	res, err := s.db.Exec(`INSERT OR IGNORE INTO blueprints (hash, name, content, source_dir, git_sha) VALUES (?, ?, ?, ?, ?)`,
		b.Hash, b.Name, b.Content, b.SourceDir, b.GitSHA)
	if err != nil {
		return false, fmt.Errorf("import blueprint: %w", err)
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

const blueprintColumns = `hash, name, content, source_dir, git_sha, imported_at`

func scanBlueprint(row interface{ Scan(...any) error }) (domain.Blueprint, error) {
	var b domain.Blueprint
	err := row.Scan(&b.Hash, &b.Name, &b.Content, &b.SourceDir, &b.GitSHA, &b.ImportedAt)
	return b, err
}

// ListBlueprints returns every imported snapshot, newest first.
func (s *Store) ListBlueprints() ([]domain.Blueprint, error) {
	rows, err := s.db.Query(`SELECT ` + blueprintColumns + ` FROM blueprints ORDER BY imported_at DESC, hash`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []domain.Blueprint
	for rows.Next() {
		b, err := scanBlueprint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// FindBlueprint returns the snapshot whose hash starts with prefix; it must
// match exactly one.
func (s *Store) FindBlueprint(prefix string) (*domain.Blueprint, error) {
	if prefix == "" {
		return nil, errors.New("blueprint hash required")
	}
	rows, err := s.db.Query(`SELECT `+blueprintColumns+` FROM blueprints WHERE substr(hash, 1, ?) = ? LIMIT 2`, len(prefix), prefix)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var found []domain.Blueprint
	for rows.Next() {
		b, err := scanBlueprint(rows)
		if err != nil {
			return nil, err
		}
		found = append(found, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	switch len(found) {
	case 0:
		return nil, fmt.Errorf("no blueprint with hash %s — import it with 'staircase blueprint import'", prefix)
	case 1:
		return &found[0], nil
	}
	return nil, fmt.Errorf("blueprint hash %s is ambiguous — give more characters", prefix)
}

// Binding is what binding a project to a blueprint creates.
type Binding struct {
	Hash       string
	Supervisor string
	Agents     []domain.AgentNode // Name, Role, Model
	Edges      []domain.Edge      // FromNode, ToNode, Condition
	Cases      []BoundCase
}

// BoundCase is one blueprint case to create; stories carry Description and
// CustomConfig (their scope).
type BoundCase struct {
	Slug, PRD string
	Stories   []domain.UserStory
}

// BindBlueprint binds a project to a blueprint in one transaction: a new
// topology version, new cases and stories tagged with the blueprint, and the
// project's blueprint. Existing topologies, cases and stories are not changed.
func (s *Store) BindBlueprint(projectID int64, b Binding) (topologyVersion int, caseIDs []int64, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, nil, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = tx.QueryRow(`SELECT COALESCE(MAX(version), 0) + 1 FROM swarm_topologies WHERE project_id = ?`, projectID).Scan(&topologyVersion); err != nil {
		return 0, nil, err
	}
	res, err := tx.Exec(`INSERT INTO swarm_topologies (project_id, version, supervisor_name) VALUES (?, ?, ?)`, projectID, topologyVersion, b.Supervisor)
	if err != nil {
		return 0, nil, fmt.Errorf("create topology: %w", err)
	}
	topoID, _ := res.LastInsertId()
	for _, a := range b.Agents {
		if _, err = tx.Exec(`INSERT INTO agent_nodes (topology_id, name, role, model) VALUES (?, ?, ?, ?)`, topoID, a.Name, a.Role, a.Model); err != nil {
			return 0, nil, fmt.Errorf("create agent %s: %w", a.Name, err)
		}
	}
	for _, e := range b.Edges {
		if _, err = tx.Exec(`INSERT INTO edges (topology_id, from_node, to_node, condition) VALUES (?, ?, ?, ?)`, topoID, e.FromNode, e.ToNode, e.Condition); err != nil {
			return 0, nil, fmt.Errorf("create edge: %w", err)
		}
	}
	for _, c := range b.Cases {
		res, err = tx.Exec(`INSERT INTO cases (project_id, status, last_modified, prd_json, blueprint_hash, blueprint_slug) VALUES (?, 'PENDING', ?, ?, ?, ?)`,
			projectID, time.Now(), c.PRD, b.Hash, c.Slug)
		if err != nil {
			return 0, nil, fmt.Errorf("create case %s: %w", c.Slug, err)
		}
		caseID, _ := res.LastInsertId()
		caseIDs = append(caseIDs, caseID)
		for _, st := range c.Stories {
			if _, err = tx.Exec(`INSERT INTO user_stories (case_id, description, status, custom_config) VALUES (?, ?, 'PENDING', ?)`,
				caseID, st.Description, sql.NullString{String: st.CustomConfig, Valid: st.CustomConfig != ""}); err != nil {
				return 0, nil, fmt.Errorf("create story: %w", err)
			}
		}
	}
	if _, err = tx.Exec(`UPDATE projects SET blueprint_hash = ? WHERE id = ?`, b.Hash, projectID); err != nil {
		return 0, nil, err
	}
	return topologyVersion, caseIDs, tx.Commit()
}

// ProjectBlueprint returns the blueprint a project is bound to, or "".
func (s *Store) ProjectBlueprint(projectID int64) (string, error) {
	var h string
	err := s.db.QueryRow(`SELECT blueprint_hash FROM projects WHERE id = ?`, projectID).Scan(&h)
	return h, err
}

// CaseBlueprint returns the blueprint and case slug a case was created from,
// or empty strings for a case made by hand.
func (s *Store) CaseBlueprint(caseID int64) (hash, slug string, err error) {
	err = s.db.QueryRow(`SELECT blueprint_hash, blueprint_slug FROM cases WHERE id = ?`, caseID).Scan(&hash, &slug)
	return hash, slug, err
}

// SetUserStoryScope sets a story's scope, as JSON {"allow": [...], "max_files": N}.
func (s *Store) SetUserStoryScope(storyID int64, scopeJSON string) error {
	res, err := s.db.Exec(`UPDATE user_stories SET custom_config = ? WHERE id = ?`, scopeJSON, storyID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("story %d not found", storyID)
	}
	return nil
}
