package persistence_test

// store_coverage_test.go — tests for store methods that had 0% coverage.
// Each test is minimal: it exercises the code path and asserts the key
// invariant. Happy-path focus; constraint violations are tested in store_test.go.

import (
	"testing"
	"time"

	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// scaffoldTopology creates a vendor→project→topology and returns all IDs.
func scaffoldTopology(t *testing.T, s *persistence.Store) (vendorID, projectID, topoID int64) {
	t.Helper()
	v, err := s.CreateVendor("TV")
	require.NoError(t, err)
	p, err := s.CreateProject(v.ID, "TP", "")
	require.NoError(t, err)
	topo, err := s.CreateSwarmTopology(p.ID, "sup", "memory", "langgraph")
	require.NoError(t, err)
	return v.ID, p.ID, topo.ID
}

// ─── Vendor ───────────────────────────────────────────────────────────────────

func TestGetVendorByID_found_and_not_found(t *testing.T) {
	s := newTestStore(t)
	v, err := s.CreateVendor("VendorX")
	require.NoError(t, err)

	got, err := s.GetVendorByID(v.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "VendorX", got.Name)

	missing, err := s.GetVendorByID(99999)
	require.NoError(t, err)
	assert.Nil(t, missing)
}

func TestListVendors_returns_all(t *testing.T) {
	s := newTestStore(t)
	_, err := s.CreateVendor("Alpha")
	require.NoError(t, err)
	_, err = s.CreateVendor("Beta")
	require.NoError(t, err)

	vs, err := s.ListVendors()
	require.NoError(t, err)
	assert.Len(t, vs, 2)
	assert.Equal(t, "Alpha", vs[0].Name) // ordered by name
	assert.Equal(t, "Beta", vs[1].Name)
}

func TestListVendors_empty(t *testing.T) {
	s := newTestStore(t)
	vs, err := s.ListVendors()
	require.NoError(t, err)
	assert.Empty(t, vs)
}

// ─── Project ──────────────────────────────────────────────────────────────────

func TestGetProjectByVendorAndName_found_and_not_found(t *testing.T) {
	s := newTestStore(t)
	v, _ := s.CreateVendor("V")
	p, _ := s.CreateProject(v.ID, "Proj", "/src")

	got, err := s.GetProjectByVendorAndName(v.ID, "Proj")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, p.ID, got.ID)

	missing, err := s.GetProjectByVendorAndName(v.ID, "NoExist")
	require.NoError(t, err)
	assert.Nil(t, missing)
}

func TestListProjectsByVendor_returns_vendor_projects(t *testing.T) {
	s := newTestStore(t)
	v1, _ := s.CreateVendor("V1")
	v2, _ := s.CreateVendor("V2")
	s.CreateProject(v1.ID, "P1", "")
	s.CreateProject(v1.ID, "P2", "")
	s.CreateProject(v2.ID, "P3", "")

	ps, err := s.ListProjectsByVendor(v1.ID)
	require.NoError(t, err)
	assert.Len(t, ps, 2)
}

func TestListAllProjects_returns_all(t *testing.T) {
	s := newTestStore(t)
	v, _ := s.CreateVendor("V")
	s.CreateProject(v.ID, "A", "")
	s.CreateProject(v.ID, "B", "")

	all, err := s.ListAllProjects()
	require.NoError(t, err)
	assert.Len(t, all, 2)
}

// Note: GetProjectConfig / SetProjectDefaultModel / SetProjectBudgetCap require
// the default_model and budget_usd_per_run columns, which are added by migrations
// 181/182 but absent from the base DDL. Fresh test DBs don't have them, so
// these methods cannot be tested without applying those specific ALTER TABLEs.
// Covered via e2e tests that use a fully-migrated workspace.

// ─── ProjectDependency ────────────────────────────────────────────────────────

