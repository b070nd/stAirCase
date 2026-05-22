package persistence_test

// store_coverage_test.go — tests for store methods that had 0% coverage.
// Each test is minimal: it exercises the code path and asserts the key
// invariant. Happy-path focus; constraint violations are tested in store_test.go.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/b070nd/staircase-core/src/internal/crypto"
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

// ─── Project config ───────────────────────────────────────────────────────────

func TestGetSetProjectConfig_defaults_and_update(t *testing.T) {
	s := newTestStore(t)
	_, projectID, _ := scaffoldTopology(t, s)

	// Defaults: empty model, 0 budget.
	model, budget, err := s.GetProjectConfig(projectID)
	require.NoError(t, err)
	assert.Equal(t, "", model)
	assert.Equal(t, float64(0), budget)

	// Update model.
	require.NoError(t, s.SetProjectDefaultModel(projectID, "claude-opus-4"))
	model, _, err = s.GetProjectConfig(projectID)
	require.NoError(t, err)
	assert.Equal(t, "claude-opus-4", model)

	// Update budget.
	require.NoError(t, s.SetProjectBudgetCap(projectID, 9.99))
	_, budget, err = s.GetProjectConfig(projectID)
	require.NoError(t, err)
	assert.InDelta(t, 9.99, budget, 0.001)
}

// ─── ListAllSecrets ───────────────────────────────────────────────────────────

func TestListAllSecrets_returns_all_secrets(t *testing.T) {
	s := newTestStore(t)
	_, projectID, _ := scaffoldTopology(t, s)

	_, err := s.CreateSecret("KEY_A", "val-a", nil)
	require.NoError(t, err)
	_, err2 := s.CreateSecret("KEY_B", "val-b", &projectID)
	require.NoError(t, err2)

	secrets, err := s.ListAllSecrets()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(secrets), 2, "ListAllSecrets must return all secrets across projects")
}

// ─── RotationLock (CHECK 4.3.3) ──────────────────────────────────────────────

func TestNewRotationLock_acquires_and_unlocks(t *testing.T) {
	wsDir := t.TempDir()
	lock, err := persistence.NewRotationLock(wsDir)
	require.NoError(t, err, "NewRotationLock must succeed on a fresh workspace")
	require.NotNil(t, lock)
	lock.Unlock() // must not panic or error
}

func TestNewRotationLock_double_acquire_fails(t *testing.T) {
	wsDir := t.TempDir()
	lock1, err := persistence.NewRotationLock(wsDir)
	require.NoError(t, err)
	defer lock1.Unlock()

	// Second acquire must fail while lock1 holds the flock.
	_, err = persistence.NewRotationLock(wsDir)
	assert.Error(t, err, "second NewRotationLock on same dir must fail")
	assert.Contains(t, err.Error(), "rotation already in progress")
}

// ─── RotateSecrets ─────────────────────────────────────────────────────────────

// TestRotateSecrets_with_identity_funcs verifies that RotateSecrets walks
// every secret, calls decrypt/encrypt, and commits the transaction.
func TestRotateSecrets_with_identity_funcs(t *testing.T) {
	s := newTestStore(t)
	// Create two secrets with plaintext "encrypted" values (identity scheme).
	_, err := s.CreateSecret("K1", "plaintext1", nil)
	require.NoError(t, err)
	_, err = s.CreateSecret("K2", "plaintext2", nil)
	require.NoError(t, err)

	// identity decrypt: returns the "ciphertext" as-is.
	decrypt := func(_ []byte, ct string) (string, error) { return ct, nil }
	// identity encrypt: returns the plaintext as-is (prefixed to show it ran).
	encrypt := func(_ []byte, pt string) (string, error) { return "rotated:" + pt, nil }

	require.NoError(t, s.RotateSecrets([]byte("oldkey"), []byte("newkey"), decrypt, encrypt))

	// Verify both secrets were updated.
	secrets, err := s.ListAllSecrets()
	require.NoError(t, err)
	for _, sec := range secrets {
		assert.True(t,
			sec.EncryptedValue == "rotated:plaintext1" || sec.EncryptedValue == "rotated:plaintext2",
			"EncryptedValue should be prefixed by identity encrypt, got %q", sec.EncryptedValue,
		)
	}
}

