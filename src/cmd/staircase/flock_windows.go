//go:build windows

package main

import "github.com/b070nd/staircase-core/src/internal/wslock"

// Advisory file locking on Windows is a no-op (see wslock package).
func flockExclusive(fd uintptr) error { return wslock.LockExclusive(fd) }
func flockUnlock(fd uintptr) error    { return wslock.Unlock(fd) }
