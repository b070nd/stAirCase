package orchestrator_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/staircase-core/src/internal/ipc"
	"github.com/b070nd/staircase-core/src/internal/orchestrator"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type baseFile struct {
	content string
	mode    os.FileMode
}

type approvalResult struct {
	Approved bool   `json:"approved"`
	Feedback string `json:"feedback"`
}

// scenarioResult is what an approval scenario leaves behind for its checks.
type scenarioResult struct {
	repo    string
	run     persistence.Run
	results []approvalResult
	events  []string
}

// scriptedAgent is an in-process agent driven by a scenario: it proposes edits
// and touches the worktree the way a runtime — or an approved shell command —
// can. It reports failures with t.Errorf because it runs off the test goroutine.
type scriptedAgent struct {
	t       *testing.T
	env     *orchestrator.AgentEnv
	results []approvalResult
}

// ask proposes edits given as {file, search, replace} and records the answer.
func (a *scriptedAgent) ask(ctx context.Context, edits ...[3]string) {
	pe := make([]ipc.ProposedEdit, len(edits))
	for i, e := range edits {
		pe[i] = ipc.ProposedEdit{File: e[0], SearchBlock: e[1], ReplaceBlock: e[2]}
	}
	r := a.env.Propose(ctx, ipc.IpcYieldRequest{AgentName: "coder", ActionType: "file_edit",
		ProposedEdits: pe, ReasoningTrace: "approval test", ConfidenceScore: 0.9})
	a.results = append(a.results, approvalResult{Approved: r.Approved, Feedback: r.Feedback})
}

// write writes like a runtime tool: temp file + rename, which leaves mode 0600.
func (a *scriptedAgent) write(path, data string) {
	full := filepath.Join(a.env.Worktree, path)
	assert.NoError(a.t, os.MkdirAll(filepath.Dir(full), 0o755))
	f, err := os.CreateTemp(filepath.Dir(full), ".write-")
	if !assert.NoError(a.t, err) {
		return
	}
	_, err = f.WriteString(data)
	assert.NoError(a.t, err)
	assert.NoError(a.t, f.Close())
	assert.NoError(a.t, os.Rename(f.Name(), full))
}

func (a *scriptedAgent) git(args ...string) {
	out, err := exec.Command("git", append([]string{"-C", a.env.Worktree}, args...)...).CombinedOutput()
	assert.NoError(a.t, err, "git %v: %s", args, out)
}

// runApprovalScenario commits baseFiles to a fresh repo and runs agent
// in-process with file_edit auto-approved by policy.
func runApprovalScenario(t *testing.T, baseFiles map[string]baseFile, agent func(context.Context, *scriptedAgent), prepare func(*testing.T, *sql.DB)) scenarioResult {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test — skipped in -short mode")
	}
	wsDir, err := os.MkdirTemp("", "strc-ap-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(wsDir) })
	prepareAgentWorkspace(t, wsDir)
	repo := initGitRepo(t)
	for name, f := range baseFiles {
		p := filepath.Join(repo, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(f.content), f.mode))
	}
	if len(baseFiles) > 0 {
		out, err := exec.Command("git", "-C", repo, "add", "-A").CombinedOutput()
		require.NoError(t, err, "%s", out)
		out, err = exec.Command("git", "-C", repo, "commit", "-q", "-m", "base").CombinedOutput()
		require.NoError(t, err, "%s", out)
	}
	db, err := persistence.InitDB(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	s := persistence.NewStore(db)
	if prepare != nil {
		prepare(t, db)
	}
	caseID, _ := scaffoldForRun(t, s, repo)
	sa := &scriptedAgent{t: t}
	opts := orchestrator.RunOptions{SkipGates: true, Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
		sa.env = env
		agent(ctx, sa)
		return nil
	})}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_ = orchestrator.NewRunner(s, wsDir).Run(ctx, caseID, opts)

	runs, err := s.ListRunsByCase(caseID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	r := scenarioResult{repo: repo, run: runs[0], results: sa.results}
	logs, err := s.ListEventLogs(r.run.ID)
	require.NoError(t, err)
	for _, l := range logs {
		r.events = append(r.events, l.EventType)
	}
	require.NoError(t, s.VerifyChain(r.run.ID))
	return r
}

