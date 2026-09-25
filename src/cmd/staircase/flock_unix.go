//go:build !windows

package main

import "github.com/b070nd/stAirCase/src/internal/wslock"

func flockShared(fd uintptr) error    { return wslock.LockShared(fd) }
func flockExclusive(fd uintptr) error { return wslock.LockExclusive(fd) }
func flockUnlock(fd uintptr) error    { return wslock.Unlock(fd) }
