package persistence_test

import (
	"testing"
	"time"

	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFinishRun_preserves_story_acceptance(t *testing.T) {
	for _, storyStatus := range []string{"PENDING", "INVALIDATED", "IMPLEMENTED"} {
		t.Run(storyStatus, func(t *testing.T) {
			s := newTestStore(t)
			_, projectID, caseID := scaffold(t, s)
			_, err := s.CreateSwarmTopology(projectID, "sup", "memory", "langgraph")
			require.NoError(t, err)
			run, err := s.CreateRun(caseID, 1, "main")
			require.NoError(t, err)
			story, err := s.CreateUserStory(caseID, "Acceptance is independent of execution")
			require.NoError(t, err)
			require.NoError(t, s.UpdateUserStoryStatus(story.ID, storyStatus))
			require.NoError(t, s.FinishRun(run.ID, persistence.RunStatusSuccess, time.Now(), "commit"))
			got, err := s.GetRun(run.ID)
			require.NoError(t, err)
			assert.Equal(t, persistence.RunStatusSuccess, got.Status)
			assert.Equal(t, "commit", got.GitCommitHash)
			assert.NotNil(t, got.EndTime)
			c, err := s.GetCase(caseID)
			require.NoError(t, err)
			want := persistence.CaseStatusPending
			if storyStatus == persistence.StoryStatusImplemented {
				want = persistence.CaseStatusCompleted
			}
			assert.Equal(t, want, c.Status)
			stories, err := s.ListUserStoriesByCase(caseID)
			require.NoError(t, err)
			require.Len(t, stories, 1)
			assert.Equal(t, storyStatus, stories[0].Status)
		})
	}
}

func TestFinishRun_rolls_back_on_case_failure(t *testing.T) {
	db, err := persistence.InitDB(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	s := persistence.NewStore(db)
	_, projectID, caseID := scaffold(t, s)
	_, err = s.CreateSwarmTopology(projectID, "sup", "memory", "langgraph")
	require.NoError(t, err)
	run, err := s.CreateRun(caseID, 1, "main")
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TRIGGER reject_case_update BEFORE UPDATE ON cases BEGIN SELECT RAISE(ABORT, 'injected case failure'); END`)
	require.NoError(t, err)
	err = s.FinishRun(run.ID, persistence.RunStatusSuccess, time.Now(), "commit")
	require.ErrorContains(t, err, "injected case failure")
	got, err := s.GetRun(run.ID)
	require.NoError(t, err)
	assert.Equal(t, persistence.RunStatusRunning, got.Status, "a failed transaction must not partially commit success")
	assert.Empty(t, got.GitCommitHash)
	assert.Nil(t, got.EndTime)
	c, err := s.GetCase(caseID)
	require.NoError(t, err)
	assert.Equal(t, persistence.CaseStatusPending, c.Status)
}

func TestFinishRun_rejects_invalid_targets(t *testing.T) {
	s := newTestStore(t)
	require.Error(t, s.FinishRun(123, persistence.RunStatusRunning, time.Now(), ""))
	require.Error(t, s.FinishRun(123, persistence.RunStatusSuccess, time.Now(), ""))
}

func TestAcceptUserStory_completes_case_with_audit(t *testing.T) {
	s := newTestStore(t)
	_, projectID, caseID := scaffold(t, s)
	_, err := s.CreateSwarmTopology(projectID, "sup", "memory", "langgraph")
	require.NoError(t, err)
	first, err := s.CreateUserStory(caseID, "first")
	require.NoError(t, err)
	second, err := s.CreateUserStory(caseID, "second")
	require.NoError(t, err)

	_, _, err = s.AcceptUserStory(first.ID, "alice")
	require.ErrorContains(t, err, "no successful run", "acceptance requires delivered work")

	run, err := s.CreateRun(caseID, 1, "main")
	require.NoError(t, err)
	_, err = s.AppendEventLogChained(run.ID, "state_emit", `{"x":1}`, "")
	require.NoError(t, err)
	require.NoError(t, s.FinishRun(run.ID, persistence.RunStatusSuccess, time.Now(), "c0ffee"))

	runID, status, err := s.AcceptUserStory(first.ID, "alice")
	require.NoError(t, err)
	assert.Equal(t, run.ID, runID)
	assert.Equal(t, persistence.CaseStatusPending, status, "one story is still outstanding")

	_, status, err = s.AcceptUserStory(second.ID, "alice")
	require.NoError(t, err)
	assert.Equal(t, persistence.CaseStatusCompleted, status)
	c, err := s.GetCase(caseID)
	require.NoError(t, err)
	assert.Equal(t, persistence.CaseStatusCompleted, c.Status)

	logs, err := s.ListEventLogs(run.ID)
	require.NoError(t, err)
	require.Len(t, logs, 3)
	assert.Equal(t, "story_accepted", logs[2].EventType)
	assert.Contains(t, logs[2].Payload, `"actor":"alice"`)
	assert.Equal(t, "c0ffee", logs[2].GitCommitHash, "acceptance is bound to the delivered commit")
	require.NoError(t, s.VerifyChain(run.ID))

	_, _, err = s.AcceptUserStory(999999, "alice")
	require.ErrorContains(t, err, "not found")
}

func TestAcceptUserStory_is_atomic(t *testing.T) {
	db, err := persistence.InitDB(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	s := persistence.NewStore(db)
	_, projectID, caseID := scaffold(t, s)
	_, err = s.CreateSwarmTopology(projectID, "sup", "memory", "langgraph")
	require.NoError(t, err)
	story, err := s.CreateUserStory(caseID, "only")
	require.NoError(t, err)
	run, err := s.CreateRun(caseID, 1, "main")
	require.NoError(t, err)
	require.NoError(t, s.FinishRun(run.ID, persistence.RunStatusSuccess, time.Now(), ""))
	_, err = db.Exec(`CREATE TRIGGER reject_case_update BEFORE UPDATE ON cases BEGIN SELECT RAISE(ABORT, 'injected case failure'); END`)
	require.NoError(t, err)

	_, _, err = s.AcceptUserStory(story.ID, "alice")
	require.ErrorContains(t, err, "injected case failure")
	stories, err := s.ListUserStoriesByCase(caseID)
	require.NoError(t, err)
	assert.Equal(t, persistence.StoryStatusPending, stories[0].Status, "no partial acceptance")
	logs, err := s.ListEventLogs(run.ID)
	require.NoError(t, err)
	assert.Empty(t, logs, "no audit entry for an acceptance that did not happen")
}
