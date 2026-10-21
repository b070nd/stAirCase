package history

import (
	"errors"
	"fmt"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// policyBound is a run bound under terms whose digest is "T", under a signed policy "P".
func policyBound() *chain {
	return (&chain{}).
		add("run_bound", map[string]any{"base_sha": "b", "branch": "refs/heads/staircase/run-1", "worktree": "w", "terms_sha256": "T"}).
		add("policy_snapshot", map[string]any{"digest": "P", "signed": true})
}

// checkedTree adds the phase_checked entry of tree.
func (c *chain) checkedTree(tree string, passed bool) *chain {
	return c.add("phase_checked", map[string]any{"tree": tree, "passed": passed, "checks": []map[string]any{{"command": "go test", "exit_code": 0}}})
}

// authorized adds delivery_authorized naming the entry just before it; set overrides a key of its payload.
func (c *chain) authorized(set ...any) *chain {
	p := map[string]any{"phase_checked": fmt.Sprintf("e%d", len(c.events)), "tree": "t1", "files": map[string]string{"docs/a.md": "1"},
		"terms_sha256": "T", "policy": "P", "mode": ModePolicy}
	for i := 0; i+1 < len(set); i += 2 {
		p[set[i].(string)] = set[i+1]
	}
	return c.add("delivery_authorized", p)
}

// ready is a policy run that approved a file and whose checks passed on tree t1.
func ready() *chain {
	return policyBound().edit("docs/a.md").decided(1, "policy", true, "paths", []string{"docs/a.md"}).checkedTree("t1", true)
}

// TestReplay_refuses_a_delivery_authorization_no_run_can_have_written (ADR 0012, section 3): delivery_authorized follows the
// phase_checked entry of the same tree, once, under the terms and the signed policy the run was bound to; no request, decision, launch,
// resume or new review follows it, and only the commit-naming and evidence events do.
func TestReplay_refuses_a_delivery_authorization_no_run_can_have_written(t *testing.T) {
	for name, tc := range map[string]struct {
		c     *chain
		entry int
		why   string
	}{
		"with no checks before it":                  {policyBound().authorized("phase_checked", "e1"), 3, "phase_checked"},
		"naming an entry that is not on the chain":  {ready().authorized("phase_checked", "e99"), 6, "phase_checked"},
		"naming an entry that is not a check":       {ready().authorized("phase_checked", "e2"), 6, "phase_checked"},
		"naming an older check":                     {ready().checkedTree("t1", true).authorized("phase_checked", "e5"), 7, "latest"},
		"for another tree than the checks ran on":   {ready().authorized("tree", "t2"), 6, "tree"},
		"on checks that failed":                     {policyBound().edit("docs/a.md").decided(1, "policy", true).checkedTree("t1", false).authorized(), 6, "did not pass"},
		"in a mode nobody defined":                  {ready().authorized("mode", "review"), 6, "mode"},
		"with no mode":                              {ready().authorized("mode", ""), 6, "mode"},
		"under other terms than the run was bound":  {ready().authorized("terms_sha256", "X"), 6, "terms"},
		"under no terms digest at all":              {ready().authorized("terms_sha256", ""), 6, "terms"},
		"under another policy than the snapshot":    {ready().authorized("policy", "Q"), 6, "policy"},
		"after a review was offered":                {ready().add("awaiting_review", map[string]any{"tree": "t1", "phase_checked": "e5"}).authorized("phase_checked", "e5"), 7, "review"},
		"twice":                                     {ready().authorized().authorized("phase_checked", "e5"), 7, "already"},
		"a request after it":                        {ready().authorized().req("file_edit"), 7, "follows delivery_authorized"},
		"a decision after it":                       {ready().edit("docs/b.md").authorized("phase_checked", "e5").decided(2, "policy", true), 8, "follows delivery_authorized"},
		"a final review after it":                   {ready().authorized().final(2, true, nil), 7, "follows delivery_authorized"},
		"a resume after it":                         {ready().authorized().add("run_resumed", map[string]any{"segment": 2}), 7, "delivery_authorized"},
		"a launch after it":                         {ready().authorized().claim(1, "g1"), 7, "delivery_authorized"},
		"a new check after it":                      {ready().authorized().checkedTree("t1", true), 7, "follows delivery_authorized"},
		"an offered review after it":                {ready().authorized().add("awaiting_review", map[string]any{"tree": "t1"}), 7, "follows delivery_authorized"},
		"the owner's decision on a review after it": {ready().authorized().add("review_accepted", nil), 7, "follows delivery_authorized"},
		"a new policy after it":                     {ready().authorized().add("policy_snapshot", map[string]any{"digest": "P", "signed": true}), 7, "follows delivery_authorized"},
		"an unreadable authorization":               {&chain{events: append(ready().events, domain.RunEventLog{EventType: "delivery_authorized", Payload: "["})}, 6, "unreadable"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := replay(tc.c.events)
			var g *GrammarError
			require.True(t, errors.As(err, &g), "%v", err)
			assert.Equal(t, tc.entry, g.Index, g.Error())
			assert.Contains(t, g.Error(), tc.why)
		})
	}
}

