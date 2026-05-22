// Package wslock provides advisory file locking (flock) primitives shared by
// the runner and the CLI rotate command.
//
// The runner acquires a shared lock on the workspace key file for the duration
// of an active run.  The 'staircase secret rotate' command acquires an
// exclusive non-blocking lock for the duration of key rotation.  The two are
// mutually exclusive: rotate fails fast if a run is active (CHECK 4.3.3).
//
//go:build !windows

package wslock

import "syscall"

// LockShared acquires a shared (reader) advisory flock on fd with LOCK_NB.
// Returns EWOULDBLOCK if an exclusive lock is already held.
func LockShared(fd uintptr) error {
	return syscall.Flock(int(fd), syscall.LOCK_SH|syscall.LOCK_NB)
}

// LockExclusive acquires an exclusive (writer) advisory flock on fd with LOCK_NB.
// Returns EWOULDBLOCK if any lock (shared or exclusive) is already held.
func LockExclusive(fd uintptr) error {
	return syscall.Flock(int(fd), syscall.LOCK_EX|syscall.LOCK_NB)
}

// Unlock releases an advisory flock held on fd.
func Unlock(fd uintptr) error {
	return syscall.Flock(int(fd), syscall.LOCK_UN)
}
