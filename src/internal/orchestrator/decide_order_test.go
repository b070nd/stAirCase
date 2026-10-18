package orchestrator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/monitor"
	"github.com/b070nd/stAirCase/src/internal/plan"
	"github.com/b070nd/stAirCase/src/internal/policy"
)

// TestDeciders_rule_order: who decides a proposal, in the order the deciders ask (decide.go): the orchestrator refuses what cannot
// be applied as shown; drift supervision, the guards and a change to a check's definition (a protected path) send a change to a
// person even when a rule would approve it, and so does a policy limit; then the policy's rules; then the agreed task, except on a sensitive path; while away, what would go to a person is
// parked, and a declared command or tool is the owner's decision made in advance; a tool call is never a policy rule's. Each case is
// one proposal put to deciders built from its terms, with a person who would approve anything.
func TestDeciders_rule_order(t *testing.T) {
	agreed := "dev@example.com"
	approveEdits := `{"rules":[{"action_types":["file_edit"],"effect":"approve"}]}`
	edit := func(file, content string) domain.YieldRequest {
		return domain.YieldRequest{AgentName: "coder", ActionType: domain.ActionFileEdit,
			ProposedEdits: []domain.ProposedEdit{{File: file, SearchBlock: MarkerNewFile, ReplaceBlock: content}}}
	}
	shell := func(cmd string) domain.YieldRequest {
		return domain.YieldRequest{AgentName: "coder", ActionType: domain.ActionShellExec,
			ProposedEdits: []domain.ProposedEdit{{File: ".", ReplaceBlock: cmd}}}
	}
	for name, tc := range map[string]struct {
		terms        Terms
		policy       string
		autoApproved int // policy approvals so far
		req          domain.YieldRequest
		want         string // the decision's source
		asked        bool   // a person was asked
		approved     bool
	}{
		"a person decides what nothing else does": {req: edit("src/b.txt", "b\n"), want: "operator", asked: true, approved: true},
		"a policy rule": {policy: approveEdits, req: edit("src/b.txt", "b\n"), want: "policy", approved: true},
		"a refusal comes before anyone": {policy: approveEdits, want: "orchestrator",
			req: domain.YieldRequest{AgentName: "coder", ActionType: domain.ActionFileEdit,
				ProposedEdits: []domain.ProposedEdit{{File: "src/a.txt", SearchBlock: "absent\n", ReplaceBlock: "x\n"}}}},
		"drift before the policy":        {policy: approveEdits, req: edit("docs/x.md", "x\n"), want: "operator", asked: true, approved: true},
		"a guard before the policy":      {policy: approveEdits, req: edit("src/go.mod", "module x\n"), want: "operator", asked: true, approved: true},
		"a policy limit before its rule": {policy: `{"rules":[{"action_types":["file_edit"],"effect":"approve"}],"limits":{"max_auto_approved":1}}`, autoApproved: 1, req: edit("src/b.txt", "b\n"), want: "operator", asked: true, approved: true},
		"the policy before the task": {terms: Terms{ApproveInScope: true, Agreed: agreed}, policy: approveEdits,
			req: edit("src/b.txt", "b\n"), want: "policy", approved: true},
		"the agreed task": {terms: Terms{ApproveInScope: true, Agreed: agreed}, req: edit("src/b.txt", "b\n"), want: "task", approved: true},
		"no task without an agreement": {terms: Terms{ApproveInScope: true}, req: edit("src/b.txt", "b\n"),
			want: "operator", asked: true, approved: true},
		"a sensitive path inside the task": {terms: Terms{ApproveInScope: true, Agreed: agreed}, req: edit("src/run.sh", "echo\n"),
			want: "operator", asked: true, approved: true},
		"away, what needs a person is parked": {terms: Terms{Away: true, Agreed: agreed}, req: edit("docs/x.md", "x\n"), want: "parked"},
		"away, a declared command":            {terms: Terms{Away: true, Agreed: agreed, AwayCommands: []string{"make"}}, req: shell("make"), want: "declared", approved: true},
		"away, any other command is parked":   {terms: Terms{Away: true, Agreed: agreed, AwayCommands: []string{"make"}}, req: shell("make install"), want: "parked"},
		"away, a declared tool": {terms: Terms{Away: true, Agreed: agreed, AwayTools: []string{"mcp__a__b"}}, want: "declared", approved: true,
			req: domain.YieldRequest{AgentName: "coder", ActionType: domain.ActionToolCall,
				ProposedEdits: []domain.ProposedEdit{{File: "mcp__a__b", SearchBlock: MarkerToolCall, ReplaceBlock: "{}"}}}},
		"a check's definition before the policy": {terms: Terms{ProtectedPaths: []string{"src/check.t"}}, policy: approveEdits,
			req: edit("src/check.t", "exit 0\n"), want: "operator", asked: true, approved: true},
		"a check's definition inside the task": {terms: Terms{ApproveInScope: true, Agreed: agreed, ProtectedPaths: []string{"src/**/*.t"}},
			req: edit("src/x/check.t", "exit 0\n"), want: "operator", asked: true, approved: true},
		"deleting a check's definition": {terms: Terms{ProtectedPaths: []string{"src/a.txt"}}, policy: approveEdits, want: "operator", asked: true, approved: true,
			req: domain.YieldRequest{AgentName: "coder", ActionType: domain.ActionFileEdit,
				ProposedEdits: []domain.ProposedEdit{{File: "src/a.txt", SearchBlock: MarkerDeleteFile}}}},
		"away, a check's definition is parked whatever the policy says": {terms: Terms{Away: true, Agreed: agreed, ProtectedPaths: []string{"src/check.t"}},
			policy: approveEdits, req: edit("src/check.t", "exit 0\n"), want: "parked"},
		"a tool call is never a rule's": {policy: `{"rules":[{"action_types":["tool_call"],"effect":"approve"}]}`, want: "operator", asked: true, approved: true,
			req: domain.YieldRequest{AgentName: "coder", ActionType: domain.ActionToolCall,
				ProposedEdits: []domain.ProposedEdit{{File: "mcp__a__b", SearchBlock: MarkerToolCall, ReplaceBlock: "{}"}}}},
	} {
		t.Run(name, func(t *testing.T) {
			d, asked := testDeciders(t, tc.terms, tc.policy)
			d.autoApproved, d.total = tc.autoApproved, tc.autoApproved
			req := tc.req
			rl := d.decide(context.Background(), &req)
			if rl.source != tc.want || (len(*asked) > 0) != tc.asked || rl.resp.Approved != tc.approved {
				t.Errorf("decided by %q (approved %v), a person asked %d time(s); want %q (approved %v), asked %v: %s",
					rl.source, rl.resp.Approved, len(*asked), tc.want, tc.approved, tc.asked, rl.resp.Feedback)
			}
		})
	}
}

