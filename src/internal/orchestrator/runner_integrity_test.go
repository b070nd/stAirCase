package orchestrator_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

func TestRun_integrity_setup_failure_is_terminal(t *testing.T) {
	s := newTestStore(t)
	caseID, _ := scaffoldForRun(t, s, "")
	wsDir := t.TempDir() // No encryption key: fail after creating the run.
	err := orchestrator.NewRunner(s, wsDir).Run(context.Background(), caseID,
		orchestrator.RunOptions{SkipGates: true})
	require.Error(t, err)
	assert.ErrorIs(t, err, orchestrator.ErrRunNotSuccessful)
	runs, err := s.ListRunsByCase(caseID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, persistence.RunStatusFailed, runs[0].Status)
	assert.NotNil(t, runs[0].EndTime)
	c, err := s.GetCase(caseID)
	require.NoError(t, err)
	assert.Equal(t, persistence.CaseStatusFailed, c.Status)
	data, err := os.ReadFile(filepath.Join(wsDir, "runs", fmt.Sprint(runs[0].ID), "summary.json"))
	require.NoError(t, err)
	var summary orchestrator.RunSummary
	require.NoError(t, json.Unmarshal(data, &summary))
	assert.Equal(t, persistence.RunStatusFailed, summary.FinalStatus)
}

