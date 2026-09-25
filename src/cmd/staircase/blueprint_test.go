package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/b070nd/staircase-core/src/internal/blueprint"
	"github.com/b070nd/staircase-core/src/internal/gate"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/b070nd/staircase-core/src/internal/plan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gateResult runs every gate for caseID and returns the named one.
func gateResult(t *testing.T, wsDir string, caseID int64, name string) gate.Result {
	t.Helper()
	store, db, err := openStore()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	for _, r := range gate.RunAll(gate.Context{WsDir: wsDir, Store: store, CaseID: caseID}).Gates {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("gate %s did not run", name)
	return gate.Result{}
}

// TestE2E_Blueprint_import_bind_compile_pinned walks the blueprint flow with
// the shipped example: import is idempotent and records the source commit of
// a clean checkout, bind materializes the case, compile carries the stories
// and the blueprint, and an imperative topology change blocks the run.
func TestE2E_Blueprint_import_bind_compile_pinned(t *testing.T) {
	wsDir, s := e2eWorkspace(t)
	repo := t.TempDir() // the blueprint's own repository
	src, err := os.ReadDir("../../../examples/blueprints/hello")
	require.NoError(t, err)
	require.NotEmpty(t, src)
	require.NoError(t, os.CopyFS(repo, os.DirFS("../../../examples/blueprints/hello")))
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "blueprint"}} {
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}

	require.NoError(t, blueprintImportCmd.RunE(nil, []string{repo}))
	require.NoError(t, blueprintImportCmd.RunE(nil, []string{repo}), "importing again is a no-op")
	list, err := s.ListBlueprints()
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Len(t, list[0].GitSHA, 40, "a clean checkout records its commit")

	v, err := s.CreateVendor("acme")
	require.NoError(t, err)
	p, err := s.CreateProject(v.ID, "app", "")
	require.NoError(t, err)
	require.NoError(t, projectBindCmd.RunE(nil, []string{fmt.Sprint(p.ID), list[0].Hash[:12]}))
	cases, err := s.ListCasesByProject(p.ID)
	require.NoError(t, err)
	require.Len(t, cases, 1)
	caseID := cases[0].ID

	compileForce = true
	require.NoError(t, compileCaseHandler(nil, []string{fmt.Sprint(caseID)}))
	pl, err := plan.Load(filepath.Join(wsDir, "tmp", fmt.Sprintf("plan_case%d.json", caseID)))
	require.NoError(t, err)
	assert.Equal(t, list[0].Hash, pl.BlueprintHash)
	require.Len(t, pl.Stories, 2)
	assert.Equal(t, []string{"GREETING.md"}, pl.Stories[0].Allow)
	assert.Equal(t, 1, pl.Stories[0].MaxFiles)
	assert.Equal(t, 2, pl.Limits.MaxScopeViolations, "a bound case runs under its blueprint's limits")
	r := gateResult(t, wsDir, caseID, "runtime.plan_pinned")
	assert.Equal(t, gate.StatusPass, r.Status, r.Message)

	topo, err := s.GetLatestTopology(p.ID)
	require.NoError(t, err)
	require.NoError(t, agentAddCmd.RunE(nil, []string{fmt.Sprint(topo.ID), "tester", "You test."}))
	require.NoError(t, compileCaseHandler(nil, []string{fmt.Sprint(caseID)}))
	r = gateResult(t, wsDir, caseID, "runtime.plan_pinned")
	assert.Equal(t, gate.StatusFail, r.Status)
	assert.Contains(t, r.Message, "drifted from blueprint hello")
}

// TestE2E_story_scope sets, validates and clears a story's scope, and refuses
// stories whose scope comes from a blueprint.
func TestE2E_story_scope(t *testing.T) {
	_, s := e2eWorkspace(t)
	caseID, _ := seedFullCase(t, s)
	stories, err := s.ListUserStoriesByCase(caseID)
	require.NoError(t, err)
	id := fmt.Sprint(stories[0].ID)
	t.Cleanup(func() { storyScopeAllow, storyScopeMaxFiles = nil, 0 })

	storyScopeAllow, storyScopeMaxFiles = []string{"docs/**", "README.md"}, 3
	require.NoError(t, storyScopeCmd.RunE(nil, []string{id}))
	st, err := s.GetUserStory(stories[0].ID)
	require.NoError(t, err)
	assert.JSONEq(t, `{"allow":["docs/**","README.md"],"max_files":3}`, st.CustomConfig)

	storyScopeAllow = []string{"../outside"}
	assert.Error(t, storyScopeCmd.RunE(nil, []string{id}))
	storyScopeAllow, storyScopeMaxFiles = nil, 0
	require.NoError(t, storyScopeCmd.RunE(nil, []string{id}))
	st, err = s.GetUserStory(stories[0].ID)
	require.NoError(t, err)
	assert.Empty(t, st.CustomConfig)

	hello := filepath.Join("..", "..", "..", "examples", "blueprints", "hello")
	require.NoError(t, blueprintImportCmd.RunE(nil, []string{hello}))
	list, err := s.ListBlueprints()
	require.NoError(t, err)
	c, err := s.GetCase(caseID)
	require.NoError(t, err)
	_, cases, err := s.BindBlueprint(c.ProjectID, blueprintBinding(t, list[0].Content))
	require.NoError(t, err)
	bound, err := s.ListUserStoriesByCase(cases[0])
	require.NoError(t, err)
	assert.ErrorContains(t, storyScopeCmd.RunE(nil, []string{fmt.Sprint(bound[0].ID)}), "blueprint")
}

func blueprintBinding(t *testing.T, content string) persistence.Binding {
	b, err := blueprint.Parse([]byte(content))
	require.NoError(t, err)
	return b.Binding()
}