// testDeciders are deciders under terms and a policy, for a repository whose base holds src/a.txt and whose stories' scope is
// src/**; their person approves whatever they are asked, and says what they were asked.
func testDeciders(t *testing.T, terms Terms, policyJSON string) (*deciders, *[]domain.YieldRequest) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "base")
	repo, err := OpenGitRepo(dir)
	if err != nil {
		t.Fatal(err)
	}
	base, err := repo.HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	appr, err := newApprovals(repo, base)
	if err != nil {
		t.Fatal(err)
	}
	engine := &policy.Engine{}
	if policyJSON != "" {
		snap, err := policy.SnapshotFromBytes([]byte(policyJSON), false)
		if err != nil {
			t.Fatal(err)
		}
		engine = snap.Engine
	}
	var asked []domain.YieldRequest
	tracker := monitor.NewTracker(1, 1, "p", "main")
	d := (&deciders{approvals: appr, drift: policy.NewSupervisor([]string{"src/**"}, plan.Limits{MaxScopeViolations: 9}), policy: engine,
		redact: func(s string) string { return s }, display: monitor.NewDisplay(tracker, 0), tracker: tracker,
		audit: func(string, any) error { return nil },
		askHuman: func(req domain.YieldRequest, _ string) domain.YieldResponse {
			asked = append(asked, req)
			return domain.Decide(true, "approved by the person")
		}}).under(terms)
	return d, &asked
}
