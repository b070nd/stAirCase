package orchestrator_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/plan"
	"github.com/b070nd/stAirCase/src/internal/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// certified runs agent with a signing key in the workspace and returns the
// run, its commit and the certificate the run left in the workspace.
func certified(t *testing.T, agent orchestrator.AgentFunc, opts orchestrator.RunOptions, op *operator, base ...map[string]runtest.File) (runtest.Result, string, certificate.Statement) {
	t.Helper()
	o := runtest.Options{Agent: agent, Run: opts, NoTopology: opts.Plan != nil && opts.Plan.Harness != "",
		Setup: func(s *persistence.Store, wsDir string, projectID int64) {
			require.NoError(t, crypto.GenerateSigningKey(wsDir))
			if op != nil { // a person answers what policy leaves open
				webhook(t, s, projectID, op)
			}
		}}
	if len(base) > 0 {
		o.Base = base[0]
	}
	r := runtest.Run(t, o)
	require.NoError(t, r.Err)
	require.NotEmpty(t, r.Run.GitCommitHash)
	b, err := os.ReadFile(filepath.Join(r.WsDir, "audit", "run-1.certificate.json"))
	require.NoError(t, err)
	var env certificate.Envelope
	require.NoError(t, json.Unmarshal(b, &env))
	pub, err := crypto.LoadSigningPublicKey(r.WsDir)
	require.NoError(t, err)
	s, err := certificate.Open(env, pub)
	require.NoError(t, err)
	return r, r.Run.GitCommitHash, s
}

// webhook makes op answer the project's proposals.
func webhook(t *testing.T, s *persistence.Store, projectID int64, op *operator) {
	srv := httptest.NewServer(op)
	t.Cleanup(srv.Close)
	require.NoError(t, s.UpdateProjectWebhook(projectID, srv.URL))
}

func git(t *testing.T, repo string, args ...string) string {
	out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return string(out)
}

func writeFile(path, content string) orchestrator.AgentFunc {
	return func(ctx context.Context, env *orchestrator.AgentEnv) error {
		return env.ProposeEdit(ctx, "claude-code", "r",
			domain.ProposedEdit{File: path, SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: content}).Apply(env.Worktree)
	}
}

// TestRun_certifies_its_commit: a run that commits leaves a signed change
// certificate about exactly that commit (in the workspace and as a git note),
// and the commit says which agent helped and where its audit chain stood.
func TestRun_certifies_its_commit(t *testing.T) {
	r, commit, s := certified(t, writeFile("health.txt", "ok\n"),
		orchestrator.RunOptions{Plan: &plan.Plan{Harness: "claude-code", Digest: "d1g35t"}}, nil)

	assert.Equal(t, commit, s.Commit())
	assert.Equal(t, 3, s.Predicate.CAL, s.Predicate.Notes)
	assert.Equal(t, []string{"Claude Code"}, s.Predicate.Agents)
	assert.Equal(t, "d1g35t", s.Predicate.PlanDigest)
	assert.Equal(t, 1, s.Predicate.Decisions["policy"])
	assert.NotEmpty(t, s.Predicate.ChainHead)

	msg := git(t, r.Repo, "log", "-1", "--format=%B", commit)
	assert.Contains(t, msg, "Assisted-by: Claude Code\n")
	assert.Contains(t, msg, "Staircase-Chain: sha256:"+s.Predicate.ChainHead)

	note := git(t, r.Repo, "notes", "--ref=staircase", "show", commit)
	var env certificate.Envelope
	require.NoError(t, json.Unmarshal([]byte(note), &env))
	assert.NotEmpty(t, env.Signatures)
	assert.Contains(t, r.Types(), "certificate_issued")
}

// TestRun_certificate_level_drops_with_unsandboxed_commands: an approved
// shell command ran outside any sandbox, so the change cannot reach CAL 3.
func TestRun_certificate_level_drops_with_unsandboxed_commands(t *testing.T) {
	agent := func(ctx context.Context, env *orchestrator.AgentEnv) error {
		if ap := env.ProposeShell(ctx, "coder", "r", ".", "true"); !ap.Approved {
			return nil
		}
		return writeFile("x.txt", "x\n")(ctx, env)
	}
	_, _, s := certified(t, agent, orchestrator.RunOptions{AllowShellExec: true}, &operator{approve: true})
	assert.Equal(t, 2, s.Predicate.CAL)
	assert.True(t, strings.Contains(strings.Join(s.Predicate.Notes, " "), "sandbox"), s.Predicate.Notes)
}

