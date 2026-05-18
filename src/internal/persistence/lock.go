//go:build !windows

// Package persistence — RotationLock provides a filesystem advisory lock that
// prevents concurrent key-rotation operations (CHECK 4.3.3).
package persistence

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// RotationLock holds an exclusive flock on the key-rotation lock file.
// Acquire via [NewRotationLock]; release by calling [Unlock].
type RotationLock struct {
	f *os.File
}

// NewRotationLock acquires an exclusive, non-blocking filesystem lock
// (syscall.LOCK_EX|syscall.LOCK_NB) on <wsDir>/tmp/key-rotation.lock.
// If the lock is already held by another process it returns an error
// immediately (refuses to block) so that a rotation in progress is never
// silently skipped or double-run (CHECK 4.3.3).
func NewRotationLock(wsDir string) (*RotationLock, error) {
	lockPath := filepath.Join(wsDir, "tmp", "key-rotation.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, fmt.Errorf("rotation lock dir: %w", err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("rotation lock open: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("rotation already in progress (flock LOCK_EX): %w", err)
	}
	return &RotationLock{f: f}, nil
}

// Unlock releases the flock and closes the lock file.
func (l *RotationLock) Unlock() {
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
}
