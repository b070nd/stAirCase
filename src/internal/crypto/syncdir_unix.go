//go:build !windows

package crypto

import "os"

// syncDir fsyncs the directory at path so that a preceding rename is durable
// after a power loss.  On Unix, directory fsyncs are required to flush
// directory-entry changes to stable storage.  Errors are fatal in rotation
// paths because a non-durable journal rename could leave the workspace
// unrecoverable after power loss.
func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}