// TestRotateSecrets_decrypt_error_aborts verifies that a decrypt failure
// causes RotateSecrets to return an error (and implicitly rolls back the tx).
func TestRotateSecrets_decrypt_error_aborts(t *testing.T) {
	s := newTestStore(t)
	_, err := s.CreateSecret("K1", "plaintext1", nil)
	require.NoError(t, err)

	decryptErr := fmt.Errorf("bad key")
	decrypt := func(_ []byte, _ string) (string, error) { return "", decryptErr }
	encrypt := func(_ []byte, pt string) (string, error) { return pt, nil }

	err = s.RotateSecrets([]byte("old"), []byte("new"), decrypt, encrypt)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rotate: decrypt")
}

// ─── HasSuccessfulRunAtTopologyVersion ───────────────────────────────────────

func TestHasSuccessfulRunAtTopologyVersion_false_when_no_runs(t *testing.T) {
	s := newTestStore(t)
	_, projectID, _ := scaffoldTopology(t, s)

	has, err := s.HasSuccessfulRunAtTopologyVersion(projectID, 1)
	require.NoError(t, err)
	assert.False(t, has)
}

func TestHasSuccessfulRunAtTopologyVersion_true_after_success(t *testing.T) {
	s := newTestStore(t)
	_, projectID, _ := scaffoldTopology(t, s)
	c, err := s.CreateCase(projectID)
	require.NoError(t, err)

	run, err := s.CreateRun(c.ID, 1, "main")
	require.NoError(t, err)
	now := time.Now()
	require.NoError(t, s.UpdateRunStatus(run.ID, "SUCCESS", &now, "abc"))

	has, err := s.HasSuccessfulRunAtTopologyVersion(projectID, 1)
	require.NoError(t, err)
	assert.True(t, has)
}

// ─── DeleteFlaggedCases / PruneEventLogs ──────────────────────────────────────

func TestDeleteFlaggedCases_removes_deleted_cases(t *testing.T) {
	s := newTestStore(t)
	_, projectID, _ := scaffoldTopology(t, s)
	c, err := s.CreateCase(projectID)
	require.NoError(t, err)

	// Flag the case as deleted.
	require.NoError(t, s.FlagCaseDeleted(c.ID))

	n, err := s.DeleteFlaggedCases()
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
}

func TestPruneEventLogs_keeps_rows_within_limit(t *testing.T) {
	s := newTestStore(t)
	_, projectID, _ := scaffoldTopology(t, s)
	c, err := s.CreateCase(projectID)
	require.NoError(t, err)
	run, err := s.CreateRun(c.ID, 1, "main")
	require.NoError(t, err)

	// Append three event log entries.
	for i := 0; i < 3; i++ {
		_, err = s.AppendEventLog(run.ID, "test_event", `{"k":"v"}`, "", "")
		require.NoError(t, err)
	}

	// Keep at most 2 rows → 1 should be pruned.
	n, err := s.PruneEventLogs(2)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
}

// ─── RotateSecretsOnConn ──────────────────────────────────────────────────────

// TestRotateSecretsOnConn_happy_path verifies that RotateSecretsOnConn
// re-encrypts all secrets on the provided pinned connection and that the
// new ciphertexts are readable with the new key (and not with the old key).
// This test also exercises the synchronous=FULL pattern that the rotate
// command uses to guarantee power-loss durability of the DB commit.
func TestRotateSecretsOnConn_happy_path(t *testing.T) {
	db, err := persistence.InitDB(t.TempDir())
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	s := persistence.NewStore(db)

	oldKey := make([]byte, 32)
	newKey := make([]byte, 32)
	for i := range newKey {
		newKey[i] = 0xBB
	}

	// Store a secret encrypted under oldKey.
	oldEnc, err := crypto.Encrypt(oldKey, "super-secret")
	require.NoError(t, err)
	_, err = s.CreateSecret("CONN_TEST_KEY", oldEnc, nil)
	require.NoError(t, err)

	// Obtain a pinned connection, set synchronous=FULL, then rotate.
	conn, err := db.Conn(context.Background())
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	_, err = conn.ExecContext(context.Background(), "PRAGMA synchronous=FULL")
	require.NoError(t, err)

	err = s.RotateSecretsOnConn(context.Background(), conn, oldKey, newKey, crypto.Decrypt, crypto.Encrypt)
	require.NoError(t, err)

	// New ciphertext must decrypt with newKey.
	secrets, err := s.ListAllSecrets()
	require.NoError(t, err)
	require.Len(t, secrets, 1)
	plaintext, err := crypto.Decrypt(newKey, secrets[0].EncryptedValue)
	require.NoError(t, err)
	assert.Equal(t, "super-secret", plaintext)

	// Old key must no longer decrypt the updated ciphertext.
	_, err = crypto.Decrypt(oldKey, secrets[0].EncryptedValue)
	assert.Error(t, err, "old key must not decrypt after rotation")
}
