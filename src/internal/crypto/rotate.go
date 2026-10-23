package crypto

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// rotateJournalFile is the crash-recovery record written to the workspace dir
// during key rotation.  Its presence across process restarts allows an
// interrupted rotation to be resumed or cleanly rolled back.
const rotateJournalFile = ".key-rotate-journal"

// rotateJournal is the JSON record written to rotateJournalFile.
type rotateJournal struct {
	// Stage is "pending" (before DB commit) or "committed" (after DB commit,
	// before key-file rename).
	Stage string `json:"stage"`
	// NewKeyPath is the absolute path of the temp file holding the new key bytes.
	NewKeyPath string `json:"new_key_path"`
}

func readRotateJournal(journalPath string) (*rotateJournal, error) {
	data, err := os.ReadFile(journalPath)
	if err != nil {
		return nil, err
	}
	var j rotateJournal
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, err
	}
	return &j, nil
}

// writeRotateJournal writes j atomically (temp+fsync+rename) to journalPath.
func writeRotateJournal(journalPath string, j rotateJournal) error {
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(journalPath), ".key-rotate-journal-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Sync(); err != nil { // flush journal bytes before rename
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, journalPath); err != nil {
		return err
	}
	return syncDir(filepath.Dir(journalPath)) // durable directory entry for journal
}

// ResumeIfCommitted completes an interrupted key installation when wsDir holds a "committed" rotation journal. LoadKey calls it, so any
// command that reads the key heals a crash that left the DB committed but the key file unreplaced. "pending" journals are left to
// RotateKey, because resolving them needs tryDecryptAny.
func ResumeIfCommitted(wsDir string) error {
	journalPath := filepath.Join(wsDir, rotateJournalFile)
	j, err := readRotateJournal(journalPath)
	if os.IsNotExist(err) {
		return nil // normal case: no journal
	}
	if err != nil {
		return fmt.Errorf("resume committed: read journal: %w", err)
	}
	if j.Stage != "committed" {
		return nil // "pending" left for RotateKey + tryDecryptAny
	}
	keyPath := filepath.Join(wsDir, KeyFile)
	if _, statErr := os.Stat(j.NewKeyPath); statErr == nil {
		if renErr := os.Rename(j.NewKeyPath, keyPath); renErr != nil {
			return fmt.Errorf("resume committed: install key: %w", renErr)
		}
		if synErr := syncDir(wsDir); synErr != nil {
			return fmt.Errorf("resume committed: sync dir: %w", synErr)
		}
	}
	// Cleanup is idempotent: a stale journal with a missing temp file means
	// the rename already succeeded on a previous recovery attempt.
	_ = os.Remove(journalPath)
	return nil
}

