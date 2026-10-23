package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/b070nd/stAirCase/src/internal/domain"
)

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

// SetSecret stores a secret, replacing the value of the same key in the same
// scope (global when projectID is nil) and counting its version.
func (s *Store) SetSecret(keyName, encryptedValue string, projectID *int64) (replaced bool, version int, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	err = tx.QueryRow(`UPDATE secrets SET encrypted_value = ?, version = version + 1
		WHERE key_name = ? AND scoped_to_project_id IS ? RETURNING version`, encryptedValue, keyName, projectID).Scan(&version)
	switch {
	case err == nil:
		replaced = true
	case errors.Is(err, sql.ErrNoRows):
		if _, err = tx.Exec(`INSERT INTO secrets (key_name, encrypted_value, scoped_to_project_id) VALUES (?, ?, ?)`,
			keyName, encryptedValue, projectID); err != nil {
			return false, 0, fmt.Errorf("create secret: %w", err)
		}
		version = 1
	default:
		return false, 0, fmt.Errorf("update secret: %w", err)
	}
	return replaced, version, tx.Commit()
}

// DeleteSecret removes the secret of keyName in one scope (global when projectID
// is nil) and reports whether there was one.
func (s *Store) DeleteSecret(keyName string, projectID *int64) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM secrets WHERE key_name = ? AND scoped_to_project_id IS ?`, keyName, projectID)
	if err != nil {
		return false, fmt.Errorf("delete secret: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
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
		if err != sql.ErrNoRows { // a failed lookup is not an absent secret
			return nil, err
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
// attempt.  outcome must be "success", "error", or "not_found".
// runID may be nil when the call originates outside of a run context.
func (s *Store) LogSecretAccess(runID *int64, keyName, outcome string) error {
	_, err := s.db.Exec(
		`INSERT INTO secret_access_log (run_id, key_name, outcome) VALUES (?, ?, ?)`,
		runID, keyName, outcome,
	)
	return err
}

// CountSecretAccesses returns the total number of secret_access_log rows for
// the given run.  Used by TestAtUseAuditCounts.
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
// calling this method.
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

// Case
