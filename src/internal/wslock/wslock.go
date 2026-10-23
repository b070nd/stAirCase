// Package wslock provides the advisory file locks (flock) shared by the runner and `secret rotate`. A run holds a shared lock on the
// workspace key file while it is active; rotation takes an exclusive non-blocking lock, so it fails at once if a run is active
//.
//
//go:build !windows

package wslock

import "syscall"

// Enforced reports whether these locks really exclude other processes. Code that decides something from
// "the lock is free, so the process is gone" must check it: where it is false (Windows) a lock proves nothing.
const Enforced = true

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
