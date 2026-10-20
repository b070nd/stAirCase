package orchestrator_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// policyDelivery is an away run delivering by policy, signed by key, with every condition the terms alone decide met.
func policyDelivery(key string) orchestrator.RunOptions {
	return orchestrator.RunOptions{Terms: orchestrator.Terms{Agreed: "dev@example.com", Away: true, Checks: []string{"true"},
		Deliver: orchestrator.DeliverPolicy, DeliverPaths: []string{"docs/**"}, SignKey: key, SignAs: "dev@example.com",
		ProtectedPaths: []string{"**/*_test.go"}}}
}

// TestDeliver_review_is_not_in_the_saved_terms (ADR 0012): asking for review is the default, so a run that does is saved byte for byte
// as a run saved before the field existed, and a policy delivery is saved and bound by the same digest as every other term.
func TestDeliver_review_is_not_in_the_saved_terms(t *testing.T) {
	saved := func(r runtest.Result) string {
		b, err := os.ReadFile(filepath.Join(r.WsDir, "journal", fmt.Sprintf("run-%d.options.json", r.Run.ID)))
		require.NoError(t, err)
		return string(b)
	}
	noop := orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error { return nil })
	opts := awayOpts()
	opts.Deliver = orchestrator.DeliverReview
	review := runtest.Run(t, runtest.Options{Run: opts, Agent: noop})
	require.NoError(t, review.Err)
	assert.NotContains(t, saved(review), "deliver")

	key, _ := sshKey(t, "dev@example.com")
	policyRun := runtest.Run(t, runtest.Options{Run: policyDelivery(key), Agent: noop})
	require.Error(t, policyRun.Err, "refused: the default policy is not signed")
	assert.Contains(t, saved(policyRun), `"deliver":"policy","deliver_paths":["docs/**"]`)
	sum := sha256.Sum256([]byte(saved(policyRun)))
	assert.Equal(t, hex.EncodeToString(sum[:]), payload(t, policyRun, "run_bound")["terms_sha256"], "bound by digest")
}

// TestDeliver_policy_refused_when_the_policy_is_not_signed (ADR 0012, condition 1): an edited policy file must not arm unattended
// delivery. The run is refused when its first segment starts, before the agent runs, and nothing is delivered; with the policy
// signed the same terms start.
func TestDeliver_policy_refused_when_the_policy_is_not_signed(t *testing.T) {
	key, _ := sshKey(t, "dev@example.com")
	ran := false
	agent := orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error { ran = true; return nil })

	unsigned := runtest.Run(t, runtest.Options{Run: policyDelivery(key), Agent: agent})
	require.Error(t, unsigned.Err)
	assert.Contains(t, unsigned.Err.Error(), "signed policy")
	assert.Contains(t, unsigned.Err.Error(), "staircase policy sign")
	assert.False(t, ran, "the agent never started")
	assert.Equal(t, persistence.RunStatusFailed, unsigned.Run.Status)
	assert.Empty(t, strings.TrimSpace(git(t, unsigned.Repo, "log", "--oneline", "main..staircase/run-1")), "nothing was delivered")

	ran = false
	signed := runtest.Run(t, runtest.Options{Run: policyDelivery(key), Agent: agent,
		Setup: func(s *persistence.Store, wsDir string, projectID int64) {
			require.NoError(t, crypto.GenerateSigningKey(wsDir))
			priv, err := crypto.LoadSigningKey(wsDir)
			require.NoError(t, err)
			data, err := os.ReadFile(filepath.Join(wsDir, "policy.json"))
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(wsDir, policy.PolicySigFile), []byte(crypto.Sign(priv, data)), 0o644))
		}})
	if signed.Err != nil {
		assert.NotContains(t, signed.Err.Error(), "signed policy")
	}
	assert.True(t, ran, "a signed policy starts the run")
}
