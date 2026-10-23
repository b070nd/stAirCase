//go:build windows

package crypto

// syncDir is a no-op on Windows: NTFS has no per-directory fsync. Key and journal contents are still fsynced before the rename, so
// rotation is safe against a process crash but not against power loss.
func syncDir(_ string) error { return nil }
