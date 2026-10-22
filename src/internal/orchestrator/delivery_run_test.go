package orchestrator_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// policyRun is an away run delivering by policy under a signed policy that holds rules, with a bare repository configured as its
// origin so a test can see that nothing is ever pushed. The key signs the run's initiator.
type policyRun struct {
	rules   string // the policy's rules; default: approve every file edit
	setup   func(s *persistence.Store, wsDir string, projectID int64)
	mutate  func(*orchestrator.Terms)
	agent   orchestrator.AgentFunc
	remote  string // the bare repository
	base    map[string]runtest.File
	unsign  bool
	options func(*orchestrator.RunOptions)
	ctx     context.Context // the run's parent context; default background
}

func (p *policyRun) run(t *testing.T) runtest.Result {
	t.Helper()
	key, _ := sshKey(t, "dev@example.com")
	p.remote = filepath.Join(t.TempDir(), "origin.git")
	out, err := exec.Command("git", "init", "-q", "--bare", p.remote).CombinedOutput()
	require.NoError(t, err, string(out))
	opts := policyDelivery(key)
	if p.mutate != nil {
		p.mutate(&opts.Terms)
	}
	if p.options != nil {
		p.options(&opts)
	}
	return runtest.Run(t, runtest.Options{Base: p.base, Run: opts, Agent: p.agent, Ctx: p.ctx,
		Setup: func(s *persistence.Store, wsDir string, projectID int64) {
			require.NoError(t, crypto.GenerateSigningKey(wsDir))
			rules := p.rules
			if rules == "" {
				rules = `{"rules":[{"action_types":["file_edit"],"effect":"approve"}]}`
			}
			require.NoError(t, os.WriteFile(filepath.Join(wsDir, "policy.json"), []byte(rules), 0o600))
			priv, err := crypto.LoadSigningKey(wsDir)
			require.NoError(t, err)
			data, err := os.ReadFile(filepath.Join(wsDir, "policy.json"))
			require.NoError(t, err)
			if !p.unsign {
				require.NoError(t, os.WriteFile(filepath.Join(wsDir, policy.PolicySigFile), []byte(crypto.Sign(priv, data)), 0o644))
			}
			proj, err := s.GetProject(projectID)
			require.NoError(t, err)
			git(t, proj.SourcePath, "remote", "add", "origin", p.remote)
			if p.setup != nil {
				p.setup(s, wsDir, projectID)
			}
		}})
}

// writes is an agent that proposes each file in turn.
func writes(files ...[2]string) orchestrator.AgentFunc {
	return func(ctx context.Context, env *orchestrator.AgentEnv) error {
		for _, f := range files {
			write(ctx, env, f[0], f[1])
		}
		return nil
	}
}

func awaitingReviewOf(t *testing.T, r runtest.Result) map[string]any {
	t.Helper()
	require.NoError(t, r.Err)
	require.Equal(t, persistence.RunStatusAwaitingReview, r.Run.Status)
	require.Empty(t, r.Run.GitCommitHash, "nothing is committed")
	assert.Nil(t, payload(t, r, "delivery_authorized"), "no authorization was written")
	assert.Nil(t, payload(t, r, "commit_prepared"))
	aw := payload(t, r, "awaiting_review")
	require.NotNil(t, aw)
	return aw
}