// TestReplay_a_delivery_authorization_is_followed_by_the_commit_and_the_evidence: the events a delivery writes after the
// authorization read; the authorization seals the run until its commit is named.
func TestReplay_a_delivery_authorization_is_followed_by_the_commit_and_the_evidence(t *testing.T) {
	h := ready().authorized().replay(t)
	require.NotNil(t, h.Authorized)
	assert.Equal(t, "t1", h.Authorized.Tree)
	assert.Equal(t, ModePolicy, h.Authorized.Mode)
	assert.Equal(t, "e5", h.Authorized.PhaseChecked)
	assert.Equal(t, "e6", h.Authorized.Entry)
	assert.Equal(t, "delivery_authorized", h.Sealed, "the agent's part of the run is over: it is not continued")

	h = ready().authorized().
		add("commit_prepared", map[string]any{"commit": "c1", "base": "b", "tree": "t1"}).
		add("check_ran", map[string]any{"command": "go test"}).
		add("certificate_issued", map[string]any{"commit": "c1", "cal": 3}).
		add("run_path", map[string]any{"phases": []string{"FINALIZE"}, "status": "SUCCESS"}).replay(t)
	assert.Equal(t, "commit_prepared", h.Sealed, "the commit-naming event is the seal once there is one")
	assert.True(t, h.Delivers("certificate_issued", "c1"))
	out, _ := h.Outcome()
	assert.Equal(t, OutcomeDelivered, out)

	h = ready().authorized().add("commit_prepared", map[string]any{"commit": "c1"}).
		add("evidence_failed", map[string]any{"commit": "c1", "errors": []string{"ledger"}}).replay(t)
	require.NotNil(t, h.Authorized)

	// a launch whose end is recorded after the authorization (the agent stopped before it) is not a new launch
	h = policyBound().claim(1, "g1").edit("docs/a.md").decided(1, "policy", true).checkedTree("t1", true).authorized().
		launchEnded(1, "g1", LaunchExited).replay(t)
	assert.Equal(t, 0, h.Lost())

	assert.Nil(t, ready().replay(t).Authorized, "a run that did not authorize anything has none")
}

// TestOutcome_a_delivery_authorized_but_not_made: a run that wrote the authorization and ended with no commit is blocked, and
// recover finishes it; one that is still going is running.
func TestOutcome_a_delivery_authorized_but_not_made(t *testing.T) {
	out, why := ready().authorized().replay(t).Outcome()
	assert.Equal(t, OutcomeRunning, out)
	assert.Contains(t, why, "does not say it ended")

	out, why = ready().authorized().ended("KILLED").replay(t).Outcome()
	assert.Equal(t, OutcomeBlocked, out)
	assert.Contains(t, why, "authorized")
	assert.Contains(t, why, "staircase recover")
}
