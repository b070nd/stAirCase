package audit_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/b070nd/staircase-core/src/internal/audit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppendCheckpoint_creates_file(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run-1.checkpoint.json")

	require.NoError(t, audit.AppendCheckpoint(path, []byte(`{"run_id":1}`)))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"run_id":1`)
}

func TestAppendCheckpoint_appends_not_overwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run-2.checkpoint.json")

	require.NoError(t, audit.AppendCheckpoint(path, []byte(`{"run_id":2,"n":1}`)))
	require.NoError(t, audit.AppendCheckpoint(path, []byte(`{"run_id":2,"n":2}`)))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	s := string(data)
	assert.Contains(t, s, `"n":1`)
	assert.Contains(t, s, `"n":2`, "second write must append, not overwrite")
}

func TestAppendCheckpoint_mode_0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run-3.checkpoint.json")

	require.NoError(t, audit.AppendCheckpoint(path, []byte(`{}`)))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "checkpoint must be owner-only")
}

func TestAppendCheckpoint_bad_dir(t *testing.T) {
	err := audit.AppendCheckpoint("/nonexistent/dir/run.json", []byte(`{}`))
	assert.Error(t, err)
}