// TestDeliverByPolicy_delivers_exactly_the_approved_bytes_once (ADR 0012, section 3 and 4): a rule of the signed policy approves
// a change inside the delivery scope, the checks pass on exactly that tree, and the run commits it to its branch without a person: one
// commit, the authorization on the chain before the commit is named, no review offered or asked; the certificate keeps the level it
// computes, says no person reviewed the whole change, carries the optional field delivery, and has no operator among its decisions.
// A remote that is configured is untouched, and only the run branch moved.
func TestDeliverByPolicy_delivers_exactly_the_approved_bytes_once(t *testing.T) {
	p := &policyRun{agent: writes([2]string{"docs/a.md", "approved bytes\n"}, [2]string{"docs/b.md", "more\n"})}
	r := p.run(t)
	require.NoError(t, r.Err)
	require.Equal(t, persistence.RunStatusSuccess, r.Run.Status)
	require.NotEmpty(t, r.Run.GitCommitHash)

	// exactly the approved bytes, once
	got, err := r.OnBranch("docs/a.md")
	require.NoError(t, err)
	assert.Equal(t, "approved bytes\n", got)
	assert.Equal(t, "1", strings.TrimSpace(git(t, r.Repo, "rev-list", "--count", "main..staircase/run-1")))

	// the chain: checked, authorized, then the commit named, and nobody asked
	types := r.Types()
	idx := func(event string) int {
		for i, e := range types {
			if e == event {
				return i
			}
		}
		return -1
	}
	require.NotEqual(t, -1, idx("delivery_authorized"))
	assert.Less(t, idx("phase_checked"), idx("delivery_authorized"))
	assert.Less(t, idx("delivery_authorized"), idx("commit_prepared"))
	assert.Less(t, idx("commit_prepared"), idx("certificate_issued"))
	assert.Equal(t, -1, idx("awaiting_review"), "no review was offered")
	assert.Equal(t, 1, strings.Count(strings.Join(types, " "), "delivery_authorized"))
	assert.Equal(t, 0, strings.Count(strings.Join(sourcesOf(t, r), " "), "operator"), "no person decided anything")

	auth := payload(t, r, "delivery_authorized")
	assert.Equal(t, "policy", auth["mode"])
	assert.Equal(t, payload(t, r, "phase_checked")["tree"], auth["tree"])
	assert.Equal(t, strings.TrimSpace(git(t, r.Repo, "rev-parse", "staircase/run-1^{tree}")), auth["tree"], "the tree it authorized is the tree committed")
	assert.Equal(t, payload(t, r, "run_bound")["terms_sha256"], auth["terms_sha256"])
	assert.Equal(t, payload(t, r, "policy_snapshot")["digest"], auth["policy"])
	h, err := history.Read(r.Store, r.Run.ID)
	require.NoError(t, err)
	assert.Equal(t, auth["phase_checked"], mustLastChecked(t, h))
	out, _ := h.Outcome()
	assert.Equal(t, history.OutcomeDelivered, out)

	// the certificate
	s := certificateOf(t, r)
	assert.Equal(t, 3, s.Predicate.CAL, "the level it computes, no cap: %v", s.Predicate.Notes)
	assert.Equal(t, "policy", s.Predicate.Delivery)
	assert.Zero(t, s.Predicate.Decisions["operator"])
	assert.Equal(t, 2, s.Predicate.Decisions["policy"])
	var note string
	for _, n := range s.Predicate.Notes {
		if strings.Contains(n, "no person reviewed the whole change") {
			note = n
		}
	}
	require.NotEmpty(t, note, "the certificate says no person reviewed the whole change: %v", s.Predicate.Notes)
	assert.Contains(t, note, s.Predicate.Policy, "it names the policy whose rules approved")
	assert.Contains(t, note, "only the run branch moved and nothing was pushed")

	// nothing reached beyond the run branch
	assert.Empty(t, strings.TrimSpace(git(t, p.remote, "for-each-ref")), "the configured remote was not pushed to")
	assert.Equal(t, "main\nstaircase/run-1", strings.TrimSpace(git(t, r.Repo, "for-each-ref", "--format=%(refname:short)", "refs/heads")), "only the run branch was added")
	assert.Equal(t, strings.TrimSpace(git(t, r.Repo, "rev-parse", "main^{tree}")), strings.TrimSpace(git(t, r.Repo, "rev-parse", "staircase/run-1~1^{tree}")), "main is where it was")
}

func certificateOf(t *testing.T, r runtest.Result) certificate.Statement {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.WsDir, "audit", "run-1.certificate.json"))
	require.NoError(t, err)
	var env certificate.Envelope
	require.NoError(t, json.Unmarshal(b, &env))
	pub, err := crypto.LoadSigningPublicKey(r.WsDir)
	require.NoError(t, err)
	s, err := certificate.Open(env, pub)
	require.NoError(t, err)
	return s
}

func mustLastChecked(t *testing.T, h history.History) string {
	t.Helper()
	entry, _, ok := h.LastChecked()
	require.True(t, ok)
	return entry
}

func sourcesOf(t *testing.T, r runtest.Result) []string { return sources(t, r) }

// refusedBy asserts the run fell back to its owner for exactly the condition n, on the chain and on the screen's source of truth
// (the review it offers), with the run branch still at its base and no file delivered.
func refusedBy(t *testing.T, r runtest.Result, n int, cite string) {
	t.Helper()
	aw := awaitingReviewOf(t, r)
	why, _ := aw["delivery_refused"].(string)
	assert.Contains(t, why, "condition "+string(rune('0'+n))+" (", "the offered review names the condition that sent the run to the owner")
	assert.Contains(t, strings.ToLower(why), strings.ToLower(cite))
	assert.Equal(t, strings.TrimSpace(git(t, r.Repo, "rev-parse", "main")), strings.TrimSpace(git(t, r.Repo, "rev-parse", "staircase/run-1")), "the run branch is at its base")
	assert.Empty(t, strings.TrimSpace(git(t, r.Repo, "for-each-ref", "refs/notes")), "no certificate was issued")
	rv, err := orchestrator.NewRunner(r.Store, r.WsDir).ReviewOf(r.Run.ID)
	require.NoError(t, err)
	assert.Contains(t, rv.DeliveryRefused, "condition "+string(rune('0'+n)), "the owner is told on the review")
	h, err := history.Read(r.Store, r.Run.ID)
	require.NoError(t, err)
	assert.Nil(t, h.Authorized)
	_, why2 := h.Outcome()
	assert.Contains(t, why2, "staircase decide")
	assert.Equal(t, 0, strings.Count(strings.Join(r.Types(), " "), "delivery_authorized"))
}

