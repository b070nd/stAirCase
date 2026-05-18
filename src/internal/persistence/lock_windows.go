//go:build windows

package persistence

import "fmt"

// RotationLock is a stub on Windows where syscall.Flock is not available.
type RotationLock struct{}

// NewRotationLock returns a no-op lock on Windows.
func NewRotationLock(_ string) (*RotationLock, error) {
	return &RotationLock{}, nil
}

// Unlock is a no-op on Windows.
func (l *RotationLock) Unlock() {}

var _ = fmt.Sprintf // suppress unused-import lint
