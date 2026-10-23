//go:build windows

// Package wslock provides the advisory file locks shared by the runner and `secret rotate`. On Windows there are none: the stubs below
// exclude nothing, so Enforced is false and a free lock does not mean a process is gone.
package wslock

// Enforced is false: these stubs exclude nothing, so a free lock does not mean a process is gone.
const Enforced = false

func LockShared(_ uintptr) error    { return nil }
func LockExclusive(_ uintptr) error { return nil }
func Unlock(_ uintptr) error        { return nil }