// RotateKey generates a new AES-256 key, re-encrypts all secrets through reencrypt, and installs the new key file atomically.
//
// A two-stage journal recovers from a crash at every step:
//   - "pending": new key written to temp, journal written, DB not committed. Recovery asks tryDecryptAny whether the DB committed anyway.
//   - "committed": DB committed, temp file holds the new key, key file not replaced. Recovery renames temp to the key file.
//
// The journal and key temp files are fsynced before their renames, and the workspace directory after every key rename, for power-loss
// durability on most file systems. The DB uses synchronous=FULL, so the transaction is on disk when reencrypt returns; a caller that
// opens it another way must set that pragma first.
//
// The caller must hold an exclusive non-blocking flock on <wsDir>/.key: it fails at once if a run or `secret set` holds a shared lock.
// tryDecryptAny reports whether at least one stored secret decrypts with the given key; it is only used in "pending"
// recovery. Pass a function returning false when there are no secrets.
func RotateKey(
	wsDir string,
	reencrypt func(oldKey, newKey []byte) error,
	tryDecryptAny func(key []byte) bool,
) error {
	keyPath := filepath.Join(wsDir, KeyFile)
	journalPath := filepath.Join(wsDir, rotateJournalFile)

	// Recovery: resume or roll back an interrupted rotation
	if j, err := readRotateJournal(journalPath); err == nil {
		switch j.Stage {
		case "committed":
			// DB was committed; temp file holds the new key (or rename was
			// already done and the temp file is gone).
			if _, statErr := os.Stat(j.NewKeyPath); statErr == nil {
				if renErr := os.Rename(j.NewKeyPath, keyPath); renErr != nil {
					return fmt.Errorf("rotate resume committed: install key: %w", renErr)
				}
				if synErr := syncDir(wsDir); synErr != nil {
					return fmt.Errorf("rotate resume committed: sync dir: %w", synErr)
				}
			}
			_ = os.Remove(journalPath) // idempotent: stale journal is recoverable
			return nil

		case "pending":
			// DB may or may not have committed.  Use tryDecryptAny to decide.
			newKey, readErr := os.ReadFile(j.NewKeyPath)
			if readErr == nil && len(newKey) == 32 && tryDecryptAny(newKey) {
				// DB committed despite "pending" stage (crash between commit
				// and journal-update).  Install the new key and finish.
				if renErr := os.Rename(j.NewKeyPath, keyPath); renErr != nil {
					return fmt.Errorf("rotate resume pending→committed: install key: %w", renErr)
				}
				if synErr := syncDir(wsDir); synErr != nil {
					return fmt.Errorf("rotate resume pending→committed: sync dir: %w", synErr)
				}
				_ = os.Remove(journalPath) // idempotent: stale journal is recoverable
				return nil
			}
			// DB not committed (or no secrets): clean up and start fresh.
			_ = os.Remove(j.NewKeyPath)
			_ = os.Remove(journalPath)
		}
	}

	// Normal rotation

	// A process killed before it used its temp key leaves that file (or the
	// journal's temp file) behind. Whatever recovery above did not need is gone
	// now, so none of it lies around: the caller holds the exclusive lock, so
	// no other rotation is using one.
	if stale, _ := filepath.Glob(filepath.Join(wsDir, ".key-rotate-*")); len(stale) > 0 {
		for _, f := range stale {
			if f != journalPath {
				_ = os.Remove(f)
			}
		}
	}

	// 1. Load old key.
	oldKey, err := LoadKey(wsDir)
	if err != nil {
		return err
	}

	// 2. Generate new key and write to a temp file (chmod 0600).
	newKey := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, newKey); err != nil {
		return fmt.Errorf("rotate: generate key: %w", err)
	}
	tmp, err := os.CreateTemp(wsDir, ".key-rotate-*")
	if err != nil {
		return fmt.Errorf("rotate: create temp key: %w", err)
	}
	newKeyPath := tmp.Name()
	if _, err := tmp.Write(newKey); err != nil {
		_ = tmp.Close()
		_ = os.Remove(newKeyPath)
		return fmt.Errorf("rotate: write temp key: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(newKeyPath)
		return fmt.Errorf("rotate: chmod temp key: %w", err)
	}
	if err := tmp.Sync(); err != nil { // flush key bytes before journal
		_ = tmp.Close()
		_ = os.Remove(newKeyPath)
		return fmt.Errorf("rotate: sync temp key: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(newKeyPath)
		return fmt.Errorf("rotate: close temp key: %w", err)
	}

	// 3. Write journal (stage: pending) before DB commit.
	//    If we crash before the DB commits, recovery detects stale state via
	//    tryDecryptAny and cleans up.
	if err := writeRotateJournal(journalPath, rotateJournal{
		Stage:      "pending",
		NewKeyPath: newKeyPath,
	}); err != nil {
		_ = os.Remove(newKeyPath)
		return fmt.Errorf("rotate: write journal: %w", err)
	}

	// 4. Re-encrypt all secrets (DB transaction commits inside reencrypt).
	if err := reencrypt(oldKey, newKey); err != nil {
		// DB rolled back; clean up temp and journal.
		_ = os.Remove(newKeyPath)
		_ = os.Remove(journalPath)
		return fmt.Errorf("rotate: reencrypt: %w", err)
	}

	// 5. Advance journal to "committed" stage.
	//    If this write fails, recovery uses tryDecryptAny to detect committed
	//    state from the "pending" journal.
	_ = writeRotateJournal(journalPath, rotateJournal{
		Stage:      "committed",
		NewKeyPath: newKeyPath,
	})

	// 6. Atomically install the new key file.
	if err := os.Rename(newKeyPath, keyPath); err != nil {
		return fmt.Errorf("rotate: install key: %w", err)
	}
	// Sync parent directory so the rename is durable after a power loss.
	if err := syncDir(wsDir); err != nil {
		return fmt.Errorf("rotate: sync dir: %w", err)
	}

	// 7. Cleanup.
	_ = os.Remove(journalPath)
	return nil
}
