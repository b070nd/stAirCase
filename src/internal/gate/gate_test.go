package gate_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/b070nd/staircase-core/src/internal/gate"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── test helpers ─────────────────────────────────────────────────────────────

func newGateEnv(t *testing.T) (ctx gate.Context, wsDir string) {
	t.Helper()
	wsDir = t.TempDir()
	db, err := persistence.InitDB(wsDir)
	require.NoError(t, err, "InitDB must succeed")
	t.Cleanup(func() { db.Close() })
	store := persistence.NewStore(db)
	ctx = gate.Context{WsDir: wsDir, Store: store}
	return ctx, wsDir
}

func makeCase(t *testing.T, s *persistence.Store) (projectID, caseID int64) {
	t.Helper()
	v, err := s.CreateVendor(t.Name() + "_vendor")
	require.NoError(t, err)
	p, err := s.CreateProject(v.ID, t.Name()+"_project", "")
	require.NoError(t, err)
	c, err := s.CreateCase(p.ID)
	require.NoError(t, err)
	return p.ID, c.ID
}

func makeTopo(t *testing.T, s *persistence.Store, projectID int64, supervisorName string, agents ...string) int64 {
	t.Helper()
	topo, err := s.CreateSwarmTopology(projectID, supervisorName, "memory", "langgraph")
	require.NoError(t, err)
	for _, name := range agents {
		_, err = s.CreateAgentNode(topo.ID, name, "role for "+name, "claude-haiku-4-6", nil)
		require.NoError(t, err)
	}
	return topo.ID
}

// ─── Report / RunAll core ─────────────────────────────────────────────────────

func TestReport_Blocking_true_when_fail(t *testing.T) {
	r := gate.Report{Overall: gate.StatusFail}
	assert.True(t, r.Blocking())
}

func TestReport_Blocking_false_for_warn(t *testing.T) {
	r := gate.Report{Overall: gate.StatusWarn}
	assert.False(t, r.Blocking())
}

func TestReport_Blocking_false_for_pass(t *testing.T) {
	r := gate.Report{Overall: gate.StatusPass}
	assert.False(t, r.Blocking())
}

func TestRunAll_summary_counts(t *testing.T) {
	gate.ReplaceRegistry(t, []gate.Gate{
		&stubGate{"a", gate.SeverityBlock, gate.StatusPass, ""},
		&stubGate{"b", gate.SeverityWarn, gate.StatusWarn, ""},
		&stubGate{"c", gate.SeverityBlock, gate.StatusFail, ""},
		&stubGate{"d", gate.SeverityBlock, gate.StatusSkip, ""},
	})

	ctx, _ := newGateEnv(t)
	r := gate.RunAll(ctx)

	assert.Equal(t, 1, r.Summary.Pass)
	assert.Equal(t, 1, r.Summary.Warn)
	assert.Equal(t, 1, r.Summary.Fail)
	assert.Equal(t, 1, r.Summary.Skip)
}

func TestRunAll_overall_fail_when_any_block_fails(t *testing.T) {
	gate.ReplaceRegistry(t, []gate.Gate{
		&stubGate{"a", gate.SeverityBlock, gate.StatusPass, ""},
		&stubGate{"b", gate.SeverityBlock, gate.StatusFail, ""},
		&stubGate{"c", gate.SeverityWarn, gate.StatusWarn, ""},
	})

	ctx, _ := newGateEnv(t)
	r := gate.RunAll(ctx)
	assert.Equal(t, gate.StatusFail, r.Overall)
	assert.True(t, r.Blocking())
}

func TestRunAll_overall_warn_when_no_fail_but_warn(t *testing.T) {
	gate.ReplaceRegistry(t, []gate.Gate{
		&stubGate{"a", gate.SeverityBlock, gate.StatusPass, ""},
		&stubGate{"b", gate.SeverityWarn, gate.StatusWarn, ""},
	})

	ctx, _ := newGateEnv(t)
	r := gate.RunAll(ctx)
	assert.Equal(t, gate.StatusWarn, r.Overall)
	assert.False(t, r.Blocking())
}

func TestRunAll_overall_pass_when_all_pass(t *testing.T) {
	gate.ReplaceRegistry(t, []gate.Gate{
		&stubGate{"a", gate.SeverityBlock, gate.StatusPass, ""},
		&stubGate{"b", gate.SeverityWarn, gate.StatusPass, ""},
	})

	ctx, _ := newGateEnv(t)
	r := gate.RunAll(ctx)
	assert.Equal(t, gate.StatusPass, r.Overall)
}

// stubGate is an in-test gate implementation for RunAll logic tests.
type stubGate struct {
	name string
	sev  gate.Severity
	stat gate.Status
	msg  string
}

