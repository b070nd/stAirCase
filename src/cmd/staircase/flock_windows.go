//go:build windows

package main

// flockExclusive is a no-op on Windows (advisory file locking via Flock is
// not supported).  Key rotation without locking is safe because the Windows
// build cannot launch Python subprocesses, so no concurrent run can hold the
// key open.
func flockExclusive(_ uintptr) error { return nil }

func flockUnlock(_ uintptr) error { return nil }