func TestCreateAndListProjectDependencies(t *testing.T) {
	s := newTestStore(t)
	v, _ := s.CreateVendor("V")
	src, _ := s.CreateProject(v.ID, "Src", "")
	tgt, _ := s.CreateProject(v.ID, "Tgt", "")

	dep, err := s.CreateProjectDependency(src.ID, tgt.ID)
	require.NoError(t, err)
	assert.Equal(t, src.ID, dep.SourceProjectID)
	assert.Equal(t, tgt.ID, dep.TargetProjectID)

	deps, err := s.ListProjectDependencies(src.ID)
	require.NoError(t, err)
	require.Len(t, deps, 1)
	assert.Equal(t, tgt.ID, deps[0].TargetProjectID)

	// ListProjectDependencies of target should be empty (no outgoing from tgt).
	depsTgt, err := s.ListProjectDependencies(tgt.ID)
	require.NoError(t, err)
	assert.Empty(t, depsTgt)
}

func TestListAllProjectDependencies(t *testing.T) {
	s := newTestStore(t)
	v, _ := s.CreateVendor("V")
	a, _ := s.CreateProject(v.ID, "A", "")
	b, _ := s.CreateProject(v.ID, "B", "")
	c, _ := s.CreateProject(v.ID, "C", "")
	s.CreateProjectDependency(a.ID, b.ID)
	s.CreateProjectDependency(b.ID, c.ID)

	all, err := s.ListAllProjectDependencies()
	require.NoError(t, err)
	assert.Len(t, all, 2)
}

// ─── Case ─────────────────────────────────────────────────────────────────────

func TestSetCasePRD_and_UpdateCasePRD(t *testing.T) {
	s := newTestStore(t)
	_, _, caseID := scaffold(t, s)

	require.NoError(t, s.SetCasePRD(caseID, `{"title":"v1"}`))

	c, err := s.GetCase(caseID)
	require.NoError(t, err)
	assert.Equal(t, `{"title":"v1"}`, c.PrdJSON)

	// UpdateCasePRD is an alias.
	require.NoError(t, s.UpdateCasePRD(caseID, `{"title":"v2"}`))
	c, err = s.GetCase(caseID)
	require.NoError(t, err)
	assert.Equal(t, `{"title":"v2"}`, c.PrdJSON)
}

func TestListCasesByProject(t *testing.T) {
	s := newTestStore(t)
	v, _ := s.CreateVendor("V")
	p, _ := s.CreateProject(v.ID, "P", "")
	s.CreateCase(p.ID)
	s.CreateCase(p.ID)

	cases, err := s.ListCasesByProject(p.ID)
	require.NoError(t, err)
	assert.Len(t, cases, 2)
}

func TestListCasesByProject_includes_soft_deleted(t *testing.T) {
	s := newTestStore(t)
	v, _ := s.CreateVendor("V")
	p, _ := s.CreateProject(v.ID, "P", "")
	c, _ := s.CreateCase(p.ID)
	s.FlagCaseDeleted(c.ID)

	cases, err := s.ListCasesByProject(p.ID)
	require.NoError(t, err)
	require.Len(t, cases, 1)
	assert.NotNil(t, cases[0].DeletedAt)
}

// ─── SwarmTopology ────────────────────────────────────────────────────────────

func TestGetTopology_found_and_not_found(t *testing.T) {
	s := newTestStore(t)
	_, _, topoID := scaffoldTopology(t, s)

	got, err := s.GetTopology(topoID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, topoID, got.ID)

	missing, err := s.GetTopology(99999)
	require.NoError(t, err)
	assert.Nil(t, missing)
}

func TestListTopologiesByProject(t *testing.T) {
	s := newTestStore(t)
	v, _ := s.CreateVendor("V")
	p, _ := s.CreateProject(v.ID, "P", "")
	s.CreateSwarmTopology(p.ID, "sup", "memory", "langgraph")
	s.CreateSwarmTopology(p.ID, "sup", "redis", "langgraph")

	topos, err := s.ListTopologiesByProject(p.ID)
	require.NoError(t, err)
	assert.Len(t, topos, 2)
	// Returns newest version first.
	assert.Equal(t, 2, topos[0].Version)
	assert.Equal(t, 1, topos[1].Version)
}

// ─── AgentNode ────────────────────────────────────────────────────────────────

