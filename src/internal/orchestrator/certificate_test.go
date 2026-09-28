package orchestrator_test

import (
	"context"
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