func (g *stubGate) Name() string            { return g.name }
func (g *stubGate) Category() string        { return "test" }
func (g *stubGate) Severity() gate.Severity { return g.sev }
func (g *stubGate) Run(_ gate.Context) gate.Result {
	return gate.Result{Name: g.name, Category: "test", Severity: g.sev, Status: g.stat, Message: g.msg}
}

// ─── Structural gates ─────────────────────────────────────────────────────────

func TestCaseProjectExistsGate_missing_case(t *testing.T) {
	ctx, _ := newGateEnv(t)
	ctx.CaseID = 999_999
	r := gate.CaseProjectExistsGate.Run(ctx)
	assert.Equal(t, gate.StatusFail, r.Status)
	assert.Equal(t, gate.SeverityBlock, r.Severity)
}

func TestCaseProjectExistsGate_valid(t *testing.T) {
	ctx, _ := newGateEnv(t)
	_, ctx.CaseID = makeCase(t, ctx.Store)
	r := gate.CaseProjectExistsGate.Run(ctx)
	assert.Equal(t, gate.StatusPass, r.Status)
}

func TestCaseHasStoriesGate_no_stories(t *testing.T) {
	ctx, _ := newGateEnv(t)
	_, ctx.CaseID = makeCase(t, ctx.Store)
	r := gate.CaseHasStoriesGate.Run(ctx)
	assert.Equal(t, gate.StatusFail, r.Status)
	assert.Equal(t, gate.SeverityBlock, r.Severity)
}

func TestCaseHasStoriesGate_all_invalidated(t *testing.T) {
	ctx, _ := newGateEnv(t)
	_, ctx.CaseID = makeCase(t, ctx.Store)
	us, _ := ctx.Store.CreateUserStory(ctx.CaseID, "Story")
	ctx.Store.UpdateUserStoryStatus(us.ID, "INVALIDATED")

	r := gate.CaseHasStoriesGate.Run(ctx)
	assert.Equal(t, gate.StatusFail, r.Status, "no PENDING stories → FAIL")
}

func TestCaseHasStoriesGate_has_pending(t *testing.T) {
	ctx, _ := newGateEnv(t)
	_, ctx.CaseID = makeCase(t, ctx.Store)
	ctx.Store.CreateUserStory(ctx.CaseID, "Pending story")

	r := gate.CaseHasStoriesGate.Run(ctx)
	assert.Equal(t, gate.StatusPass, r.Status)
	assert.Contains(t, r.Message, "1 PENDING")
}

func TestCaseHasPRDGate_no_prd(t *testing.T) {
	ctx, _ := newGateEnv(t)
	_, ctx.CaseID = makeCase(t, ctx.Store)
	r := gate.CaseHasPRDGate.Run(ctx)
	assert.Equal(t, gate.StatusWarn, r.Status)
	assert.Equal(t, gate.SeverityWarn, r.Severity)
}

func TestCaseHasPRDGate_with_prd(t *testing.T) {
	ctx, _ := newGateEnv(t)
	_, ctx.CaseID = makeCase(t, ctx.Store)
	ctx.Store.SetCasePRD(ctx.CaseID, `{"title":"Test PRD"}`)
	r := gate.CaseHasPRDGate.Run(ctx)
	assert.Equal(t, gate.StatusPass, r.Status)
}

func TestTopologyExistsGate_no_topology(t *testing.T) {
	ctx, _ := newGateEnv(t)
	_, ctx.CaseID = makeCase(t, ctx.Store)
	r := gate.TopologyExistsGate.Run(ctx)
	assert.Equal(t, gate.StatusFail, r.Status)
	assert.Equal(t, gate.SeverityBlock, r.Severity)
}

func TestTopologyExistsGate_with_topology(t *testing.T) {
	ctx, _ := newGateEnv(t)
	pID, caseID := makeCase(t, ctx.Store)
	ctx.CaseID = caseID
	makeTopo(t, ctx.Store, pID, "sup", "sup")
	r := gate.TopologyExistsGate.Run(ctx)
	assert.Equal(t, gate.StatusPass, r.Status)
}

func TestTopologyHasAgentsGate_no_agents(t *testing.T) {
	ctx, _ := newGateEnv(t)
	pID, caseID := makeCase(t, ctx.Store)
	ctx.CaseID = caseID
	ctx.Store.CreateSwarmTopology(pID, "sup", "memory", "langgraph")
	r := gate.TopologyHasAgentsGate.Run(ctx)
	assert.Equal(t, gate.StatusFail, r.Status)
}

