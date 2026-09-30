package orchestrator_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// evidenceRun makes one run that commits health.txt. blockAudit makes the
// workspace's audit directory unwritable (a file stands where it should be),
// so the ledger and the certificate cannot be written; noKey leaves the
// workspace without a signing key.
func evidenceRun(t *testing.T, opts orchestrator.RunOptions, blockAudit, noKey bool) (runtest.Result, orchestrator.RunSummary) {
	r := runtest.Run(t, runtest.Options{Agent: writeFile("health.txt", "ok\n"), Run: opts,
		Setup: func(_ *persistence.Store, ws string, _ int64) {
			if !noKey {
				require.NoError(t, crypto.GenerateSigningKey(ws))
			}
			if blockAudit {
				require.NoError(t, os.WriteFile(filepath.Join(ws, "audit"), []byte("not a directory"), 0o600))
			}
		}})
	var s orchestrator.RunSummary
	b, err := os.ReadFile(filepath.Join(r.WsDir, "runs", "1", "summary.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &s))
	return r, s
}

// TestRun_says_what_evidence_the_commit_has: a commit's run summary names its
// outcome (certified, or delivered without evidence and why) and the audit
// chain records the failure. The commit stays delivered either way (F100).
func TestRun_says_what_evidence_the_commit_has(t *testing.T) {
	r, s := evidenceRun(t, orchestrator.RunOptions{}, false, false)
	require.NoError(t, r.Err)
	assert.Equal(t, orchestrator.OutcomeCertified, s.Outcome)
	assert.Empty(t, s.EvidenceErrors)

	r, s = evidenceRun(t, orchestrator.RunOptions{}, true, false)
	require.NoError(t, r.Err, "without --require-evidence the run still succeeds")
	assert.Equal(t, persistence.RunStatusSuccess, r.Run.Status)
	assert.NotEmpty(t, r.Run.GitCommitHash, "the commit was delivered")
	assert.Equal(t, orchestrator.OutcomeWithoutEvidence, s.Outcome)
	assert.NotEmpty(t, s.EvidenceErrors)
	assert.Contains(t, r.Types(), "evidence_failed")

	r, s = evidenceRun(t, orchestrator.RunOptions{}, false, true)
	require.NoError(t, r.Err)
	assert.Equal(t, orchestrator.OutcomeWithoutEvidence, s.Outcome, "a workspace with no signing key delivers uncertified, and says so")
	assert.Contains(t, s.EvidenceErrors[0], "signing key")
}

// TestRun_require_evidence: with RequireEvidence a commit whose evidence is
// incomplete makes the run fail loudly, but the commit is reported as what it
// is: delivered, with its hash in the record, not relabelled as undelivered.
func TestRun_require_evidence(t *testing.T) {
	for name, tc := range map[string]struct{ blockAudit, noKey bool }{
		"certificate cannot be written": {blockAudit: true},
		"no signing key":                {noKey: true},
	} {
		t.Run(name, func(t *testing.T) {
			r, s := evidenceRun(t, orchestrator.RunOptions{RequireEvidence: true}, tc.blockAudit, tc.noKey)
			require.ErrorIs(t, r.Err, orchestrator.ErrEvidenceIncomplete)
			assert.Equal(t, persistence.RunStatusSuccess, r.Run.Status, "the commit was made")
			assert.NotEmpty(t, r.Run.GitCommitHash)
			assert.Equal(t, r.Run.GitCommitHash, s.CommitHash)
			assert.Equal(t, orchestrator.OutcomeWithoutEvidence, s.Outcome)
			out, err := r.OnBranch("health.txt")
			require.NoError(t, err)
			assert.Equal(t, "ok\n", out)
		})
	}

	r, s := evidenceRun(t, orchestrator.RunOptions{RequireEvidence: true}, false, false)
	require.NoError(t, r.Err)
	assert.Equal(t, orchestrator.OutcomeCertified, s.Outcome)
}
