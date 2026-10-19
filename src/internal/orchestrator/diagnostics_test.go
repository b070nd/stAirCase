package orchestrator_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/diag"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func debugLog(t *testing.T, wsDir string) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(wsDir, "log"))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	b, err := os.ReadFile(filepath.Join(wsDir, "log", entries[0].Name()))
	require.NoError(t, err)
	return string(b)
}

// The --debug log stops growing at its bound and says that it did (V11-C01 scope 8: diagnostics are bounded, and their loss is
// visible); the run's audit chain, which is the record, is whole.
func TestDebugLog_is_bounded_and_says_what_it_dropped(t *testing.T) {
	old := diag.MaxLogBytes
	diag.MaxLogBytes = 2048
	t.Cleanup(func() { diag.MaxLogBytes = old })

	r := runtest.Run(t, runtest.Options{Run: orchestrator.RunOptions{Debug: true},
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			for range 60 {
				env.Emit(ctx, orchestrator.Usage{Agent: "planner", Model: "claude-sonnet-4-6", InputTokens: 7})
			}
			return nil
		}),
	})
	require.NoError(t, r.Err)

	entries, err := os.ReadDir(filepath.Join(r.WsDir, "log"))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	fi, err := os.Stat(filepath.Join(r.WsDir, "log", entries[0].Name()))
	require.NoError(t, err)
	assert.LessOrEqual(t, fi.Size(), int64(2048))
	log := debugLog(t, r.WsDir)
	assert.Contains(t, log, "bound of 2048 bytes")
	assert.Regexp(t, `dropped \d+ bytes in all`, log)
	emits := 0
	for _, e := range r.Events {
		if e.EventType == "state_emit" {
			emits++
		}
	}
	assert.Equal(t, 60, emits, "every message is on the audit chain, whatever the debug log dropped")
}

// A secret handed to an agent reaches no log: not one the secret store delivered, and not one the harness took from its own
// environment (--pass-env), which the agent's adapter declares with Deliver.
func TestDebugLog_holds_no_secret_the_agent_was_handed(t *testing.T) {
	const stored, passed = "sk-test-STORED-4711", "tok-PASSED-ENV-0815"
	r := runtest.Run(t, runtest.Options{Run: orchestrator.RunOptions{Debug: true},
		Setup: func(s *persistence.Store, wsDir string, projectID int64) {
			key, err := crypto.LoadKey(wsDir)
			require.NoError(t, err)
			enc, err := crypto.Encrypt(key, stored)
			require.NoError(t, err)
			_, err = s.CreateSecret("OPENAI_API_KEY", enc, &projectID)
			require.NoError(t, err)
		},
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			v, err := env.Secret("OPENAI_API_KEY")
			require.NoError(t, err)
			env.Deliver(passed)
			env.Emit(ctx, orchestrator.Usage{Agent: "planner", Model: "m", Content: "saw " + v + " and " + passed})
			return nil
		}),
	})
	require.NoError(t, r.Err)

	log := debugLog(t, r.WsDir)
	assert.Contains(t, log, "saw <REDACTED> and <REDACTED>")
	for _, secret := range []string{stored, passed} {
		assert.NotContains(t, log, secret)
		for _, e := range r.Events {
			assert.NotContains(t, e.Payload, secret, "audit event %s", e.EventType)
		}
	}
	assert.Contains(t, diag.Delivered(), passed, "the process's other logs (the hosted session's) scrub it too")
}

// stderrErr is how an adapter reports a failed agent: the cause, with the agent's stderr tail kept beside it.
type stderrErr struct{ tail string }

func (e stderrErr) Error() string      { return "claude code: exit status 1" }
func (e stderrErr) StderrTail() string { return e.tail }

// The tail of an agent's stderr is a local diagnostic (Cloudroom persisted its tails into records despite saying it did not,
// PROTOCOL.md): it is not in the error a run ends with, which a caller may log or pass on, nor in the audit chain, the
// certificate or the ledger. It is kept in the debug log, scrubbed.
func TestAgentStderr_never_reaches_a_record(t *testing.T) {
	const tailText, secret = "TAIL-MARKER panic: connection refused", "tok-PASSED-ENV-0815"
	r := runtest.Run(t, runtest.Options{Run: orchestrator.RunOptions{Debug: true},
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			env.Deliver(secret)
			return stderrErr{tail: tailText + " " + secret}
		}),
	})
	require.Error(t, r.Err)
	assert.Equal(t, persistence.RunStatusFailed, r.Run.Status)

	assert.NotContains(t, r.Err.Error(), "TAIL-MARKER", "not in the error the run ends with")
	assert.Contains(t, r.Err.Error(), "exit status 1")
	for _, e := range r.Events {
		assert.NotContains(t, e.Payload, "TAIL-MARKER", "audit event %s", e.EventType)
	}
	require.NoError(t, filepath.WalkDir(r.WsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(path, filepath.Join(r.WsDir, "log")) {
			return err
		}
		b, rerr := os.ReadFile(path)
		require.NoError(t, rerr)
		assert.NotContains(t, string(b), "TAIL-MARKER", "%s is a record", path)
		return nil
	}))

	log := debugLog(t, r.WsDir)
	assert.Contains(t, log, "TAIL-MARKER panic: connection refused <REDACTED>", "the tail is kept for the person debugging, scrubbed")
	assert.NotContains(t, log, secret)
}
