// Advisory file locking stubs for Windows.  The Windows build cannot launch
// Python subprocesses, so no concurrent run can hold the key file open; all
// operations are safe without locking.
//
//go:build windows

package wslock

func LockShared(_ uintptr) error    { return nil }
func LockExclusive(_ uintptr) error { return nil }
func Unlock(_ uintptr) error        { return nil }
