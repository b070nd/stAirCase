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

// TestDecide_a_project_can_add_sensitive_paths_and_narrow_the_scope (the project profile): a contract file the project names always
// goes to a person whatever a rule approves, in a run you watch and in an away run, where it is parked; a run's own scope sends what
// is outside it to a person like drift; and neither can make a built-in sensitive path easier to approve.
func TestDecide_a_project_can_add_sensitive_paths_and_narrow_the_scope(t *testing.T) {
	approveEdits := `{"rules":[{"action_types":["file_edit"],"effect":"approve"}]}`
	edit := func(file string) domain.YieldRequest {
		return domain.YieldRequest{AgentName: "coder", ActionType: domain.ActionFileEdit,
			ProposedEdits: []domain.ProposedEdit{{File: file, SearchBlock: MarkerNewFile, ReplaceBlock: "x\n"}}}
	}
	terms := Terms{SensitivePaths: []string{"contracts/**"}, Scope: []string{"src/**", "contracts/**"}}
	for _, away := range []bool{false, true} {
		terms.Away = away
		for file, want := range map[string]string{ // who decides each file: the rule, or a person (parked while away)
			"src/a.php":          "policy",
			"contracts/doc.json": map[bool]string{false: "operator", true: "parked"}[away],
			"docs/readme.md":     map[bool]string{false: "operator", true: "parked"}[away], // outside the run's scope
			".github/ci.yml":     map[bool]string{false: "operator", true: "parked"}[away], // still sensitive: the profile only adds
		} {
			d, _ := testDeciders(t, terms, approveEdits)
			d.drift = policy.NewSupervisor([]string{"**"}, plan.Limits{MaxScopeViolations: 9}) // the stories allow everything
			req := edit(file)
			if rl := d.decide(context.Background(), &req); rl.source != want {
				t.Errorf("away=%v %s: decided by %q, want %q (%s)", away, file, rl.source, want, rl.resp.Feedback)
			}
		}
	}
	if bar := terms.PersonBar([]string{"contracts/doc.json"}); !strings.Contains(bar, "a sensitive path of this project") {
		t.Errorf("the bar for a project's own sensitive path: %q", bar)
	}
	if bar := (Terms{}).PersonBar([]string{"contracts/doc.json"}); bar != "" {
		t.Errorf("without the profile a contract file is an ordinary path: %q", bar)
	}
}

func TestNewTerms_normalizes_the_scope_and_the_project_sensitive_paths(t *testing.T) {
	got, err := NewTerms(Terms{Scope: []string{"src//a/**", "src/a/**", "docs"}, SensitivePaths: []string{"./contracts/**"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Scope, ",") != "docs,src/a/**" || strings.Join(got.SensitivePaths, ",") != "contracts/**" {
		t.Errorf("scope %v, sensitive %v", got.Scope, got.SensitivePaths)
	}
	for _, bad := range []Terms{{Scope: []string{"/abs"}}, {Scope: []string{"../up"}}, {SensitivePaths: []string{"."}}, {SensitivePaths: []string{"a/[b"}}} {
		if _, err := NewTerms(bad, ""); err == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
}

// TestNewTerms_the_read_boundary: a project that keeps paths out of the agent's reach cannot be run by an agent that cannot be stopped from
// reading them (Codex, Gemini CLI, OpenCode: their sandboxes restrict writes, not reads); the built-in runtime and Claude Code can.
func TestNewTerms_the_read_boundary(t *testing.T) {
	terms := Terms{Unreadable: []string{"./auth.json", "**/.env", "auth.json"}}
	for harness, name := range map[string]string{"codex": "Codex", "gemini": "Gemini CLI", "opencode": "OpenCode"} {
		_, err := NewTerms(terms, harness)
		if err == nil {
			t.Fatalf("%s was accepted on a project that keeps files out of its reach", harness)
		}
		for _, want := range []string{name + " cannot be stopped from reading", "auth.json", "Claude Code or the built-in runtime"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: %q does not say %q", harness, err, want)
			}
		}
		if _, err := NewTerms(Terms{}, harness); err != nil {
			t.Errorf("%s with nothing out of reach: %v", harness, err)
		}
	}
	for _, harness := range []string{"", "claude-code"} {
		got, err := NewTerms(terms, harness)
		if err != nil {
			t.Fatalf("%q: %v", harness, err)
		}
		if strings.Join(got.Unreadable, ",") != "**/.env,auth.json" {
			t.Errorf("%q: the paths are cleaned and sorted: %v", harness, got.Unreadable)
		}
	}
	if _, err := NewTerms(Terms{Unreadable: []string{"/etc/passwd"}}, ""); err == nil {
		t.Error("a path outside the repository was accepted")
	}
}
