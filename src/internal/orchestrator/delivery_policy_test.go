package orchestrator

import (
	"encoding/base64"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/sshsig"
	"github.com/b070nd/stAirCase/src/internal/testdeps"
)

// A delivery by policy is judged on seven conditions (ADR 0012, section 2), each read from the verified history of the run, the
// terms its chain binds and the tree now approved. These tests build the history on a real, verified chain and break one condition
// at a time: each, alone, refuses delivery and names itself; with none broken, nothing refuses.

// deliveryRun describes the chain of a run that was to deliver by policy: by default one that met every condition.
type deliveryRun struct {
	unsigned   bool                // the policy snapshot says it was not signed
	noInit     bool                // the initiator did not sign
	forgedInit bool                // the chain names a signature of the initiator that is no signature
	decisions  []deliveryDecision  // the proposals decided; default: one file approved by a rule
	checkTree  string              // the tree the checks ran on
	failed     bool                // the checks failed
	parkedLive int                 // proposals the run counts as parked
	mutate     func(*deliveryCase) // changes the case after it is built
}

type deliveryDecision struct {
	source, action string
	approved       bool
	paths          []string
}

// signedRequest is the base64 of principal's real SSH signature of the request of run runID from base under plan, as a run signs it
// when it starts (segment.go), by a throwaway key.
func signedRequest(t *testing.T, runID int64, base, plan, principal string) string {
	t.Helper()
	testdeps.Need(t, "ssh-keygen")
	key := filepath.Join(t.TempDir(), "id")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", principal, "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v: %s", err, out)
	}
	sig, err := sshsig.Sign(key, certificate.InitiatorNamespace,
		certificate.InitiatorText(certificate.Predicate{Run: runID, BaseCommit: base, PlanDigest: plan}, principal))
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(sig)
}

// goodTerms are the terms of a policy delivery that met every condition the terms decide.
func goodTerms() Terms {
	return Terms{Away: true, Agreed: "dev@example.com", Checks: []string{"true"}, Deliver: DeliverPolicy, DeliverPaths: []string{"docs/**"},
		SignKey: "k", SignAs: "dev@example.com", ProtectedPaths: []string{"**/*_test.go"}}
}