func gitShow(t *testing.T, repo, rev string) (string, error) {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "show", rev).CombinedOutput()
	return string(out), err
}

// approvalScenario is an agent's moves against a base repo, and what must follow.
type approvalScenario struct {
	name    string
	base    map[string]baseFile
	prepare func(*testing.T, *sql.DB)
	agent   func(ctx context.Context, a *scriptedAgent)
	check   func(t *testing.T, r scenarioResult)
}

func onBranch(t *testing.T, r scenarioResult, file string) (string, error) {
	return gitShow(t, r.repo, fmt.Sprintf("staircase/run-%d:%s", r.run.ID, file))
}

func failedWith(event string) func(*testing.T, scenarioResult) {
	return func(t *testing.T, r scenarioResult) {
		assert.Equal(t, persistence.RunStatusFailed, r.run.Status)
		assert.Empty(t, r.run.GitCommitHash)
		assert.Contains(t, r.events, event)
	}
}

func approvalScenarios() []approvalScenario {
	return []approvalScenario{{
		name: "create_without_agent_hash",
		agent: func(ctx context.Context, a *scriptedAgent) {
			a.ask(ctx, [3]string{"new.txt", "(new file)", "hello\n"})
			a.write("new.txt", "hello\n")
		},
		check: func(t *testing.T, r scenarioResult) {
			require.Equal(t, persistence.RunStatusSuccess, r.run.Status)
			require.Len(t, r.results, 1)
			assert.True(t, r.results[0].Approved)
			got, err := onBranch(t, r, "new.txt")
			require.NoError(t, err)
			assert.Equal(t, "hello\n", got)
		},
	}, {
		name: "edits_chain_with_universal_newlines",
		base: map[string]baseFile{"f.txt": {"a\r\nb\r\n", 0o644}},
		agent: func(ctx context.Context, a *scriptedAgent) {
			a.ask(ctx, [3]string{"f.txt", "b\n", "B\n"})
			a.write("f.txt", "a\nB\n")
			a.ask(ctx, [3]string{"f.txt", "a\n", "A\n"})
			a.write("f.txt", "A\nB\n")
		},
		check: func(t *testing.T, r scenarioResult) {
			require.Equal(t, persistence.RunStatusSuccess, r.run.Status)
			got, err := onBranch(t, r, "f.txt")
			require.NoError(t, err)
			assert.Equal(t, "A\nB\n", got)
		},
	}, {
		name: "post_approval_tamper_fails",
		agent: func(ctx context.Context, a *scriptedAgent) {
			a.ask(ctx, [3]string{"x.txt", "(new file)", "approved\n"})
			a.write("x.txt", "something else\n")
		},
		check: failedWith("approval_content_mismatch"),
	}, {
		name: "unapproved_extra_file_fails",
		agent: func(ctx context.Context, a *scriptedAgent) {
			a.ask(ctx, [3]string{"a.txt", "(new file)", "a\n"})
			a.write("a.txt", "a\n")
			a.write("extra.txt", "never approved\n")
		},
		check: failedWith("unapproved_worktree_change"),
	}, {
		name: "direct_index_write_fails",
		agent: func(ctx context.Context, a *scriptedAgent) {
			a.ask(ctx, [3]string{"a.txt", "(new file)", "a\n"})
			a.write("a.txt", "a\n")
			a.write("sneaky.txt", "staged behind the orchestrator's back\n")
			a.git("add", "sneaky.txt")
		},
		check: failedWith("unapproved_worktree_change"),
	}, {
		// A commit made in the worktree hides its changes from a status check
		// (they are no longer "changes"), so the branch itself must be checked.
		name: "agent_commit_on_the_run_branch_fails",
		agent: func(ctx context.Context, a *scriptedAgent) {
			a.ask(ctx, [3]string{"a.txt", "(new file)", "a\n"})
			a.write("a.txt", "a\n")
			a.write("sneaky.txt", "committed behind the orchestrator's back\n")
			a.git("add", "-A")
			a.git("-c", "user.name=a", "-c", "user.email=a@a", "commit", "-qm", "sneaky")
		},
		check: func(t *testing.T, r scenarioResult) {
			if r.run.Status == persistence.RunStatusSuccess {
				_, err := onBranch(t, r, "sneaky.txt")
				assert.Error(t, err, "a successful run's branch carries a file nobody approved")
			}
			failedWith("run_branch_moved")(t, r)
		},
	}, {
		name: "unapproved_deletion_fails",
		base: map[string]baseFile{"old.txt": {"keep me\n", 0o644}},
		agent: func(_ context.Context, a *scriptedAgent) {
			assert.NoError(a.t, os.Remove(filepath.Join(a.env.Worktree, "old.txt")))
		},
		check: failedWith("unapproved_worktree_change"),
	}, {
		name: "approved_deletion_is_committed",
		base: map[string]baseFile{"old.txt": {"bye\n", 0o644}, "keep.txt": {"k\n", 0o644}},
		agent: func(ctx context.Context, a *scriptedAgent) {
			a.ask(ctx, [3]string{"old.txt", "(delete file)", ""})
			assert.NoError(a.t, os.Remove(filepath.Join(a.env.Worktree, "old.txt")))
		},
		check: func(t *testing.T, r scenarioResult) {
			require.Equal(t, persistence.RunStatusSuccess, r.run.Status, "%v", r.results)
			_, err := onBranch(t, r, "old.txt")
			assert.Error(t, err, "old.txt must be deleted on the run branch")
		},
	}, {
		name: "exec_bit_preserved",
		base: map[string]baseFile{"run.sh": {"echo a\n", 0o755}},
		agent: func(ctx context.Context, a *scriptedAgent) {
			a.ask(ctx, [3]string{"run.sh", "echo a", "echo b"})
			a.write("run.sh", "echo b\n")
		},
		check: func(t *testing.T, r scenarioResult) {
			require.Equal(t, persistence.RunStatusSuccess, r.run.Status)
			out, err := exec.Command("git", "-C", r.repo, "ls-tree", fmt.Sprintf("staircase/run-%d", r.run.ID), "run.sh").CombinedOutput()
			require.NoError(t, err)
			assert.True(t, strings.HasPrefix(string(out), "100755"), "mode must stay executable: %s", out)
		},
	}, {
		name: "invalid_proposals_are_refused_before_anyone_sees_them",
		base: map[string]baseFile{"sub/keep.txt": {"k\n", 0o644}, "f.txt": {"hello\n", 0o644}},
		agent: func(ctx context.Context, a *scriptedAgent) {
			for _, p := range []string{".", "sub", ".git/config", "SUB/../.GIT/hooks/pre-commit", "../escape.txt", "/etc/passwd", ""} {
				a.ask(ctx, [3]string{p, "(new file)", "x"})
			}
			a.ask(ctx, [3]string{"f.txt", "not in the file", "x"})
			a.ask(ctx, [3]string{"missing.txt", "a", "b"})
			a.ask(ctx, [3]string{"big.txt", "(new file)", strings.Repeat("a", 201*1024)})
		},
		check: func(t *testing.T, r scenarioResult) {
			assert.Equal(t, persistence.RunStatusSuccess, r.run.Status, "refusals alone do not fail the run")
			assert.Empty(t, r.run.GitCommitHash)
			require.Len(t, r.results, 10)
			for i, res := range r.results {
				assert.False(t, res.Approved, "proposal %d must be refused: %s", i, res.Feedback)
				assert.NotEmpty(t, res.Feedback, "the agent must learn why (%d)", i)
			}
			assert.Contains(t, r.events, "approval_path_escape")
		},
	}, {
		name: "symlinked_path_is_refused",
		agent: func(ctx context.Context, a *scriptedAgent) {
			assert.NoError(a.t, os.Symlink("/tmp", filepath.Join(a.env.Worktree, "link")))
			a.ask(ctx, [3]string{"link/x.txt", "(new file)", "x"})
		},
		check: func(t *testing.T, r scenarioResult) {
			require.Len(t, r.results, 1)
			assert.False(t, r.results[0].Approved, r.results[0].Feedback)
			assert.Equal(t, persistence.RunStatusFailed, r.run.Status, "the planted symlink is itself an unapproved change")
		},
	}, {
		name: "decision_not_recorded_means_not_approved",
		prepare: func(t *testing.T, db *sql.DB) {
			_, err := db.Exec(`CREATE TRIGGER no_decisions BEFORE INSERT ON run_event_logs
				WHEN NEW.event_type = 'yield_decided' BEGIN SELECT RAISE(ABORT, 'injected audit failure'); END`)
			require.NoError(t, err)
		},
		agent: func(ctx context.Context, a *scriptedAgent) {
			a.ask(ctx, [3]string{"a.txt", "(new file)", "a\n"})
		},
		check: func(t *testing.T, r scenarioResult) {
			require.Len(t, r.results, 1)
			assert.False(t, r.results[0].Approved, "an approval that could not be audited must not reach the agent")
			assert.Equal(t, persistence.RunStatusFailed, r.run.Status)
		},
	}}
}

