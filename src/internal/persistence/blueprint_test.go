package persistence_test

import (
	"strings"
	"testing"

	"github.com/b070nd/staircase-core/src/internal/domain"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBlueprints_import_is_idempotent_and_found_by_prefix(t *testing.T) {
	s := newTestStore(t)
	a, b := "abc1"+strings.Repeat("0", 60), "abc2"+strings.Repeat("0", 60)
	created, err := s.ImportBlueprint(domain.Blueprint{Hash: a, Name: "one", Content: "{}"})
	require.NoError(t, err)
	assert.True(t, created)
	created, err = s.ImportBlueprint(domain.Blueprint{Hash: a, Name: "one", Content: "{}", SourceDir: "/elsewhere"})
	require.NoError(t, err)
	assert.False(t, created, "a snapshot is immutable: the first import wins")
	_, err = s.ImportBlueprint(domain.Blueprint{Hash: b, Name: "two", Content: "{}"})
	require.NoError(t, err)

	list, err := s.ListBlueprints()
	require.NoError(t, err)
	assert.Len(t, list, 2)
	got, err := s.FindBlueprint("abc1")
	require.NoError(t, err)
	assert.Equal(t, "one", got.Name)
	assert.Empty(t, got.SourceDir)
	_, err = s.FindBlueprint("abc")
	assert.ErrorContains(t, err, "ambiguous")
	_, err = s.FindBlueprint("fff")
	assert.ErrorContains(t, err, "no blueprint")
	_, err = s.FindBlueprint("")
	assert.Error(t, err)
}

func binding(hash string) persistence.Binding {
	return persistence.Binding{Hash: hash, Supervisor: "sup",
		Agents: []domain.AgentNode{{Name: "sup", Role: "route", Model: "m"}, {Name: "coder", Role: "code", Model: "m"}},
		Edges:  []domain.Edge{{FromNode: "sup", ToNode: "coder"}},
		Cases: []persistence.BoundCase{{Slug: "greet", PRD: "Say hello.",
			Stories: []domain.UserStory{{Description: "greeting", CustomConfig: `{"allow":["GREETING.md"]}`}}}}}
}

func TestBindBlueprint_materializes_new_rows(t *testing.T) {
	s := newTestStore(t)
	_, projectID, oldCase := scaffold(t, s)
	old, err := s.CreateSwarmTopology(projectID, "legacy", "memory", "langgraph")
	require.NoError(t, err)

	version, cases, err := s.BindBlueprint(projectID, binding("h1"))
	require.NoError(t, err)
	assert.Equal(t, old.Version+1, version)
	require.Len(t, cases, 1)

	topo, err := s.GetLatestTopology(projectID)
	require.NoError(t, err)
	assert.Equal(t, "sup", topo.SupervisorName)
	agents, err := s.ListAgentNodes(topo.ID)
	require.NoError(t, err)
	assert.Len(t, agents, 2)
	edges, err := s.ListEdges(topo.ID)
	require.NoError(t, err)
	assert.Len(t, edges, 1)
	c, err := s.GetCase(cases[0])
	require.NoError(t, err)
	assert.Equal(t, "Say hello.", c.PrdJSON)
	stories, err := s.ListUserStoriesByCase(cases[0])
	require.NoError(t, err)
	require.Len(t, stories, 1)
	assert.Equal(t, `{"allow":["GREETING.md"]}`, stories[0].CustomConfig)
	hash, slug, err := s.CaseBlueprint(cases[0])
	require.NoError(t, err)
	assert.Equal(t, [2]string{"h1", "greet"}, [2]string{hash, slug})
	bound, err := s.ProjectBlueprint(projectID)
	require.NoError(t, err)
	assert.Equal(t, "h1", bound)

	hash, _, err = s.CaseBlueprint(oldCase)
	require.NoError(t, err)
	assert.Empty(t, hash, "existing cases are not rebound")
	legacy, err := s.GetTopology(old.ID)
	require.NoError(t, err)
	assert.Equal(t, "legacy", legacy.SupervisorName, "existing topologies are not changed")
}

func TestBindBlueprint_is_atomic(t *testing.T) {
	db, err := persistence.InitDB(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	s := persistence.NewStore(db)
	_, projectID, _ := scaffold(t, s)
	_, err = db.Exec(`CREATE TRIGGER reject_story BEFORE INSERT ON user_stories BEGIN SELECT RAISE(ABORT, 'injected story failure'); END`)
	require.NoError(t, err)

	_, _, err = s.BindBlueprint(projectID, binding("h1"))
	require.ErrorContains(t, err, "injected story failure")
	topo, err := s.GetLatestTopology(projectID)
	require.NoError(t, err)
	assert.Nil(t, topo, "no topology from a failed bind")
	cases, err := s.ListCasesByProject(projectID)
	require.NoError(t, err)
	assert.Len(t, cases, 1, "only the scaffolded case")
	bound, err := s.ProjectBlueprint(projectID)
	require.NoError(t, err)
	assert.Empty(t, bound)
}
