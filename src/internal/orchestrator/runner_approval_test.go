package orchestrator_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/staircase-core/src/internal/orchestrator"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// approvalAgent wraps a scenario body with the real IPC client and helpers:
// ask() proposes edits (tuples of file, search, replace) and returns the
// response; write() writes like the generated runtime (temp file + rename,
// which leaves mode 0600); results is dumped for the test to inspect.
func approvalAgent(resultsPath, body string) string {
	return fmt.Sprintf(`import json, os, subprocess, sys, tempfile
boot = json.loads(sys.stdin.readline())
sys.path.insert(0, boot["runner_path"])
from staircase_runner.ipc import IPCClient
ipc = IPCClient(boot["socket_path"], boot["token"])
root = boot["project_path"]
results = []
def ask(*edits):
    r = ipc.yield_request("coder", "file_edit",
        [dict(file=f, search_block=s, replace_block=r) for f, s, r in edits], "approval test")
    results.append({"approved": r.get("approved"), "feedback": r.get("feedback", "")})
    json.dump(results, open(%q, "w"))  # record at once: the run may stop the agent next
    return r
def write(path, data):
    full = os.path.join(root, path)
    os.makedirs(os.path.dirname(full), exist_ok=True)
    fd, tmp = tempfile.mkstemp(dir=os.path.dirname(full))
    with os.fdopen(fd, "w", newline="") as f:
        f.write(data)
    os.replace(tmp, full)
%s
json.dump(results, open(%q, "w"))
ipc.close()
`, resultsPath, body, resultsPath)
}

type baseFile struct {
	content string
	mode    os.FileMode
}

type approvalResult struct {
	Approved bool   `json:"approved"`
	Feedback string `json:"feedback"`
}

// runApprovalScenario commits baseFiles to a fresh repo, runs the agent body
// with file_edit auto-approved by policy, and returns what the test needs.
func runApprovalScenario(t *testing.T, baseFiles map[string]baseFile, body string, prepare func(*sql.DB)) (s *persistence.Store, repo string, run persistence.Run, results []approvalResult, events []string) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test — skipped in -short mode")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	wsDir, err := os.MkdirTemp("", "strc-ap-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(wsDir) })
	prepareRunWorkspace(t, wsDir)
	repo = initGitRepo(t)
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
	s = persistence.NewStore(db)
	if prepare != nil {
		prepare(db)
	}
	caseID, _ := scaffoldForRun(t, s, repo)
	resultsPath := filepath.Join(t.TempDir(), "results.json")
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "tmp", fmt.Sprintf("graph_exec_case%d.py", caseID)),
		[]byte(approvalAgent(resultsPath, body)), 0o600))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_ = orchestrator.NewRunner(s, wsDir).Run(ctx, caseID, orchestrator.RunOptions{SkipGates: true})

	runs, err := s.ListRunsByCase(caseID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	run = runs[0]
	if b, err := os.ReadFile(resultsPath); err == nil {
		require.NoError(t, json.Unmarshal(b, &results))
	}
	logs, err := s.ListEventLogs(run.ID)
	require.NoError(t, err)
	for _, l := range logs {
		events = append(events, l.EventType)
	}
	require.NoError(t, s.VerifyChain(run.ID))
	return s, repo, run, results, events
}

func gitShow(t *testing.T, repo, rev string) (string, error) {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "show", rev).CombinedOutput()
	return string(out), err
}

