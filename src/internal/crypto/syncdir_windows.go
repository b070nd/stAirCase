//go:build windows

package crypto

// syncDir is a no-op on Windows.  NTFS does not expose a meaningful
// per-directory fsync primitive; Windows manages directory-entry durability
// internally.  Key-file and journal file contents are still fsynced via
// os.File.Sync() on the temp files before rename.  This means rotation on
// Windows is process-crash safe (file contents durable) but does not provide
// power-loss durability for directory-entry visibility of renames.
func syncDir(_ string) error { return nil }