// TestApproval_orchestrator_binds_the_exact_bytes catches any way for bytes
// nobody approved to reach the run branch: trusting an agent-supplied hash, the
// runtime writing something other than what was shown, unapproved files or
// index entries, the agent's own commits, bad paths, oversized proposals, and
// approvals released before they are on the audit chain.
func TestApproval_orchestrator_binds_the_exact_bytes(t *testing.T) {
	for _, sc := range approvalScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			sc.check(t, runApprovalScenario(t, sc.base, sc.agent, sc.prepare))
		})
	}
}

// TestApproval_runtime_mirrors_the_create_cap catches the runtime's copy of the
// create cap drifting from the orchestrator's: create_file would then send
// what the orchestrator refuses, or refuse what it would accept.
func TestApproval_runtime_mirrors_the_create_cap(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "template", "graph_exec.py.tmpl"))
	require.NoError(t, err)
	assert.Contains(t, string(src), fmt.Sprintf("_MAX_APPROVAL_BYTES = %d * 1024", orchestrator.MaxApprovedFileBytesForTest/1024))
}

// TestApproval_commit_is_built_from_the_approvals catches finalize committing
// whatever is on disk (or on the branch) at commit time rather than what was
// approved: a process still running after verify — started by an approved
// shell command, say — must not be able to change the commit.
func TestApproval_commit_is_built_from_the_approvals(t *testing.T) {
	t.Run("file_swapped_after_verify", func(t *testing.T) {
		repo := initGitRepo(t)
		hash, err := orchestrator.CommitApprovedForTest(repo, "a.txt", "approved\n", func() {
			require.NoError(t, os.WriteFile(filepath.Join(repo, "a.txt"), []byte("swapped after verify\n"), 0o644))
		})
		require.NoError(t, err)
		got, err := gitShow(t, repo, hash+":a.txt")
		require.NoError(t, err)
		assert.Equal(t, "approved\n", got)
	})
	t.Run("branch_moved_after_verify", func(t *testing.T) {
		repo := initGitRepo(t)
		hash, err := orchestrator.CommitApprovedForTest(repo, "a.txt", "approved\n", func() {
			out, err := exec.Command("git", "-C", repo, "commit", "-q", "--allow-empty", "-m", "moved").CombinedOutput()
			require.NoError(t, err, "%s", out)
		})
		assert.Error(t, err, "the branch moved under finalize; committing on top would carry that commit along")
		assert.Empty(t, hash)
	})
}
