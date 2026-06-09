//go:build !windows

package runtime_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b070nd/staircase-core/src/internal/obs"
	"github.com/b070nd/staircase-core/src/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lockedBuffer is a goroutine-safe bytes.Buffer for capturing log output
// written by the stderr-forwarding goroutine.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestLaunchPython_scrubs_stderr proves that a delivered secret printed by the
// Python process to stderr is redacted before reaching the orchestrator log
// (audit finding: stderr secret leak).
func TestLaunchPython_scrubs_stderr(t *testing.T) {
	const secret = "hunter2-delivered-secret-value"

	wsDir := t.TempDir()
	binDir := filepath.Join(wsDir, "venv", "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	// Fake python: print the secret to stderr, drain stdin, exit cleanly.
	fake := "#!/bin/sh\necho \"leak: " + secret + "\" >&2\ncat > /dev/null\nexit 0\n"
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "python"), []byte(fake), 0o755))

	var buf lockedBuffer
	obs.InitJSON(&buf, slog.LevelDebug)
	t.Cleanup(func() { obs.InitJSON(io.Discard, slog.LevelInfo) })

	scriptFile, err := os.CreateTemp(t.TempDir(), "script-*.py")
	require.NoError(t, err)
	t.Cleanup(func() { _ = scriptFile.Close() })

	proc, err := runtime.LaunchPython(context.Background(), wsDir, scriptFile,
		filepath.Join(t.TempDir(), "ipc.sock"), "test-token",
		runtime.LaunchPythonOptions{
			ScrubStderr: func(line string) string {
				return strings.ReplaceAll(line, secret, "[REDACTED]")
			},
		})
	require.NoError(t, err)

	select {
	case <-proc.Done:
	case <-time.After(10 * time.Second):
		t.Fatal("fake python did not exit")
	}
	// The stderr goroutine races process exit by a hair; poll briefly.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(buf.String(), "[REDACTED]") {
		time.Sleep(20 * time.Millisecond)
	}

	out := buf.String()
	assert.NotContains(t, out, secret, "plaintext secret must never reach the log")
	assert.Contains(t, out, "[REDACTED]", "scrubbed stderr line must be logged")
}