// TestDeliverByPolicy_one_parked_proposal_sends_the_whole_run_to_the_owner (condition 3): the part a rule approved is not delivered
// either, since it may depend on what is parked.
func TestDeliverByPolicy_one_parked_proposal_sends_the_whole_run_to_the_owner(t *testing.T) {
	// a rule approves documentation; the third edit changes a build file, which a person decides whatever the policy says
	p := &policyRun{rules: `{"rules":[{"action_types":["file_edit"],"effect":"approve","paths":["docs/**"]}]}`,
		agent: writes([2]string{"docs/a.md", "fine\n"}, [2]string{"docs/b.md", "also fine\n"}, [2]string{"Makefile", "all:\n"})}
	r := p.run(t)
	assert.Equal(t, []string{"file_edit:policy", "file_edit:policy", "file_edit:parked"}, sources(t, r))
	refusedBy(t, r, 3, "parked")
	_, err := r.OnBranch("docs/a.md")
	assert.Error(t, err, "what the rule approved is not delivered either")
}

// TestDeliverByPolicy_a_change_approved_on_evidence_goes_to_the_owner (condition 2): evidence, the agreed task or a model never
// stand in for a person's review of the whole change, even when the checks pass.
func TestDeliverByPolicy_a_change_approved_on_evidence_goes_to_the_owner(t *testing.T) {
	// the rule approves only docs; the change to the notes is approved on evidence (the checks pass on it)
	p := &policyRun{rules: `{"rules":[{"action_types":["file_edit"],"effect":"approve","paths":["docs/**"]}]}`,
		agent: writes([2]string{"docs/a.md", "by a rule\n"}, [2]string{"docs/note.txt", "by a rule too\n"})}
	r := p.run(t)
	require.Equal(t, persistence.RunStatusSuccess, r.Run.Status, "control: both approved by the rule, delivered")

	p = &policyRun{rules: `{"rules":[]}`, agent: writes([2]string{"docs/a.md", "approved on evidence\n"})}
	r = p.run(t)
	assert.Equal(t, []string{"file_edit:evidence"}, sources(t, r))
	refusedBy(t, r, 2, "evidence")

	p = &policyRun{rules: `{"rules":[{"action_types":["file_edit"],"effect":"approve","paths":["docs/a.md"]}]}`,
		agent: writes([2]string{"docs/a.md", "by a rule\n"}, [2]string{"docs/b.md", "on evidence\n"})}
	r = p.run(t)
	assert.Equal(t, []string{"file_edit:policy", "file_edit:evidence"}, sources(t, r))
	refusedBy(t, r, 2, "evidence")
}

// TestDeliverByPolicy_a_run_the_owner_accepted_is_not_certified_as_delivered_by_policy: the terms say policy, but a condition failed and
// a person accepted the whole change, so that certificate has no delivery field and no note that nobody reviewed the change.
func TestDeliverByPolicy_a_run_the_owner_accepted_is_not_certified_as_delivered_by_policy(t *testing.T) {
	p := &policyRun{rules: `{"rules":[]}`, agent: writes([2]string{"docs/a.md", "approved on evidence\n"})}
	r := p.run(t)
	require.Equal(t, persistence.RunStatusAwaitingReview, r.Run.Status)
	_, err := orchestrator.NewRunner(r.Store, r.WsDir).Decide(context.Background(), r.Run.ID, orchestrator.DecideOptions{Accept: true})
	require.NoError(t, err)

	s := certificateOf(t, r)
	assert.Empty(t, s.Predicate.Delivery, "a person reviewed the whole change: it was not delivered by policy")
	assert.NotContains(t, strings.Join(s.Predicate.Notes, " "), "no person reviewed the whole change")
	b, err := json.Marshal(s.Predicate)
	require.NoError(t, err)
	assert.NotContains(t, string(b), `"delivery"`)
}