func TestTopologyHasAgentsGate_with_agents(t *testing.T) {
	ctx, _ := newGateEnv(t)
	pID, caseID := makeCase(t, ctx.Store)
	ctx.CaseID = caseID
	makeTopo(t, ctx.Store, pID, "sup", "sup", "worker")
	r := gate.TopologyHasAgentsGate.Run(ctx)
	assert.Equal(t, gate.StatusPass, r.Status)
}

func TestTopologySupervisorRegisteredGate_supervisor_missing(t *testing.T) {
	ctx, _ := newGateEnv(t)
	pID, caseID := makeCase(t, ctx.Store)
	ctx.CaseID = caseID
	makeTopo(t, ctx.Store, pID, "sup", "worker")
	r := gate.TopologySupervisorRegisteredGate.Run(ctx)
	assert.Equal(t, gate.StatusFail, r.Status)
}

func TestTopologySupervisorRegisteredGate_supervisor_present(t *testing.T) {
	ctx, _ := newGateEnv(t)
	pID, caseID := makeCase(t, ctx.Store)
	ctx.CaseID = caseID
	makeTopo(t, ctx.Store, pID, "sup", "sup", "worker")
	r := gate.TopologySupervisorRegisteredGate.Run(ctx)
	assert.Equal(t, gate.StatusPass, r.Status)
}

func TestTopologyEdgesValidGate_unknown_node(t *testing.T) {
	ctx, _ := newGateEnv(t)
	pID, caseID := makeCase(t, ctx.Store)
	ctx.CaseID = caseID
	topo, _ := ctx.Store.CreateSwarmTopology(pID, "sup", "memory", "langgraph")
	ctx.Store.CreateAgentNode(topo.ID, "sup", "role", "model", nil)
	ctx.Store.CreateEdge(topo.ID, "sup", "ghost", "")
	r := gate.TopologyEdgesValidGate.Run(ctx)
	assert.Equal(t, gate.StatusFail, r.Status)
	assert.Contains(t, r.Message, "ghost")
}

func TestTopologyEdgesValidGate_all_valid(t *testing.T) {
	ctx, _ := newGateEnv(t)
	pID, caseID := makeCase(t, ctx.Store)
	ctx.CaseID = caseID
	topo, _ := ctx.Store.CreateSwarmTopology(pID, "sup", "memory", "langgraph")
	ctx.Store.CreateAgentNode(topo.ID, "sup", "r", "m", nil)
	ctx.Store.CreateAgentNode(topo.ID, "worker", "r", "m", nil)
	ctx.Store.CreateEdge(topo.ID, "sup", "worker", "")
	ctx.Store.CreateEdge(topo.ID, "worker", "END", "")
	r := gate.TopologyEdgesValidGate.Run(ctx)
	assert.Equal(t, gate.StatusPass, r.Status)
}

func TestTopologyNoOrphanAgentsGate_orphan_detected(t *testing.T) {
	ctx, _ := newGateEnv(t)
	pID, caseID := makeCase(t, ctx.Store)
	ctx.CaseID = caseID
	topo, _ := ctx.Store.CreateSwarmTopology(pID, "sup", "memory", "langgraph")
	ctx.Store.CreateAgentNode(topo.ID, "sup", "r", "m", nil)
	ctx.Store.CreateAgentNode(topo.ID, "orphan", "r", "m", nil)
	ctx.Store.CreateEdge(topo.ID, "sup", "END", "")
	r := gate.TopologyNoOrphanAgentsGate.Run(ctx)
	assert.Equal(t, gate.StatusWarn, r.Status)
	assert.Contains(t, r.Message, "orphan")
}

func TestTopologyNoOrphanAgentsGate_all_connected(t *testing.T) {
	ctx, _ := newGateEnv(t)
	pID, caseID := makeCase(t, ctx.Store)
	ctx.CaseID = caseID
	topo, _ := ctx.Store.CreateSwarmTopology(pID, "sup", "memory", "langgraph")
	ctx.Store.CreateAgentNode(topo.ID, "sup", "r", "m", nil)
	ctx.Store.CreateAgentNode(topo.ID, "worker", "r", "m", nil)
	ctx.Store.CreateEdge(topo.ID, "sup", "worker", "")
	ctx.Store.CreateEdge(topo.ID, "worker", "sup", "")
	r := gate.TopologyNoOrphanAgentsGate.Run(ctx)
	assert.Equal(t, gate.StatusPass, r.Status)
}

// ─── Security gates ───────────────────────────────────────────────────────────