func TestRun_integrity_existing_branch_is_preserved(t *testing.T) {
	s := newTestStore(t)
	repo := initGitRepo(t)
	caseID, _ := scaffoldForRun(t, s, repo)
	createStaircaseBranch(t, repo, 1)
	before, err := exec.Command("git", "-C", repo, "rev-parse", "staircase/run-1").Output()
	require.NoError(t, err)
	err = orchestrator.NewRunner(s, t.TempDir()).Run(context.Background(), caseID,
		orchestrator.RunOptions{SkipGates: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
	after, err := exec.Command("git", "-C", repo, "rev-parse", "staircase/run-1").Output()
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
}

func TestRun_integrity_finalization(t *testing.T) {
	for _, scenario := range []string{"no_changes", "approved_change", "staging_failure", "commit_failure", "summary_failure", "budget_kill"} {
		t.Run(scenario, func(t *testing.T) {
			s, wsDir, repo, caseID := setupContentHashRun(t)
			_, err := s.CreateUserStory(caseID, "Must be independently verified")
			require.NoError(t, err)
			const content = "approved content\n"
			sum := sha256.Sum256([]byte(content))
			script := contentHashScript(hex.EncodeToString(sum[:]), content)
			wantStatus := persistence.RunStatusSuccess
			wantCaseStatus := persistence.CaseStatusPending
			var errorPart string
			switch scenario {
			case "no_changes":
				script = "import json, sys\njson.loads(sys.stdin.readline())\n"
			case "staging_failure":
				// Replace the run worktree's index with a directory so staging fails
				// deterministically (a linked worktree's index lives in its gitdir).
				script = strings.Replace(script, "s.close()", "import os\ngd = open(os.path.join(boot['project_path'], '.git')).read().split(':', 1)[1].strip()\np = os.path.join(gd, 'index')\nif os.path.exists(p): os.unlink(p)\nos.mkdir(p)\ns.close()", 1)
				errorPart = "stage approved files"
			case "commit_failure":
				if os.Geteuid() == 0 {
					t.Skip("root bypasses read-only reference permissions")
				}
				// The index remains writable, but publishing the branch ref cannot succeed.
				refPath := filepath.Join(repo, ".git", "refs", "heads", "staircase", "run-1")
				script = strings.Replace(script, "s.close()", fmt.Sprintf("import os\nos.chmod(%q, 0o400)\ns.close()", refPath), 1)
				t.Cleanup(func() { _ = os.Chmod(refPath, 0o600) })
				errorPart = "commit approved files"
			case "summary_failure":
				require.NoError(t, os.WriteFile(filepath.Join(wsDir, "runs"), []byte("not a directory"), 0o600))
				errorPart = "summary"
			case "budget_kill":
				c, err := s.GetCase(caseID)
				require.NoError(t, err)
				require.NoError(t, s.SetProjectBudgetCap(c.ProjectID, 0.00001))
				script = strings.Replace(script, "s.close()", `s.sendall((json.dumps({"type":"state_emit","active_agent":"coder","state":{"model":"claude-sonnet-4-6","input_tokens":100000,"output_tokens":100000}})+"\n").encode())
time.sleep(20)
s.close()`, 1)
				errorPart = "KILLED"
			}
			if errorPart != "" {
				wantStatus = persistence.RunStatusFailed
				wantCaseStatus = persistence.CaseStatusFailed
			}
			if scenario == "budget_kill" {
				wantStatus = persistence.RunStatusKilled
			}
			require.NoError(t, os.WriteFile(filepath.Join(wsDir, "tmp", fmt.Sprintf("graph_exec_case%d.py", caseID)), []byte(script), 0o600))
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			started := time.Now()
			runErr := orchestrator.NewRunner(s, wsDir).Run(ctx, caseID,
				orchestrator.RunOptions{SkipGates: true})
			if scenario == "budget_kill" {
				assert.Less(t, time.Since(started), 5*time.Second, "budget termination must not wait for the caller deadline")
			}
			if errorPart == "" {
				require.NoError(t, runErr)
			} else {
				assert.ErrorIs(t, runErr, orchestrator.ErrRunNotSuccessful)
				require.Error(t, runErr)
				assert.Contains(t, runErr.Error(), errorPart)
			}
			runs, err := s.ListRunsByCase(caseID)
			require.NoError(t, err)
			require.Len(t, runs, 1)
			assert.Equal(t, wantStatus, runs[0].Status)
			assert.NotNil(t, runs[0].EndTime)
			if scenario == "approved_change" || scenario == "summary_failure" {
				assert.NotEmpty(t, runs[0].GitCommitHash, "retain the delivered commit even when later finalization fails")
			} else {
				assert.Empty(t, runs[0].GitCommitHash)
			}
			c, err := s.GetCase(caseID)
			require.NoError(t, err)
			assert.Equal(t, wantCaseStatus, c.Status)
			stories, err := s.ListUserStoriesByCase(caseID)
			require.NoError(t, err)
			require.Len(t, stories, 1)
			assert.Equal(t, persistence.StoryStatusPending, stories[0].Status,
				"an agent exit or Git commit does not prove acceptance of a story")
		})
	}
}

func TestRun_integrity_persistence_failure_returns_error(t *testing.T) {
	_, wsDir, repo, _ := setupContentHashRun(t)
	db, err := persistence.InitDB(wsDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	s := persistence.NewStore(db)
	caseID, _ := scaffoldForRun(t, s, repo)
	// RUNNING and FAILED remain writable, but committing success is rejected.
	_, err = db.Exec(`CREATE TRIGGER reject_success BEFORE UPDATE ON cases WHEN NEW.status = 'COMPLETED' BEGIN SELECT RAISE(ABORT, 'injected terminal failure'); END`)
	require.NoError(t, err)
	script := "import json, sys\njson.loads(sys.stdin.readline())\n"
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "tmp", fmt.Sprintf("graph_exec_case%d.py", caseID)), []byte(script), 0o600))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err = orchestrator.NewRunner(s, wsDir).Run(ctx, caseID, orchestrator.RunOptions{SkipGates: true})
	require.ErrorContains(t, err, "injected terminal failure")
	assert.ErrorIs(t, err, orchestrator.ErrRunNotSuccessful)
	runs, err := s.ListRunsByCase(caseID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, persistence.RunStatusFailed, runs[0].Status)
	c, err := s.GetCase(caseID)
	require.NoError(t, err)
	assert.Equal(t, persistence.CaseStatusFailed, c.Status)
}
