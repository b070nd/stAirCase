package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/diag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hostLog is the text and size of the one session's host log.
func hostLog(t *testing.T, ws string) (string, int64) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(ws, "host"))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	b, err := os.ReadFile(filepath.Join(ws, "host", entries[0].Name()))
	require.NoError(t, err)
	return string(b), int64(len(b))
}

// The host log (where a hosted session's own output goes, never the runner's stream) stops growing at its bound and says that it
// did (V11-C01 scope 8).
func TestHost_log_is_bounded_and_says_what_it_dropped(t *testing.T) {
	_, ws, _ := hostRepo(t)
	old := diag.MaxLogBytes
	diag.MaxLogBytes = 1024
	t.Cleanup(func() { diag.MaxLogBytes = old })

	hostedRun(t, cloudroomArgs("11111111-2222-3333-4444-555555555555"))

	log, size := hostLog(t, ws)
	assert.LessOrEqual(t, size, int64(1024))
	assert.Contains(t, log, "bound of 1024 bytes")
	assert.Regexp(t, `dropped \d+ bytes in all`, log)
}

// A hosted session that fails: its runner sees an error with the cause and no stderr text (a runner may keep what the program it
// runs prints; Cloudroom keeps stderr tails in its records), and the credential the session was given (--pass-env) is in no log.
func TestHost_failed_session_keeps_stderr_and_the_credential_out_of_the_runners_view(t *testing.T) {
	_, ws, _ := hostRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(ws, "host.json"),
		[]byte(`{"allow":["HEALTH.md"],"checks":["test -f HEALTH.md"],"pass_env":["HOST_TOKEN"]}`), 0o600))
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\necho \"TAIL-MARKER rejected $HOST_TOKEN\" >&2\nexit 1\n"), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOST_TOKEN", "tok-HOSTED-0815")

	var out bytes.Buffer
	err := hostClaude(cloudroomArgs("11111111-2222-3333-4444-555555555555"), strings.NewReader(`{"type":"user"}`+"\n"), &out)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "TAIL-MARKER")
	assert.NotContains(t, err.Error(), "tok-HOSTED-0815")
	assert.NotContains(t, out.String(), "TAIL-MARKER", "nothing of it reaches the runner's stream")

	log, _ := hostLog(t, ws)
	assert.Contains(t, log, "TAIL-MARKER rejected <REDACTED>", "the person debugging finds it here, scrubbed")
	assert.NotContains(t, log, "tok-HOSTED-0815")
}