func TestSecretAnthropicKeyGate_missing(t *testing.T) {
	ctx, _ := newGateEnv(t)
	_, ctx.CaseID = makeCase(t, ctx.Store)
	r := gate.SecretAnthropicKeyGate.Run(ctx)
	assert.Equal(t, gate.StatusFail, r.Status)
	assert.Equal(t, gate.SeverityBlock, r.Severity)
}

func TestSecretAnthropicKeyGate_global_present(t *testing.T) {
	ctx, _ := newGateEnv(t)
	_, ctx.CaseID = makeCase(t, ctx.Store)
	ctx.Store.CreateSecret("ANTHROPIC_API_KEY", "sk-test", nil)
	r := gate.SecretAnthropicKeyGate.Run(ctx)
	assert.Equal(t, gate.StatusPass, r.Status)
	assert.Contains(t, r.Message, "global")
}

func TestSecretAnthropicKeyGate_project_scoped(t *testing.T) {
	ctx, _ := newGateEnv(t)
	pID, caseID := makeCase(t, ctx.Store)
	ctx.CaseID = caseID
	ctx.Store.CreateSecret("ANTHROPIC_API_KEY", "sk-proj", &pID)
	r := gate.SecretAnthropicKeyGate.Run(ctx)
	assert.Equal(t, gate.StatusPass, r.Status)
	assert.Contains(t, r.Message, "project")
}

func TestSecretKeyFileGate_missing(t *testing.T) {
	ctx, wsDir := newGateEnv(t)
	_, ctx.CaseID = makeCase(t, ctx.Store)
	ctx.WsDir = wsDir
	r := gate.SecretKeyFileGate.Run(ctx)
	assert.Equal(t, gate.StatusFail, r.Status)
	assert.Equal(t, gate.SeverityBlock, r.Severity)
}

func TestSecretKeyFileGate_wrong_size(t *testing.T) {
	ctx, wsDir := newGateEnv(t)
	_, ctx.CaseID = makeCase(t, ctx.Store)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, ".key"), []byte("short"), 0o600))
	r := gate.SecretKeyFileGate.Run(ctx)
	assert.Equal(t, gate.StatusFail, r.Status)
	assert.Contains(t, r.Message, "32")
}

func TestSecretKeyFileGate_bad_permissions(t *testing.T) {
	ctx, wsDir := newGateEnv(t)
	_, ctx.CaseID = makeCase(t, ctx.Store)
	key := make([]byte, 32)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, ".key"), key, 0o644))
	r := gate.SecretKeyFileGate.Run(ctx)
	assert.Equal(t, gate.StatusWarn, r.Status, "bad permissions should warn, not block")
}

func TestSecretKeyFileGate_valid(t *testing.T) {
	ctx, wsDir := newGateEnv(t)
	_, ctx.CaseID = makeCase(t, ctx.Store)
	key := make([]byte, 32)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, ".key"), key, 0o600))
	r := gate.SecretKeyFileGate.Run(ctx)
	assert.Equal(t, gate.StatusPass, r.Status)
}

func TestSecretNoDuplicatesGate_no_duplicates(t *testing.T) {
	ctx, _ := newGateEnv(t)
	pID, caseID := makeCase(t, ctx.Store)
	ctx.CaseID = caseID
	ctx.Store.CreateSecret("KEY_A", "v", &pID)
	ctx.Store.CreateSecret("KEY_B", "v", &pID)
	r := gate.SecretNoDuplicatesGate.Run(ctx)
	assert.Equal(t, gate.StatusPass, r.Status)
}

// TestSecretNoDuplicatesGate_duplicate_warns is removed:
// the unique index on secrets(key_name, scoped_to_project_id) now enforces
// this at the DB level — the scenario is no longer reachable via the public API.

// ─── Runtime gates ────────────────────────────────────────────────────────────

func TestRuntimeScriptCompiledGate_missing(t *testing.T) {
	ctx, wsDir := newGateEnv(t)
	_, ctx.CaseID = makeCase(t, ctx.Store)
	ctx.WsDir = wsDir
	r := gate.RuntimeScriptCompiledGate.Run(ctx)
	assert.Equal(t, gate.StatusFail, r.Status)
	assert.Equal(t, gate.SeverityBlock, r.Severity)
}

func TestRuntimeScriptCompiledGate_present(t *testing.T) {
	// Script exists, no .topo sidecar (pre-feature scripts) → backward-compat pass.
	ctx, wsDir := newGateEnv(t)
	ctx.WsDir = wsDir
	_, ctx.CaseID = makeCase(t, ctx.Store)
	tmpDir := filepath.Join(wsDir, "tmp")
	require.NoError(t, os.MkdirAll(tmpDir, 0o755))
	script := filepath.Join(tmpDir, fmt.Sprintf("graph_exec_case%d.py", ctx.CaseID))
	require.NoError(t, os.WriteFile(script, []byte("# graph"), 0o600))
	r := gate.RuntimeScriptCompiledGate.Run(ctx)
	assert.Equal(t, gate.StatusPass, r.Status)
}

