//go:build !windows

package runtime_test

import (
	"testing"

	"github.com/b070nd/staircase-core/src/internal/runtime"
	"github.com/stretchr/testify/assert"
)

func TestPipInstallArgs_requireHashesOnlyWhenHashed(t *testing.T) {
	// Unpinned requirements (current state): --require-hashes must NOT be added,
	// otherwise pip fails closed and breaks `staircase init`.
	plain := runtime.ExportedPipInstallArgs("/tmp/req.txt", "", []byte("langgraph==0.2.45\n"))
	assert.NotContains(t, plain, "--require-hashes")
	assert.Equal(t, []string{"-m", "pip", "install", "-r", "/tmp/req.txt"}, plain)

	// Hash-pinned lock file: --require-hashes is enforced automatically.
	hashed := runtime.ExportedPipInstallArgs("/tmp/req.txt", "",
		[]byte("langgraph==0.2.45 --hash=sha256:deadbeef\n"))
	assert.Contains(t, hashed, "--require-hashes")

	// Offline wheels flags are appended regardless.
	offline := runtime.ExportedPipInstallArgs("/tmp/req.txt", "/wheels", []byte("x==1\n"))
	assert.Contains(t, offline, "--no-index")
	assert.Contains(t, offline, "--find-links")
	assert.Contains(t, offline, "/wheels")
}
