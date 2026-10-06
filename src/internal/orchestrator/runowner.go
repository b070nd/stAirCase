package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/b070nd/stAirCase/src/internal/wslock"
)

// A run is owned by the process that drives it: it holds an exclusive lock on journal/run-N.owner.lock for its
// whole life. The operating system drops the lock when the process ends, however it ends (a kill -9 included), so
// "is this run still running?" has an answer that does not depend on a record the dead process could not update:
// while the lock is held the run is live, and nothing else may recover or continue it; once it is free the process
// is gone. Where locks exclude nothing (Windows) the answer is "unknown", never "gone".

func ownerLockPath(wsDir string, runID int64) string {
	return filepath.Join(wsDir, "journal", fmt.Sprintf("run-%d.owner.lock", runID))
}

// claimRun takes the run's owner lock without waiting, creating the file. It returns what releases it, or an error
// when another process holds it: two processes cannot drive the same run.
func claimRun(wsDir string, runID int64) (release func(), err error) {
	path := ownerLockPath(wsDir, runID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := wslock.LockExclusive(f.Fd()); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("run #%d is owned by another process: %w", runID, err)
	}
	return func() { _ = wslock.Unlock(f.Fd()); _ = f.Close() }, nil
}

// RunOwner says whether the process that started a run is still alive. held: some process holds the run's owner
// lock, so the run is live. known: the answer can be trusted; it is false for a run started before runs took
// this lock (no lock file) and where locks exclude nothing. A caller must treat known == false as "do not
// know", and keep asking the person (the way `recover --force` does).
func RunOwner(wsDir string, runID int64) (held, known bool) {
	if !wslock.Enforced {
		return false, false
	}
	f, err := os.OpenFile(ownerLockPath(wsDir, runID), os.O_RDWR, 0)
	if err != nil {
		return false, false
	}
	defer func() { _ = f.Close() }()
	if err := wslock.LockExclusive(f.Fd()); err != nil {
		return true, true
	}
	_ = wslock.Unlock(f.Fd())
	return false, true
}