func TestRuntimeScriptCompiledGate_current_topology_passes(t *testing.T) {
	// Script compiled for the same topology version as the current one → pass.
	ctx, wsDir := newGateEnv(t)
	ctx.WsDir = wsDir
	pID, caseID := makeCase(t, ctx.Store)
	ctx.CaseID = caseID
	topo, _ := ctx.Store.CreateSwarmTopology(pID, "sup", "memory", "langgraph")
	tmpDir := filepath.Join(wsDir, "tmp")
	require.NoError(t, os.MkdirAll(tmpDir, 0o755))
	script := filepath.Join(tmpDir, fmt.Sprintf("graph_exec_case%d.py", caseID))
	sidecar := filepath.Join(tmpDir, fmt.Sprintf("graph_exec_case%d.topo", caseID))
	require.NoError(t, os.WriteFile(script, []byte("# graph"), 0o600))
	require.NoError(t, os.WriteFile(sidecar, []byte(fmt.Sprintf("%d", topo.Version)), 0o644))
	r := gate.RuntimeScriptCompiledGate.Run(ctx)
	assert.Equal(t, gate.StatusPass, r.Status)
}

func TestRuntimeScriptCompiledGate_stale_topology_warns(t *testing.T) {
	// Script compiled for v1 but topology is now v2 → warn.
	ctx, wsDir := newGateEnv(t)
	ctx.WsDir = wsDir
	pID, caseID := makeCase(t, ctx.Store)
	ctx.CaseID = caseID
	topoV1, _ := ctx.Store.CreateSwarmTopology(pID, "sup", "memory", "langgraph") // v1
	ctx.Store.CreateSwarmTopology(pID, "sup2", "memory", "langgraph")             // v2
	tmpDir := filepath.Join(wsDir, "tmp")
	require.NoError(t, os.MkdirAll(tmpDir, 0o755))
	script := filepath.Join(tmpDir, fmt.Sprintf("graph_exec_case%d.py", caseID))
	sidecar := filepath.Join(tmpDir, fmt.Sprintf("graph_exec_case%d.topo", caseID))
	require.NoError(t, os.WriteFile(script, []byte("# graph"), 0o600))
	require.NoError(t, os.WriteFile(sidecar, []byte(fmt.Sprintf("%d", topoV1.Version)), 0o644))
	r := gate.RuntimeScriptCompiledGate.Run(ctx)
	assert.Equal(t, gate.StatusWarn, r.Status)
	assert.Contains(t, r.Message, "--force")
}

func TestRuntimeVenvReadyGate_no_venv(t *testing.T) {
	ctx, wsDir := newGateEnv(t)
	ctx.WsDir = wsDir
	r := gate.RuntimeVenvReadyGate.Run(ctx)
	assert.Equal(t, gate.StatusFail, r.Status)
}

