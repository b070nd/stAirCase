package orchestrator_test

import (
	"context"
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
	for _, scenario := range []string{"no_changes", "approved_change", "status_failure", "commit_failure", "summary_failure", "budget_kill"} {
		t.Run(scenario, func(t *testing.T) {
			s, wsDir, repo, caseID := setupApprovalRun(t)
			_, err := s.CreateUserStory(caseID, "Must be independently verified")
			require.NoError(t, err)
			const content = "approved content\n"
			agent := approveThenWrite(content, content)
			then := func(*orchestrator.AgentEnv) {} // after the approved write
			wantStatus := persistence.RunStatusSuccess
			wantCaseStatus := persistence.CaseStatusPending
			var errorPart string
			switch scenario {
			case "no_changes":
				agent = idle
			case "status_failure":
				// Replace the run worktree's index with a directory so reading the
				// worktree status fails deterministically (a linked worktree's index
				// lives in its gitdir).
				then = func(env *orchestrator.AgentEnv) {
					b, err := os.ReadFile(filepath.Join(env.Worktree, ".git"))
					assert.NoError(t, err)
					gd := strings.TrimSpace(strings.TrimPrefix(string(b), "gitdir:"))
					_ = os.Remove(filepath.Join(gd, "index"))
					assert.NoError(t, os.Mkdir(filepath.Join(gd, "index"), 0o755))
				}
				errorPart = "read worktree status"
			case "commit_failure":
				if os.Geteuid() == 0 {
					t.Skip("root bypasses read-only reference permissions")
				}
				// Objects can be written, but the branch ref cannot be updated: git
				// cannot create its lock file in a read-only refs directory.
				refDir := filepath.Join(repo, ".git", "refs", "heads", "staircase")
				then = func(*orchestrator.AgentEnv) { assert.NoError(t, os.Chmod(refDir, 0o500)) }
				t.Cleanup(func() { _ = os.Chmod(refDir, 0o700) })
				errorPart = "git update-ref"
			case "summary_failure":
				require.NoError(t, os.WriteFile(filepath.Join(wsDir, "runs"), []byte("not a directory"), 0o600))
				errorPart = "summary"
			case "budget_kill":
				c, err := s.GetCase(caseID)
				require.NoError(t, err)
				require.NoError(t, s.SetProjectBudgetCap(c.ProjectID, 0.00001))
				agent = func(ctx context.Context, env *orchestrator.AgentEnv) error {
					env.Emit(ctx, orchestrator.Usage{Agent: "coder", Model: "claude-sonnet-4-6", InputTokens: 100000, OutputTokens: 100000})
					<-ctx.Done()
					return ctx.Err()
				}
				errorPart = "KILLED"
			}
			if errorPart != "" {
				wantStatus = persistence.RunStatusFailed
				wantCaseStatus = persistence.CaseStatusFailed
			}
			if scenario == "budget_kill" {
				wantStatus = persistence.RunStatusKilled
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			started := time.Now()
			runErr := orchestrator.NewRunner(s, wsDir).Run(ctx, caseID, orchestrator.RunOptions{SkipGates: true,
				Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
					if err := agent(ctx, env); err != nil {
						return err
					}
					then(env)
					return nil
				})})
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
	_, wsDir, repo, _ := setupApprovalRun(t)
	db, err := persistence.InitDB(wsDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	s := persistence.NewStore(db)
	caseID, _ := scaffoldForRun(t, s, repo)
	// RUNNING and FAILED remain writable, but committing success is rejected.
	_, err = db.Exec(`CREATE TRIGGER reject_success BEFORE UPDATE ON cases WHEN NEW.status = 'COMPLETED' BEGIN SELECT RAISE(ABORT, 'injected terminal failure'); END`)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err = orchestrator.NewRunner(s, wsDir).Run(ctx, caseID, orchestrator.RunOptions{SkipGates: true, Agent: idle})
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