// TestDeliverByPolicy_a_path_outside_the_delivery_scope_goes_to_the_owner (condition 4).
func TestDeliverByPolicy_a_path_outside_the_delivery_scope_goes_to_the_owner(t *testing.T) {
	p := &policyRun{agent: writes([2]string{"docs/a.md", "inside\n"}, [2]string{"src/b.go", "package b\n"})}
	r := p.run(t)
	assert.Equal(t, []string{"file_edit:policy", "file_edit:policy"}, sources(t, r))
	refusedBy(t, r, 4, "src/b.go")
}

// TestDeliverByPolicy_never_delivers_a_sensitive_or_protected_path_even_if_a_rule_approves_it: the bars of the deciders park such a
// change (whatever the policy says), and delivery by policy repeats the bars (condition 4) so that it does not depend on that. A
// scope of "everything" changes nothing.
func TestDeliverByPolicy_never_delivers_a_sensitive_or_protected_path_even_if_a_rule_approves_it(t *testing.T) {
	for name, tc := range map[string]struct {
		file string
		n    int
		cite string
	}{
		"a CI workflow":       {".github/workflows/ci.yml", 3, "sensitive"},
		"a CODEOWNERS file":   {"docs/CODEOWNERS", 3, "sensitive"},
		"a build file":        {"Makefile", 3, "sensitive"},
		"a check's own file":  {"docs/x_test.go", 3, "definition of a check"},
		"a protected pattern": {"pkg/deep/thing_test.go", 3, "definition of a check"},
	} {
		t.Run(name, func(t *testing.T) {
			p := &policyRun{mutate: func(tm *orchestrator.Terms) { tm.DeliverPaths = []string{"**"} },
				agent: writes([2]string{"docs/a.md", "fine\n"}, [2]string{tc.file, "x := 1\n"})}
			r := p.run(t)
			require.Equal(t, persistence.RunStatusAwaitingReview, r.Run.Status)
			assert.Equal(t, []string{"file_edit:policy", "file_edit:parked"}, sources(t, r), "the rule approved it, the bar parked it")
			refusedBy(t, r, tc.n, "parked")
			h, err := history.Read(r.Store, r.Run.ID)
			require.NoError(t, err)
			require.Len(t, h.Parked(), 1)
			assert.Contains(t, h.Parked()[0].ParkedWhy, tc.cite, "the owner is told why it was parked")
			assert.Contains(t, h.Parked()[0].ParkedWhy, tc.file)
			_, err = r.OnBranch(tc.file)
			assert.Error(t, err)
		})
	}
}

// TestDeliverByPolicy_terms_the_chain_does_not_bind_go_to_the_owner (condition 1): the saved terms are read back and checked against
// the digest on run_bound. Terms that were changed while the run went (here, by the agent) are not the terms it was bound to, and in
// doubt the owner decides.
func TestDeliverByPolicy_terms_the_chain_does_not_bind_go_to_the_owner(t *testing.T) {
	p := &policyRun{agent: func(ctx context.Context, env *orchestrator.AgentEnv) error {
		write(ctx, env, "docs/a.md", "fine\n")
		path := filepath.Join(env.Workspace, "journal", "run-1.options.json")
		b, err := os.ReadFile(path)
		require.NoError(t, err)
		return os.WriteFile(path, []byte(strings.Replace(string(b), `"deliver_paths":["docs/**"]`, `"deliver_paths":["**"]`, 1)), 0o600)
	}}
	r := p.run(t)
	refusedBy(t, r, 1, "bound")
}

// TestDeliverByPolicy_an_unsigned_policy_never_starts: condition 1 as the terms leave it to the start of the run (ticket 10). The
// evaluation repeats it (TestDeliveryCase_...), so a policy that is not signed can never be what delivers.
func TestDeliverByPolicy_an_unsigned_policy_never_starts(t *testing.T) {
	p := &policyRun{unsign: true, agent: writes([2]string{"docs/a.md", "x\n"})}
	r := p.run(t)
	require.Error(t, r.Err)
	assert.Contains(t, r.Err.Error(), "signed policy")
	assert.Empty(t, r.Run.GitCommitHash)
	assert.Nil(t, payload(t, r, "delivery_authorized"))
}

// TestDeliverByPolicy_review_runs_are_unchanged: the same run in review mode offers its change for review, as it always has.
func TestDeliverByPolicy_review_runs_are_unchanged(t *testing.T) {
	p := &policyRun{agent: writes([2]string{"docs/a.md", "fine\n"}),
		mutate: func(tm *orchestrator.Terms) { tm.Deliver, tm.DeliverPaths = "", nil }}
	r := p.run(t)
	aw := awaitingReviewOf(t, r)
	assert.NotContains(t, aw, "delivery_refused", "a review run's awaiting_review is the same bytes as before")
	assert.Contains(t, aw, "phase_checked")
}
