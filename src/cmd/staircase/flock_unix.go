//go:build !windows

package main

import "github.com/b070nd/staircase-core/src/internal/wslock"

func flockExclusive(fd uintptr) error { return wslock.LockExclusive(fd) }
func flockUnlock(fd uintptr) error    { return wslock.Unlock(fd) }
