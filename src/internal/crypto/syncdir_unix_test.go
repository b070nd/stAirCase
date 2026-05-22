//go:build !windows

package crypto

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSyncDir_nonexistent_path_returns_error verifies that syncDir propagates
// the error when the path does not exist.  On Unix this is fatal in rotation
// paths, so the error must surface rather than be silently swallowed.
func TestSyncDir_nonexistent_path_returns_error(t *testing.T) {
	err := syncDir("/nonexistent/staircase-syncdir-test-xyz999")
	assert.Error(t, err, "syncDir must return an error for a non-existent path")
}

// TestSyncDir_existing_dir_succeeds verifies the happy path: syncing an
// existing directory returns nil.
func TestSyncDir_existing_dir_succeeds(t *testing.T) {
	dir := t.TempDir()
	assert.NoError(t, syncDir(dir))
}