// build writes the run's chain and returns the case the seven conditions are judged on.
func (d deliveryRun) build(t *testing.T) deliveryCase {
	t.Helper()
	db, err := persistence.InitDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := persistence.NewStore(db)
	v, _ := s.CreateVendor("V")
	p, _ := s.CreateProject(v.ID, "P", t.TempDir())
	_, _ = s.CreateSwarmTopology(p.ID, "sup", "memory", "langgraph")
	c, _ := s.CreateCase(p.ID)
	run, err := s.CreateRun(c.ID, 1, "staircase/run-1")
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{store: s}
	must := func(event string, fields any) {
		t.Helper()
		if err := r.audit(run.ID, event, fields); err != nil {
			t.Fatal(err)
		}
	}
	must("run_bound", history.Bound{BaseSHA: "base", Branch: "refs/heads/staircase/run-1", TermsSHA256: "T"})
	must("policy_snapshot", history.PolicySnapshot{Digest: "P", Signed: !d.unsigned})
	if !d.noInit {
		sig := "c2ln" // not a signature
		if !d.forgedInit {
			sig = signedRequest(t, run.ID, "base", "", "dev@example.com")
		}
		must("initiator_signed", history.InitiatorSigned{Principal: "dev@example.com", Signature: sig})
	}
	decisions := d.decisions
	if decisions == nil {
		decisions = []deliveryDecision{{"policy", domain.ActionFileEdit, true, []string{"docs/a.md"}}}
	}
	files := map[string]string{}
	for i, dc := range decisions {
		must("yield_request", domain.YieldRequest{AgentName: "coder", ActionType: dc.action})
		dd := history.Decided{Seq: i + 1, Source: dc.source, ActionType: dc.action, Approved: dc.approved, Paths: dc.paths, RequestSHA256: "r"}
		if dc.approved && dc.action == domain.ActionFileEdit {
			dd.Files = map[string]string{}
			for _, path := range dc.paths {
				dd.Files[path], files[path] = "d-"+path, "d-"+path
			}
		}
		if dc.source == "parked" {
			dd.ParkedWhy = "it needs a person's decision"
		}
		must("yield_decided", dd)
	}
	tree := d.checkTree
	if tree == "" {
		tree = "t1"
	}
	checks := []history.Check{{Command: "true", ExitCode: 0}}
	failed := ""
	if d.failed {
		failed = "true: exit 1"
	}
	must("phase_checked", history.PhaseChecked{Tree: tree, Checks: checks, Passed: !d.failed, Failed: failed})
	h, err := history.Read(s, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	dc := deliveryCase{terms: goodTerms(), hist: h, tree: "t1", files: files, parked: d.parkedLive, atBase: true}
	if d.mutate != nil {
		d.mutate(&dc)
	}
	return dc
}

// TestDeliveryCase_met_conditions_do_not_refuse: a run that met all seven conditions is not refused, and a refusal names none.
func TestDeliveryCase_met_conditions_do_not_refuse(t *testing.T) {
	if why := (deliveryRun{}).build(t).refusal(); why != "" {
		t.Fatalf("refused a run that met every condition: %s", why)
	}
}

// TestDeliveryCase_each_condition_failing_alone_refuses_and_names_itself: seven conditions, each broken by itself; ADR 0012 section 2.
func TestDeliveryCase_each_condition_failing_alone_refuses_and_names_itself(t *testing.T) {
	file := func(source string, approved bool, path string) []deliveryDecision {
		return []deliveryDecision{{source, domain.ActionFileEdit, approved, []string{path}}}
	}
	for name, tc := range map[string]struct {
		run  deliveryRun
		n    int
		cite string
	}{
		// 1: the bound terms say policy, and the run's policy snapshot was signed
		"1: the terms say review":          {deliveryRun{mutate: func(c *deliveryCase) { c.terms.Deliver = "" }}, 1, "review"},
		"1: the policy was not signed":     {deliveryRun{unsigned: true}, 1, "signed"},
		"1: no policy snapshot":            {deliveryRun{mutate: func(c *deliveryCase) { c.hist.Policy = nil }}, 1, "signed"},
		"1: terms the chain does not bind": {deliveryRun{mutate: func(c *deliveryCase) { c.hist.Bound.TermsSHA256 = "" }}, 1, "bound"},

		// 2: every file approval has the source policy; no operator, task, evidence or reviewer-model approval
		"2: a change approved on evidence":                        {deliveryRun{decisions: file("evidence", true, "docs/a.md")}, 2, "evidence"},
		"2: a change approved as the task":                        {deliveryRun{decisions: file("task", true, "docs/a.md")}, 2, "task"},
		"2: a change a person approved":                           {deliveryRun{decisions: file("operator", true, "docs/a.md")}, 2, "operator"},
		"2: a change a reviewer model approved":                   {deliveryRun{decisions: file("validator:m", true, "docs/a.md")}, 2, "validator:m"},
		"2: a file edit under a declared command":                 {deliveryRun{decisions: file("declared", true, "docs/a.md")}, 2, "declared"},
		"2: an approval of a source nobody knows":                 {deliveryRun{decisions: file("orchestrator", true, "docs/a.md")}, 2, "orchestrator"},
		"2: something approved on the task (the run's own count)": {deliveryRun{mutate: func(c *deliveryCase) { c.taskApproved = true }}, 2, "task"},
		"2: something a reviewer model left unreviewed":           {deliveryRun{mutate: func(c *deliveryCase) { c.unreviewed = true }}, 2, "reviewer"},
		"2: nothing a rule approved":                              {deliveryRun{decisions: file("policy", false, "docs/a.md"), mutate: func(c *deliveryCase) { c.files = nil }}, 2, "nothing"},

		// 3: nothing was parked
		"3: a proposal parked": {deliveryRun{decisions: []deliveryDecision{
			{"policy", domain.ActionFileEdit, true, []string{"docs/a.md"}}, {"parked", domain.ActionFileEdit, false, []string{"docs/b.md"}}}}, 3, "parked"},
		"3: a parked command": {deliveryRun{decisions: []deliveryDecision{
			{"policy", domain.ActionFileEdit, true, []string{"docs/a.md"}}, {"parked", domain.ActionShellExec, false, nil}}}, 3, "parked"},
		"3: the run's own count says parked": {deliveryRun{parkedLive: 1}, 3, "parked"},

		// 4: every approved path inside the delivery scope, and neither sensitive nor protected
		"4: a path outside the delivery scope": {deliveryRun{decisions: file("policy", true, "src/a.go"), mutate: func(c *deliveryCase) {
			c.terms.ProtectedPaths = []string{"**/*_test.go"}
		}}, 4, "src/a.go"},
		"4: a sensitive path inside the scope": {deliveryRun{decisions: file("policy", true, "docs/CODEOWNERS"), mutate: func(c *deliveryCase) {
			c.terms.DeliverPaths = []string{"docs/**"}
		}}, 4, "docs/CODEOWNERS"},
		"4: a CI workflow, scope everything": {deliveryRun{decisions: file("policy", true, ".github/workflows/ci.yml"), mutate: func(c *deliveryCase) {
			c.terms.DeliverPaths = []string{"**"}
		}}, 4, ".github/workflows/ci.yml"},
		"4: a protected path inside the scope": {deliveryRun{decisions: file("policy", true, "docs/x_test.go"), mutate: func(c *deliveryCase) {
			c.terms.DeliverPaths = []string{"docs/**"}
		}}, 4, "docs/x_test.go"},
		"4: a protected path, any case": {deliveryRun{decisions: file("policy", true, "docs/X_TEST.GO"), mutate: func(c *deliveryCase) {
			c.terms.DeliverPaths = []string{"docs/**"}
		}}, 4, "docs/X_TEST.GO"},
		"4: an approved path the tree does not show": {deliveryRun{mutate: func(c *deliveryCase) {
			c.files = map[string]string{"docs/a.md": "d-docs/a.md", "src/sneaky.go": "x"}
		}}, 4, "src/sneaky.go"},
		"4: an approval that records no path": {deliveryRun{decisions: file("policy", true, "docs/a.md"), mutate: func(c *deliveryCase) {
			for i := range c.hist.Decisions {
				c.hist.Decisions[i].Paths = nil
			}
		}}, 4, "path"},
		"4: no delivery scope": {deliveryRun{mutate: func(c *deliveryCase) { c.terms.DeliverPaths = nil }}, 4, "scope"},

		// 5: the checks passed on exactly the approved tree, and the run branch is still at the base
		"5: the checks ran on another tree":         {deliveryRun{checkTree: "t9"}, 5, "tree"},
		"5: the checks failed":                      {deliveryRun{failed: true}, 5, "pass"},
		"5: other checks than the terms name":       {deliveryRun{mutate: func(c *deliveryCase) { c.terms.Checks = []string{"true", "go vet ./..."} }}, 5, "checks"},
		"5: nothing to commit":                      {deliveryRun{mutate: func(c *deliveryCase) { c.unchanged = true }}, 5, "nothing to commit"},
		"5: the run branch moved":                   {deliveryRun{mutate: func(c *deliveryCase) { c.atBase = false }}, 5, "branch"},
		"5: the approved bytes are not the chain's": {deliveryRun{mutate: func(c *deliveryCase) { c.files["docs/a.md"] = "other" }}, 5, "bytes"},

		// 6: the protected-paths declaration is complete
		"6: no protected path declared": {deliveryRun{mutate: func(c *deliveryCase) { c.terms.ProtectedPaths = nil }}, 6, "protected"},

		// 7: the initiator signed
		"7: the initiator did not sign": {deliveryRun{noInit: true}, 7, "initiator"},
		// ... and a name on the chain is not enough: the signature must be a real one, of this run's request
		"7: the initiator's signature is no signature": {deliveryRun{forgedInit: true}, 7, "signature"},
		"7: the signature is of another base commit": {deliveryRun{mutate: func(c *deliveryCase) { c.hist.Bound.BaseSHA = "other" }}, 7, "signature"},
		"7: the signature is of another principal":   {deliveryRun{mutate: func(c *deliveryCase) { c.hist.Initiator.Principal = "eve@example.com" }}, 7, "signature"},
	} {
		t.Run(name, func(t *testing.T) {
			why := tc.run.build(t).refusal()
			want := "condition " + string(rune('0'+tc.n))
			if !strings.HasPrefix(why, want) {
				t.Fatalf("refusal %q does not name %s", why, want)
			}
			if !strings.Contains(strings.ToLower(why), strings.ToLower(tc.cite)) {
				t.Errorf("refusal %q does not mention %q", why, tc.cite)
			}
		})
	}
}

// TestDeliveryCase_a_declared_command_is_not_a_file_approval: the source declared is allowed for commands and tools only, which
// carry no files (ADR 0012, condition 2); a rule may approve a command too.
func TestDeliveryCase_a_declared_command_is_not_a_file_approval(t *testing.T) {
	run := deliveryRun{decisions: []deliveryDecision{
		{"policy", domain.ActionFileEdit, true, []string{"docs/a.md"}},
		{"declared", domain.ActionShellExec, true, nil},
		{"declared", domain.ActionToolCall, true, nil},
		{"policy", domain.ActionShellExec, true, nil},
		{"policy", domain.ActionFileEdit, false, []string{"docs/rejected.md"}},
	}}
	if why := run.build(t).refusal(); why != "" {
		t.Fatalf("refused: %s", why)
	}
}