func TestCreateAgentNode_GetAgentNode_ListAgentNodes(t *testing.T) {
	s := newTestStore(t)
	_, _, topoID := scaffoldTopology(t, s)

	node, err := s.CreateAgentNode(topoID, "worker", "implements stories", "claude-sonnet-4-5", nil)
	require.NoError(t, err)
	assert.Equal(t, "worker", node.Name)

	got, err := s.GetAgentNode(node.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "worker", got.Name)

	missing, err := s.GetAgentNode(99999)
	require.NoError(t, err)
	assert.Nil(t, missing)

	nodes, err := s.ListAgentNodes(topoID)
	require.NoError(t, err)
	assert.Len(t, nodes, 1)
}

// ─── AgentTool ────────────────────────────────────────────────────────────────

func TestCreateAgentTool_ListAgentTools(t *testing.T) {
	s := newTestStore(t)
	_, _, topoID := scaffoldTopology(t, s)
	node, _ := s.CreateAgentNode(topoID, "worker", "role", "model", nil)

	tool, err := s.CreateAgentTool(node.ID, "read_file", `{"max_bytes":4096}`)
	require.NoError(t, err)
	assert.Equal(t, "read_file", tool.ToolName)

	tools, err := s.ListAgentTools(node.ID)
	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, `{"max_bytes":4096}`, tools[0].ToolConfig)
}

func TestListAgentTools_empty(t *testing.T) {
	s := newTestStore(t)
	_, _, topoID := scaffoldTopology(t, s)
	node, _ := s.CreateAgentNode(topoID, "sup", "role", "model", nil)

	tools, err := s.ListAgentTools(node.ID)
	require.NoError(t, err)
	assert.Empty(t, tools)
}

// ─── Edge ─────────────────────────────────────────────────────────────────────

func TestCreateEdge_ListEdges(t *testing.T) {
	s := newTestStore(t)
	_, _, topoID := scaffoldTopology(t, s)

	edge, err := s.CreateEdge(topoID, "supervisor", "worker", "")
	require.NoError(t, err)
	assert.Equal(t, "supervisor", edge.FromNode)
	assert.Equal(t, "worker", edge.ToNode)

	// Second edge with a condition.
	_, err = s.CreateEdge(topoID, "worker", "END", "done")
	require.NoError(t, err)

	edges, err := s.ListEdges(topoID)
	require.NoError(t, err)
	assert.Len(t, edges, 2)
}

// ─── Run ──────────────────────────────────────────────────────────────────────

func TestListRunsByCase(t *testing.T) {
	s := newTestStore(t)
	_, _, topoID := scaffoldTopology(t, s)

	// Need case_id — get it from topology's project.
	topo, _ := s.GetTopology(topoID)
	c, _ := s.CreateCase(topo.ProjectID)

	run1, _ := s.CreateRun(c.ID, 1, "staircase/run-1")
	run2, _ := s.CreateRun(c.ID, 1, "staircase/run-2")

	runs, err := s.ListRunsByCase(c.ID)
	require.NoError(t, err)
	assert.Len(t, runs, 2)
	// Returned in DESC order by id.
	assert.Equal(t, run2.ID, runs[0].ID)
	assert.Equal(t, run1.ID, runs[1].ID)
}

func TestListRunsByStatus(t *testing.T) {
	s := newTestStore(t)
	_, _, topoID := scaffoldTopology(t, s)
	topo, _ := s.GetTopology(topoID)
	c, _ := s.CreateCase(topo.ProjectID)

	run, _ := s.CreateRun(c.ID, 1, "main")
	now := time.Now()
	s.UpdateRunStatus(run.ID, "SUCCESS", &now, "abc123")

	running, err := s.ListRunsByStatus(string(persistence.RunStatusRunning))
	require.NoError(t, err)
	assert.Empty(t, running)

	succeeded, err := s.ListRunsByStatus("SUCCESS")
	require.NoError(t, err)
	require.Len(t, succeeded, 1)
	assert.Equal(t, run.ID, succeeded[0].ID)
}