// TestRun_certificate_level_with_sandboxed_commands: an approved command that
// ran in the sandbox keeps CAL 3, and the file it wrote reaches the branch
// once decided; the same command without a sandbox drops the run to CAL 2.
func TestRun_certificate_level_with_sandboxed_commands(t *testing.T) {
	for _, sandboxed := range []bool{true, false} {
		agent := func(ctx context.Context, env *orchestrator.AgentEnv) error {
			if ap := env.ProposeShell(ctx, "coder", "r", ".", "make gen"); !ap.Approved {
				return nil
			}
			if err := os.WriteFile(filepath.Join(env.Worktree, "gen.txt"), []byte("gen\n"), 0o644); err != nil {
				return err
			}
			env.ShellRan(ctx, "coder", "make gen", sandboxed)
			return nil
		}
		r, _, s := certified(t, agent, orchestrator.RunOptions{AllowShellExec: true}, &operator{approve: true})
		got, err := r.OnBranch("gen.txt")
		require.NoError(t, err, got)
		assert.Equal(t, "gen\n", got)
		assert.Contains(t, r.Types(), "shell_ran")
		if sandboxed {
			assert.Equal(t, 3, s.Predicate.CAL, s.Predicate.Notes)
		} else {
			assert.Equal(t, 2, s.Predicate.CAL)
		}
	}
}

// TestRun_checks_the_commit_it_made: each check runs on a clean checkout of
// exactly the commit the run made, in the sandbox where the machine has one,
// and its result is in the certificate; a failing check is recorded, not
// hidden, and its checkout is removed.
func TestRun_checks_the_commit_it_made(t *testing.T) {
	r, _, s := certified(t, writeFile("health.txt", "ok\n"), orchestrator.RunOptions{
		Plan:   &plan.Plan{Harness: "claude-code"},
		Checks: []string{"grep -q ok health.txt && test -z \"$(git status --porcelain)\"", "echo broken; exit 3"}}, nil)

	c := s.Predicate.Checks
	require.Len(t, c, 2)
	assert.Equal(t, "grep -q ok health.txt && test -z \"$(git status --porcelain)\"", c[0].Command)
	assert.Equal(t, 0, c[0].ExitCode, "the check sees the committed tree")
	assert.Equal(t, 3, c[1].ExitCode)
	assert.Equal(t, sha256Hex("broken\n"), c[1].OutputSHA256)
	d := t.TempDir()
	_, _, cleanup, noSandbox := sandbox.Command(context.Background(), d, d, "true", sandbox.Required)
	if noSandbox == nil {
		cleanup()
	}
	assert.Equal(t, noSandbox == nil, c[0].Sandboxed, "sandboxed wherever this machine has a sandbox")
	assert.Equal(t, 3, s.Predicate.CAL, "checks are evidence, not decisions")
	assert.Contains(t, r.Types(), "check_ran")
	assert.NotContains(t, git(t, r.Repo, "worktree", "list"), "staircase-check")
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// TestRun_certificate_shows_review_attention: the certificate records how
// people decided: how many decisions, their median time, and how many large
// changes were approved within seconds (a rubber stamp becomes visible).
func TestRun_certificate_shows_review_attention(t *testing.T) {
	big := strings.Repeat("line\n", 30)
	agent := func(ctx context.Context, env *orchestrator.AgentEnv) error {
		if err := writeFile("big.txt", big)(ctx, env); err != nil {
			return err
		}
		return writeFile("small.txt", "one\n")(ctx, env)
	}
	op := &operator{approve: true}
	r := runtest.Run(t, runtest.Options{Agent: orchestrator.AgentFunc(agent),
		Setup: func(st *persistence.Store, wsDir string, projectID int64) {
			require.NoError(t, crypto.GenerateSigningKey(wsDir))
			require.NoError(t, os.WriteFile(filepath.Join(wsDir, "policy.json"), []byte(`{"rules":[]}`), 0o600)) // people decide
			webhook(t, st, projectID, op)
		}})
	require.NoError(t, r.Err)
	b, err := os.ReadFile(filepath.Join(r.WsDir, "audit", "run-1.certificate.json"))
	require.NoError(t, err)
	var env certificate.Envelope
	require.NoError(t, json.Unmarshal(b, &env))
	pub, err := crypto.LoadSigningPublicKey(r.WsDir)
	require.NoError(t, err)
	s, err := certificate.Open(env, pub)
	require.NoError(t, err)
	a := s.Predicate.Attention
	require.NotNil(t, a)
	assert.Equal(t, 2, a.HumanDecisions)
	assert.Equal(t, 1, a.QuickApprovals, "30 lines approved at once by the test operator")
	assert.GreaterOrEqual(t, a.MedianSeconds, 0.0)
}