func TestApproval_orchestrator_binds_the_exact_bytes(t *testing.T) {
	t.Run("create_without_agent_hash", func(t *testing.T) {
		_, repo, run, res, _ := runApprovalScenario(t, nil, `
ask(("new.txt", "(new file)", "hello\n"))
write("new.txt", "hello\n")`, nil)
		require.Equal(t, persistence.RunStatusSuccess, run.Status)
		require.Len(t, res, 1)
		assert.True(t, res[0].Approved)
		got, err := gitShow(t, repo, fmt.Sprintf("staircase/run-%d:new.txt", run.ID))
		require.NoError(t, err)
		assert.Equal(t, "hello\n", got)
	})

	t.Run("edits_chain_and_mirror_python_newlines", func(t *testing.T) {
		_, repo, run, _, _ := runApprovalScenario(t, map[string]baseFile{"f.txt": {"a\r\nb\r\n", 0o644}}, `
ask(("f.txt", "b\n", "B\n"))
write("f.txt", "a\nB\n")
ask(("f.txt", "a\n", "A\n"))
write("f.txt", "A\nB\n")`, nil)
		require.Equal(t, persistence.RunStatusSuccess, run.Status)
		got, err := gitShow(t, repo, fmt.Sprintf("staircase/run-%d:f.txt", run.ID))
		require.NoError(t, err)
		assert.Equal(t, "A\nB\n", got)
	})

	t.Run("post_approval_tamper_fails", func(t *testing.T) {
		_, _, run, _, ev := runApprovalScenario(t, nil, `
ask(("x.txt", "(new file)", "approved\n"))
write("x.txt", "something else\n")`, nil)
		assert.Equal(t, persistence.RunStatusFailed, run.Status)
		assert.Empty(t, run.GitCommitHash)
		assert.Contains(t, ev, "approval_content_mismatch")
	})

	t.Run("unapproved_extra_file_fails", func(t *testing.T) {
		_, _, run, _, ev := runApprovalScenario(t, nil, `
ask(("a.txt", "(new file)", "a\n"))
write("a.txt", "a\n")
write("extra.txt", "never approved\n")`, nil)
		assert.Equal(t, persistence.RunStatusFailed, run.Status)
		assert.Empty(t, run.GitCommitHash)
		assert.Contains(t, ev, "unapproved_worktree_change")
	})

	t.Run("direct_index_write_fails", func(t *testing.T) {
		_, _, run, _, ev := runApprovalScenario(t, nil, `
ask(("a.txt", "(new file)", "a\n"))
write("a.txt", "a\n")
write("sneaky.txt", "staged behind the orchestrator's back\n")
subprocess.run(["git", "-C", root, "add", "sneaky.txt"], check=True)`, nil)
		assert.Equal(t, persistence.RunStatusFailed, run.Status)
		assert.Empty(t, run.GitCommitHash)
		assert.Contains(t, ev, "unapproved_worktree_change")
	})

	t.Run("unapproved_deletion_fails", func(t *testing.T) {
		_, _, run, _, ev := runApprovalScenario(t, map[string]baseFile{"old.txt": {"keep me\n", 0o644}}, `
os.remove(os.path.join(root, "old.txt"))`, nil)
		assert.Equal(t, persistence.RunStatusFailed, run.Status)
		assert.Contains(t, ev, "unapproved_worktree_change")
	})

	t.Run("approved_deletion_is_committed", func(t *testing.T) {
		_, repo, run, res, _ := runApprovalScenario(t, map[string]baseFile{"old.txt": {"bye\n", 0o644}, "keep.txt": {"k\n", 0o644}}, `
ask(("old.txt", "(delete file)", ""))
os.remove(os.path.join(root, "old.txt"))`, nil)
		require.Equal(t, persistence.RunStatusSuccess, run.Status, "%v", res)
		_, err := gitShow(t, repo, fmt.Sprintf("staircase/run-%d:old.txt", run.ID))
		assert.Error(t, err, "old.txt must be deleted on the run branch")
	})

	t.Run("exec_bit_preserved", func(t *testing.T) {
		_, repo, run, _, _ := runApprovalScenario(t, map[string]baseFile{"run.sh": {"echo a\n", 0o755}}, `
ask(("run.sh", "echo a", "echo b"))
write("run.sh", "echo b\n")`, nil)
		require.Equal(t, persistence.RunStatusSuccess, run.Status)
		out, err := exec.Command("git", "-C", repo, "ls-tree", fmt.Sprintf("staircase/run-%d", run.ID), "run.sh").CombinedOutput()
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(string(out), "100755"), "mode must stay executable: %s", out)
	})

	t.Run("invalid_proposals_are_refused_before_anyone_sees_them", func(t *testing.T) {
		_, _, run, res, ev := runApprovalScenario(t, map[string]baseFile{"sub/keep.txt": {"k\n", 0o644}, "f.txt": {"hello\n", 0o644}}, `
for p in [".", "sub", ".git/config", "SUB/../.GIT/hooks/pre-commit", "../escape.txt", "/etc/passwd", ""]:
    ask((p, "(new file)", "x"))
ask(("f.txt", "not in the file", "x"))
ask(("missing.txt", "a", "b"))
ask(("big.txt", "(new file)", "a" * (201 * 1024)))`, nil)
		assert.Equal(t, persistence.RunStatusSuccess, run.Status, "refusals alone do not fail the run")
		assert.Empty(t, run.GitCommitHash)
		require.Len(t, res, 10)
		for i, r := range res {
			assert.False(t, r.Approved, "proposal %d must be refused: %s", i, r.Feedback)
			assert.NotEmpty(t, r.Feedback, "the agent must learn why (%d)", i)
		}
		assert.Contains(t, ev, "approval_path_escape")
	})

	t.Run("proposal_over_the_line_limit_is_refused_not_fatal", func(t *testing.T) {
		// 120,000 bytes as UTF-8 (under the create cap) but 360,000 once
		// JSON-escaped: the client must refuse it rather than overflow the
		// orchestrator's 256 KiB line limit and lose the connection.
		_, _, run, res, _ := runApprovalScenario(t, nil, `
ask(("wide.txt", "(new file)", "é" * 60000))`, nil)
		require.Len(t, res, 1)
		assert.False(t, res[0].Approved)
		assert.Contains(t, res[0].Feedback, "split it")
		assert.Equal(t, persistence.RunStatusSuccess, run.Status)
	})

	t.Run("symlinked_path_is_refused", func(t *testing.T) {
		_, _, run, res, _ := runApprovalScenario(t, nil, `
os.symlink("/tmp", os.path.join(root, "link"))
ask(("link/x.txt", "(new file)", "x"))`, nil)
		require.Len(t, res, 1)
		assert.False(t, res[0].Approved, res[0].Feedback)
		assert.Equal(t, persistence.RunStatusFailed, run.Status, "the planted symlink is itself an unapproved change")
	})

	t.Run("decision_not_recorded_means_not_approved", func(t *testing.T) {
		_, _, run, res, _ := runApprovalScenario(t, nil, `
import signal
signal.signal(signal.SIGTERM, signal.SIG_IGN)  # outlive the stop long enough to record the answer
ask(("a.txt", "(new file)", "a\n"))`, func(db *sql.DB) {
			_, err := db.Exec(`CREATE TRIGGER no_decisions BEFORE INSERT ON run_event_logs
				WHEN NEW.event_type = 'yield_decided' BEGIN SELECT RAISE(ABORT, 'injected audit failure'); END`)
			require.NoError(t, err)
		})
		require.Len(t, res, 1)
		assert.False(t, res[0].Approved, "an approval that could not be audited must not reach the agent")
		assert.Equal(t, persistence.RunStatusFailed, run.Status)
	})
}
