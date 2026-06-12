//go:build !windows

package runtime_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/b070nd/staircase-core/src/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLaunchPython_propagates_traceparent proves the W3C trace context set in
// LaunchPythonOptions arrives in the bootstrap message the Python process
// reads on stdin (B2: distributed-trace propagation across the process
// boundary).
func TestLaunchPython_propagates_traceparent(t *testing.T) {
	wsDir := t.TempDir()
	binDir := filepath.Join(wsDir, "venv", "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	bootDump := filepath.Join(wsDir, "bootstrap.json")
	// Fake python: copy stdin (the bootstrap line) to a file, exit cleanly.
	fake := "#!/bin/sh\ncat > " + bootDump + "\nexit 0\n"
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "python"), []byte(fake), 0o755))

	scriptFile, err := os.CreateTemp(t.TempDir(), "script-*.py")
	require.NoError(t, err)
	t.Cleanup(func() { _ = scriptFile.Close() })

	const tp = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	proc, err := runtime.LaunchPython(context.Background(), wsDir, scriptFile,
		filepath.Join(t.TempDir(), "ipc.sock"), "test-token",
		runtime.LaunchPythonOptions{Traceparent: tp})
	require.NoError(t, err)

	select {
	case <-proc.Done:
	case <-time.After(10 * time.Second):
		t.Fatal("fake python did not exit")
	}

	raw, err := os.ReadFile(bootDump)
	require.NoError(t, err)
	var boot map[string]any
	require.NoError(t, json.Unmarshal(raw, &boot))
	assert.Equal(t, tp, boot["traceparent"], "bootstrap must carry the run span's traceparent")
}