func TestRuntimeVenvReadyGate_venv_no_hash_warns(t *testing.T) {
	ctx, wsDir := newGateEnv(t)
	ctx.WsDir = wsDir
	require.NoError(t, os.MkdirAll(filepath.Join(wsDir, "venv", "bin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "venv", "bin", "python"), []byte(""), 0o755))
	r := gate.RuntimeVenvReadyGate.Run(ctx)
	assert.Equal(t, gate.StatusWarn, r.Status)
}

func TestRuntimeVenvReadyGate_fully_ready(t *testing.T) {
	ctx, wsDir := newGateEnv(t)
	ctx.WsDir = wsDir
	require.NoError(t, os.MkdirAll(filepath.Join(wsDir, "venv", "bin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "venv", "bin", "python"), []byte(""), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "venv", ".requirements_hash"), []byte("abc"), 0o644))
	r := gate.RuntimeVenvReadyGate.Run(ctx)
	assert.Equal(t, gate.StatusPass, r.Status)
}

func TestRuntimeSourcePathGate_no_source_path_passes(t *testing.T) {
	ctx, _ := newGateEnv(t)
	_, ctx.CaseID = makeCase(t, ctx.Store)
	r := gate.RuntimeSourcePathGate.Run(ctx)
	assert.Equal(t, gate.StatusPass, r.Status, "no source path configured is acceptable")
}

func TestRuntimeSourcePathGate_invalid_path(t *testing.T) {
	ctx, _ := newGateEnv(t)
	v, _ := ctx.Store.CreateVendor(t.Name())
	p, _ := ctx.Store.CreateProject(v.ID, "P", "/this/path/does/not/exist")
	c, _ := ctx.Store.CreateCase(p.ID)
	ctx.CaseID = c.ID
	r := gate.RuntimeSourcePathGate.Run(ctx)
	assert.Equal(t, gate.StatusFail, r.Status)
}

func TestRuntimeNoConcurrentRunGate_no_runs_passes(t *testing.T) {
	ctx, _ := newGateEnv(t)
	_, ctx.CaseID = makeCase(t, ctx.Store)
	r := gate.RuntimeNoConcurrentRunGate.Run(ctx)
	assert.Equal(t, gate.StatusPass, r.Status)
}

func TestRuntimeNoConcurrentRunGate_running_run_fails(t *testing.T) {
	ctx, _ := newGateEnv(t)
	pID, caseID := makeCase(t, ctx.Store)
	ctx.CaseID = caseID
	topo, _ := ctx.Store.CreateSwarmTopology(pID, "sup", "memory", "langgraph")
	ctx.Store.CreateRun(caseID, topo.Version, "main")
	r := gate.RuntimeNoConcurrentRunGate.Run(ctx)
	assert.Equal(t, gate.StatusFail, r.Status)
}

// ─── Dependency gates ─────────────────────────────────────────────────────────

func TestDepNoCycleGate_no_deps_passes(t *testing.T) {
	ctx, _ := newGateEnv(t)
	_, ctx.CaseID = makeCase(t, ctx.Store)
	r := gate.DepNoCycleGate.Run(ctx)
	assert.Equal(t, gate.StatusPass, r.Status)
}

func TestDepNoCycleGate_acyclic_passes(t *testing.T) {
	ctx, _ := newGateEnv(t)
	_, ctx.CaseID = makeCase(t, ctx.Store)
	v, _ := ctx.Store.CreateVendor(t.Name() + "_v2")
	p1, _ := ctx.Store.CreateProject(v.ID, "P1", "")
	p2, _ := ctx.Store.CreateProject(v.ID, "P2", "")
	ctx.Store.CreateProjectDependency(p2.ID, p1.ID)
	r := gate.DepNoCycleGate.Run(ctx)
	assert.Equal(t, gate.StatusPass, r.Status)
}

func TestDepNoCycleGate_cycle_fails(t *testing.T) {
	ctx, _ := newGateEnv(t)
	_, ctx.CaseID = makeCase(t, ctx.Store)
	v, _ := ctx.Store.CreateVendor(t.Name() + "_cv")
	p1, _ := ctx.Store.CreateProject(v.ID, "C1", "")
	p2, _ := ctx.Store.CreateProject(v.ID, "C2", "")
	ctx.Store.CreateProjectDependency(p1.ID, p2.ID)
	ctx.Store.CreateProjectDependency(p2.ID, p1.ID)
	r := gate.DepNoCycleGate.Run(ctx)
	assert.Equal(t, gate.StatusFail, r.Status)
	assert.Contains(t, r.Message, "cycle")
}

func TestDepDepsCompletedGate_no_deps_passes(t *testing.T) {
	ctx, _ := newGateEnv(t)
	_, ctx.CaseID = makeCase(t, ctx.Store)
	r := gate.DepDepsCompletedGate.Run(ctx)
	assert.Equal(t, gate.StatusPass, r.Status)
}

func TestDepDepsCompletedGate_incomplete_upstream_warns(t *testing.T) {
	// Upstream has a topology but no SUCCESS run at that version → warn.
	ctx, _ := newGateEnv(t)
	v, _ := ctx.Store.CreateVendor(t.Name())
	upstream, _ := ctx.Store.CreateProject(v.ID, "Upstream", "")
	downstream, _ := ctx.Store.CreateProject(v.ID, "Downstream", "")
	ctx.Store.CreateProjectDependency(downstream.ID, upstream.ID)
	ctx.Store.CreateSwarmTopology(upstream.ID, "sup", "memory", "langgraph") // topology exists, no runs
	c, _ := ctx.Store.CreateCase(downstream.ID)
	ctx.CaseID = c.ID
	r := gate.DepDepsCompletedGate.Run(ctx)
	assert.Equal(t, gate.StatusWarn, r.Status)
	assert.Contains(t, r.Message, "SUCCESS run")
}

func TestDepDepsCompletedGate_completed_upstream_passes(t *testing.T) {
	// Upstream has a topology AND a SUCCESS run at that topology version → pass.
	ctx, _ := newGateEnv(t)
	v, _ := ctx.Store.CreateVendor(t.Name())
	upstream, _ := ctx.Store.CreateProject(v.ID, "Up", "")
	downstream, _ := ctx.Store.CreateProject(v.ID, "Down", "")
	ctx.Store.CreateProjectDependency(downstream.ID, upstream.ID)

	topo, _ := ctx.Store.CreateSwarmTopology(upstream.ID, "sup", "memory", "langgraph")
	upCase, _ := ctx.Store.CreateCase(upstream.ID)
	run, _ := ctx.Store.CreateRun(upCase.ID, topo.Version, "main")
	ctx.Store.UpdateRunStatus(run.ID, "SUCCESS", nil, "abc123")

	c, _ := ctx.Store.CreateCase(downstream.ID)
	ctx.CaseID = c.ID
	r := gate.DepDepsCompletedGate.Run(ctx)
	assert.Equal(t, gate.StatusPass, r.Status)
}

func TestDepDepsCompletedGate_stale_topology_warns(t *testing.T) {
	// Upstream has a SUCCESS run at v1 but topology was bumped to v2 → warn.
	ctx, _ := newGateEnv(t)
	v, _ := ctx.Store.CreateVendor(t.Name())
	upstream, _ := ctx.Store.CreateProject(v.ID, "Up", "")
	downstream, _ := ctx.Store.CreateProject(v.ID, "Down", "")
	ctx.Store.CreateProjectDependency(downstream.ID, upstream.ID)

	topoV1, _ := ctx.Store.CreateSwarmTopology(upstream.ID, "sup", "memory", "langgraph") // v1
	upCase, _ := ctx.Store.CreateCase(upstream.ID)
	run, _ := ctx.Store.CreateRun(upCase.ID, topoV1.Version, "main")
	ctx.Store.UpdateRunStatus(run.ID, "SUCCESS", nil, "abc123")

	ctx.Store.CreateSwarmTopology(upstream.ID, "sup2", "memory", "langgraph") // v2 — bumps version

	c, _ := ctx.Store.CreateCase(downstream.ID)
	ctx.CaseID = c.ID
	r := gate.DepDepsCompletedGate.Run(ctx)
	assert.Equal(t, gate.StatusWarn, r.Status)
	assert.Contains(t, r.Message, "v2") // should mention the stale version
}

func TestTopologyRuntimeValidGate_valid(t *testing.T) {
	ctx, _ := newGateEnv(t)
	pID, caseID := makeCase(t, ctx.Store)
	ctx.CaseID = caseID
	ctx.Store.CreateSwarmTopology(pID, "sup", "memory", "langgraph")
	r := gate.TopologyRuntimeValidGate.Run(ctx)
	assert.Equal(t, gate.StatusPass, r.Status)
}

func TestTopologyRuntimeValidGate_invalid_runtime(t *testing.T) {
	ctx, _ := newGateEnv(t)
	pID, caseID := makeCase(t, ctx.Store)
	ctx.CaseID = caseID
	ctx.Store.CreateSwarmTopology(pID, "sup", "memory", "tensorflow")
	r := gate.TopologyRuntimeValidGate.Run(ctx)
	assert.Equal(t, gate.StatusFail, r.Status)
	assert.Contains(t, r.Message, "tensorflow")
}

// crewai and autogen are recognised future runtimes — gate must WARN (not pass,
// not block) so the operator is aware before wasting a run.
func TestTopologyRuntimeValidGate_crewai_warns(t *testing.T) {
	ctx, _ := newGateEnv(t)
	pID, caseID := makeCase(t, ctx.Store)
	ctx.CaseID = caseID
	ctx.Store.CreateSwarmTopology(pID, "sup", "memory", "crewai")
	r := gate.TopologyRuntimeValidGate.Run(ctx)
	assert.Equal(t, gate.StatusWarn, r.Status)
	assert.Contains(t, r.Message, "not yet executable")
}

func TestTopologyRuntimeValidGate_autogen_warns(t *testing.T) {
	ctx, _ := newGateEnv(t)
	pID, caseID := makeCase(t, ctx.Store)
	ctx.CaseID = caseID
	ctx.Store.CreateSwarmTopology(pID, "sup", "memory", "autogen")
	r := gate.TopologyRuntimeValidGate.Run(ctx)
	assert.Equal(t, gate.StatusWarn, r.Status)
	assert.Contains(t, r.Message, "not yet executable")
}

// ─── Soft-deleted case gate ───────────────────────────────────────────────────

func TestCaseProjectExistsGate_deleted_case_fails(t *testing.T) {
	ctx, _ := newGateEnv(t)
	_, ctx.CaseID = makeCase(t, ctx.Store)
	require.NoError(t, ctx.Store.FlagCaseDeleted(ctx.CaseID))
	r := gate.CaseProjectExistsGate.Run(ctx)
	assert.Equal(t, gate.StatusFail, r.Status)
	assert.Contains(t, r.Message, "deleted")
}

// ─── runtime.venv_broken gate ─────────────────────────────────────────────────

func TestRuntimeVenvBrokenGate_no_sentinel_passes(t *testing.T) {
	ctx, wsDir := newGateEnv(t)
	ctx.WsDir = wsDir
	r := gate.RuntimeVenvBrokenGate.Run(ctx)
	assert.Equal(t, gate.StatusPass, r.Status)
}

func TestRuntimeVenvBrokenGate_sentinel_present_fails(t *testing.T) {
	ctx, wsDir := newGateEnv(t)
	ctx.WsDir = wsDir
	require.NoError(t, os.MkdirAll(filepath.Join(wsDir, "venv"), 0o755))
	sentinel := filepath.Join(wsDir, "venv", ".requirements_hash.broken")
	require.NoError(t, os.WriteFile(sentinel, []byte("broken"), 0o644))
	r := gate.RuntimeVenvBrokenGate.Run(ctx)
	assert.Equal(t, gate.StatusFail, r.Status)
	assert.Contains(t, r.Message, "broken")
}

// ─── runtime.script_compiled — corrupt sidecar backward-compat ───────────────

func TestRuntimeScriptCompiledGate_corrupt_sidecar_passes(t *testing.T) {
	// A sidecar with non-numeric content (e.g. truncated write) must not crash
	// or block the run — the gate falls through to pass, treating the sidecar
	// as absent (same behaviour as pre-sidecar scripts).
	ctx, wsDir := newGateEnv(t)
	ctx.WsDir = wsDir
	pID, caseID := makeCase(t, ctx.Store)
	ctx.CaseID = caseID
	ctx.Store.CreateSwarmTopology(pID, "sup", "memory", "langgraph")
	tmpDir := filepath.Join(wsDir, "tmp")
	require.NoError(t, os.MkdirAll(tmpDir, 0o755))
	script := filepath.Join(tmpDir, fmt.Sprintf("graph_exec_case%d.py", caseID))
	sidecar := filepath.Join(tmpDir, fmt.Sprintf("graph_exec_case%d.topo", caseID))
	require.NoError(t, os.WriteFile(script, []byte("# graph"), 0o600))
	require.NoError(t, os.WriteFile(sidecar, []byte("not-a-number"), 0o644))
	r := gate.RuntimeScriptCompiledGate.Run(ctx)
	assert.Equal(t, gate.StatusPass, r.Status, "corrupt sidecar must fall through to pass")
}

// ─── deps.deps_completed — upstream has no topology ───────────────────────────

func TestDepDepsCompletedGate_upstream_no_topology_warns(t *testing.T) {
	// Upstream project exists and is a declared dependency but has never had a
	// topology registered — the gate should warn, not panic.
	ctx, _ := newGateEnv(t)
	v, _ := ctx.Store.CreateVendor(t.Name())
	upstream, _ := ctx.Store.CreateProject(v.ID, "Up", "") // no topology
	downstream, _ := ctx.Store.CreateProject(v.ID, "Down", "")
	ctx.Store.CreateProjectDependency(downstream.ID, upstream.ID)
	c, _ := ctx.Store.CreateCase(downstream.ID)
	ctx.CaseID = c.ID
	r := gate.DepDepsCompletedGate.Run(ctx)
	assert.Equal(t, gate.StatusWarn, r.Status)
	assert.Contains(t, r.Message, "no topology")
}

// ─── runtime.git_available gate ───────────────────────────────────────────────

func TestRuntimeGitAvailableGate_no_source_path_passes(t *testing.T) {
	ctx, _ := newGateEnv(t)
	_, ctx.CaseID = makeCase(t, ctx.Store) // project has empty source_path
	r := gate.RuntimeGitAvailableGate.Run(ctx)
	assert.Equal(t, gate.StatusPass, r.Status)
}

func TestRuntimeGitAvailableGate_with_source_path_and_git_present(t *testing.T) {
	ctx, _ := newGateEnv(t)
	v, _ := ctx.Store.CreateVendor(t.Name())
	p, _ := ctx.Store.CreateProject(v.ID, "P", t.TempDir())
	c, _ := ctx.Store.CreateCase(p.ID)
	ctx.CaseID = c.ID
	r := gate.RuntimeGitAvailableGate.Run(ctx)
	// git is available in the test environment; expect pass.
	assert.Equal(t, gate.StatusPass, r.Status)
}
