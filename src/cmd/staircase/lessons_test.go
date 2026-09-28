package main

import (
	"encoding/json"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProjectLessons: a person's rejections in earlier runs of the project,
// with their reasons, become lessons for the next plan; refusals by the
// orchestrator or a policy, and rejections without a reason, do not.
func TestProjectLessons(t *testing.T) {
	_, s := e2eWorkspace(t)
	caseID, topo := seedFullCase(t, s)
	run, err := s.CreateRun(caseID, topo, "main")
	require.NoError(t, err)
	event := func(typ string, v map[string]any) {
		b, _ := json.Marshal(v)
		_, err := s.AppendEventLogChained(run.ID, typ, string(b), "")
		require.NoError(t, err)
	}
	request := func(file string) {
		event("yield_request", map[string]any{"type": "yield_request", "proposed_edits": []map[string]string{{"file": file}}})
	}
	decided := func(source string, approved bool, feedback string) {
		event("yield_decided", map[string]any{"source": source, "approved": approved, "feedback": feedback})
	}
	request("src/api.go")
	decided("operator", false, "no global variables, use the Server struct")
	request("go.mod")
	decided("orchestrator", false, "refused by the orchestrator: search_block not found")
	request("README.md")
	decided("operator", true, "fine")
	request("docs/x.md")
	decided("operator", false, "")

	lessons, err := projectLessons(s, mustProject(t, s, caseID))
	require.NoError(t, err)
	assert.Equal(t, []string{"src/api.go: no global variables, use the Server struct"}, lessons)
}

func mustProject(t *testing.T, s *persistence.Store, caseID int64) int64 {
	c, err := s.GetCase(caseID)
	require.NoError(t, err)
	return c.ProjectID
}
