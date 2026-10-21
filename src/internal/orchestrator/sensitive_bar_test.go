package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/plan"
	"github.com/b070nd/stAirCase/src/internal/policy"
)

// TestDecide_the_person_bar_has_one_wording: the reason a decider gives, the line `staircase policy test` prints and the reason
// delivery by policy refuses with are all Terms.PersonBar, so the sentences cannot drift apart.
func TestDecide_the_person_bar_has_one_wording(t *testing.T) {
	for name, tc := range map[string]struct {
		terms Terms
		path  string
	}{
		"a check's definition": {Terms{ProtectedPaths: []string{"src/check.t"}}, "src/check.t"},
		"a sensitive path":     {Terms{}, ".github/workflows/ci.yml"},
	} {
		t.Run(name, func(t *testing.T) {
			bar := tc.terms.PersonBar([]string{tc.path})
			if bar == "" {
				t.Fatalf("no bar for %s", tc.path)
			}
			d, _ := testDeciders(t, tc.terms, `{"rules":[{"action_types":["file_edit"],"effect":"approve"}]}`)
			d.drift = policy.NewSupervisor([]string{"**"}, plan.Limits{MaxScopeViolations: 9})
			req := domain.YieldRequest{AgentName: "coder", ActionType: domain.ActionFileEdit,
				ProposedEdits: []domain.ProposedEdit{{File: tc.path, SearchBlock: MarkerNewFile, ReplaceBlock: "x\n"}}}
			d.decide(context.Background(), &req)
			if req.Review != bar+PersonDecides {
				t.Errorf("the decider says %q, want %q", req.Review, bar+PersonDecides)
			}
			c := deliveryCase{terms: tc.terms, files: map[string]string{tc.path: "digest"}}
			c.terms.DeliverPaths = []string{"**"}
			if why := c.insideBars(); !strings.HasPrefix(why, bar) {
				t.Errorf("delivery by policy says %q, want it to start with %q", why, bar)
			}
		})
	}
}

// TestDecide_policy_rule_cannot_approve_a_sensitive_path: a file edit that touches a sensitive path (CI, build, dependency, secret
// and shell files, CODEOWNERS at any depth, Gemini's repository settings) goes to a person whatever a rule approves, in a run you
// watch and in an away run, where it is parked. The reason names the path.
func TestDecide_policy_rule_cannot_approve_a_sensitive_path(t *testing.T) {
	approveEdits := `{"rules":[{"action_types":["file_edit"],"effect":"approve"}]}`
	for _, path := range []string{
		".github/workflows/ci.yml", ".gitlab/ci.yml", ".circleci/config.yml", ".husky/pre-commit",
		".gitlab-ci.yml", "src/.gitlab-ci.yml", ".travis.yml", "azure-pipelines.yml", "Jenkinsfile",
		"Makefile", "src/GNUmakefile", "justfile", "Dockerfile", "go.mod", "go.sum", "package.json", "yarn.lock",
		".npmrc", ".gitattributes", ".gitmodules", ".pre-commit-config.yaml", ".env.local", "scripts/deploy.sh",
		"CODEOWNERS", "docs/CODEOWNERS", "src/a/b/CODEOWNERS", ".github/CODEOWNERS", ".gemini/settings.json",
	} {
		for _, away := range []bool{false, true} {
			name := path + " live"
			want := "operator"
			if away {
				name, want = path+" away", "parked"
			}
			t.Run(name, func(t *testing.T) {
				d, asked := testDeciders(t, Terms{Away: away}, approveEdits)
				d.drift = policy.NewSupervisor([]string{"**"}, plan.Limits{MaxScopeViolations: 9}) // the path is in scope: only the bar can stop it
				req := domain.YieldRequest{AgentName: "coder", ActionType: domain.ActionFileEdit,
					ProposedEdits: []domain.ProposedEdit{{File: path, SearchBlock: MarkerNewFile, ReplaceBlock: "x=1\n"}}}
				rl := d.decide(context.Background(), &req)
				if rl.source != want || (len(*asked) > 0) == away {
					t.Errorf("decided by %q, a person asked %d time(s); want %q: %s", rl.source, len(*asked), want, rl.resp.Feedback)
				}
				if !strings.Contains(req.Review, path) || !strings.Contains(req.Review, "whatever the policy says") {
					t.Errorf("the reason %q does not name %s and say a person decides whatever the policy says", req.Review, path)
				}
			})
		}
	}

	t.Run("a path that is not sensitive is still the policy's", func(t *testing.T) {
		d, asked := testDeciders(t, Terms{}, approveEdits)
		req := domain.YieldRequest{AgentName: "coder", ActionType: domain.ActionFileEdit,
			ProposedEdits: []domain.ProposedEdit{{File: "src/notes.md", SearchBlock: MarkerNewFile, ReplaceBlock: "x\n"}}}
		if rl := d.decide(context.Background(), &req); rl.source != "policy" || !rl.resp.Approved || len(*asked) > 0 {
			t.Errorf("decided by %q (approved %v), asked %d time(s); want the policy to approve", rl.source, rl.resp.Approved, len(*asked))
		}
	})
}

// TestDecide_sensitive_bar_applies_before_limits_and_policy: the bar sits before the limits and the policy, so a sensitive path is
// sent to a person for its own reason (not a limit's), and the rule that would have approved it is not counted as having approved.
func TestDecide_sensitive_bar_applies_before_limits_and_policy(t *testing.T) {
	d, _ := testDeciders(t, Terms{}, `{"rules":[{"action_types":["file_edit"],"effect":"approve"}],"limits":{"max_auto_approved":1}}`)
	d.drift = policy.NewSupervisor([]string{"**"}, plan.Limits{MaxScopeViolations: 9})
	d.autoApproved, d.total = 1, 1 // the limit is already hit
	req := domain.YieldRequest{AgentName: "coder", ActionType: domain.ActionFileEdit,
		ProposedEdits: []domain.ProposedEdit{{File: "Makefile", SearchBlock: MarkerNewFile, ReplaceBlock: "all:\n"}}}
	rl := d.decide(context.Background(), &req)
	if rl.source != "operator" || !strings.Contains(req.Review, "Makefile") {
		t.Errorf("decided by %q with the reason %q; want a person, for the sensitive path", rl.source, req.Review)
	}
	if d.autoApproved != 1 {
		t.Errorf("the policy counted %d approvals; want the 1 it had", d.autoApproved)
	}
}
