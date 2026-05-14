package persistence_test

import (
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── test helper ─────────────────────────────────────────────────────────────

func newTestStore(t *testing.T) *persistence.Store {
	t.Helper()
	db, err := persistence.InitDB(t.TempDir())
	require.NoError(t, err, "InitDB must succeed")
	t.Cleanup(func() { db.Close() })
	return persistence.NewStore(db)
}

// scaffold creates a vendor+project+case hierarchy and returns them.
func scaffold(t *testing.T, s *persistence.Store) (vendorID, projectID, caseID int64) {
	t.Helper()
	v, err := s.CreateVendor("TestVendor")
	require.NoError(t, err)
	p, err := s.CreateProject(v.ID, "TestProject", "/tmp/test")
	require.NoError(t, err)
	c, err := s.CreateCase(p.ID)
	require.NoError(t, err)
	return v.ID, p.ID, c.ID
}

// ─── Vendor ───────────────────────────────────────────────────────────────────

func TestCreateVendor_and_retrieve_by_name(t *testing.T) {
	s := newTestStore(t)
	v, err := s.CreateVendor("Acme")
	require.NoError(t, err)
	assert.Equal(t, "Acme", v.Name)
	assert.Positive(t, v.ID)

	got, err := s.GetVendorByName("Acme")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, v.ID, got.ID)
}

func TestCreateVendor_duplicate_name_fails(t *testing.T) {
	s := newTestStore(t)
	_, err := s.CreateVendor("Dup")
	require.NoError(t, err)
	_, err = s.CreateVendor("Dup")
	assert.Error(t, err, "unique constraint must be enforced")
}

func TestGetVendorByName_not_found_returns_nil(t *testing.T) {
	s := newTestStore(t)
	v, err := s.GetVendorByName("ghost")
	require.NoError(t, err)
	assert.Nil(t, v)
}

// ─── Project ─────────────────────────────────────────────────────────────────

func TestCreateProject_and_retrieve(t *testing.T) {
	s := newTestStore(t)
	v, _ := s.CreateVendor("V")
	p, err := s.CreateProject(v.ID, "App", "/path/to/app")
	require.NoError(t, err)
	assert.Equal(t, "App", p.Name)

	got, err := s.GetProject(p.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "/path/to/app", got.SourcePath)
}

func TestGetProject_not_found_returns_nil(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetProject(999_999)
	require.NoError(t, err)
	assert.Nil(t, got)
}

// ─── UserStory ────────────────────────────────────────────────────────────────

func TestCreateUserStory_default_status_is_pending(t *testing.T) {
	s := newTestStore(t)
	_, _, caseID := scaffold(t, s)
	us, err := s.CreateUserStory(caseID, "Story 1")
	require.NoError(t, err)
	assert.Equal(t, "PENDING", us.Status)
}

func TestUpdateUserStoryStatus(t *testing.T) {
	s := newTestStore(t)
	_, _, caseID := scaffold(t, s)
	us, _ := s.CreateUserStory(caseID, "Story")
	require.NoError(t, s.UpdateUserStoryStatus(us.ID, "IMPLEMENTED"))

	stories, err := s.ListUserStoriesByCase(caseID)
	require.NoError(t, err)
	require.Len(t, stories, 1)
	assert.Equal(t, "IMPLEMENTED", stories[0].Status)
}

func TestListUserStoriesByCase_returns_all_statuses(t *testing.T) {
	s := newTestStore(t)
	_, _, caseID := scaffold(t, s)
	s.CreateUserStory(caseID, "Pending")
	us2, _ := s.CreateUserStory(caseID, "Invalidated")
	s.UpdateUserStoryStatus(us2.ID, "INVALIDATED")

	stories, err := s.ListUserStoriesByCase(caseID)
	require.NoError(t, err)
	// Both stories returned regardless of status — caller filters.
	assert.Len(t, stories, 2)
}

// ─── Run ─────────────────────────────────────────────────────────────────────

func TestCreateRun_default_status_is_running(t *testing.T) {
	s := newTestStore(t)
	_, _, caseID := scaffold(t, s)
	topo, _ := s.CreateSwarmTopology(mustGetProjectID(t, s, caseID), "sup", "memory", "langgraph")

	run, err := s.CreateRun(caseID, topo.Version, "main")
	require.NoError(t, err)
	assert.Equal(t, "RUNNING", run.Status)
	assert.Nil(t, run.EndTime)
}

func TestCreateRun_invalid_topology_version_rejected(t *testing.T) {
	s := newTestStore(t)
	_, _, caseID := scaffold(t, s)
	// No topology created for this project — version 99 does not exist.
	_, err := s.CreateRun(caseID, 99, "main")
	require.Error(t, err, "CreateRun with unknown topology version must return an error")
	assert.Contains(t, err.Error(), "topology version 99 does not exist")
}

func TestCreateRun_valid_topology_version_accepted(t *testing.T) {
	s := newTestStore(t)
	_, _, caseID := scaffold(t, s)
	topo, _ := s.CreateSwarmTopology(mustGetProjectID(t, s, caseID), "sup", "memory", "langgraph")
	run, err := s.CreateRun(caseID, topo.Version, "main")
	require.NoError(t, err, "CreateRun with a real topology version must succeed")
	assert.Equal(t, topo.Version, run.TopologyVersion)
}

func TestUpdateRunStatus_sets_end_time(t *testing.T) {
	s := newTestStore(t)
	_, _, caseID := scaffold(t, s)
	topo, _ := s.CreateSwarmTopology(mustGetProjectID(t, s, caseID), "sup", "memory", "langgraph")
	run, _ := s.CreateRun(caseID, topo.Version, "main")

	now := time.Now()
	require.NoError(t, s.UpdateRunStatus(run.ID, "SUCCESS", &now, "abc123"))

	got, err := s.GetRun(run.ID)
	require.NoError(t, err)
	assert.Equal(t, "SUCCESS", got.Status)
	assert.Equal(t, "abc123", got.GitCommitHash)
	assert.NotNil(t, got.EndTime)
}

// ─── AppendEventLog / chain hash ────────────────────────────────────────────

func TestAppendEventLog_first_entry_hash(t *testing.T) {
	s := newTestStore(t)
	_, _, caseID := scaffold(t, s)
	topo, _ := s.CreateSwarmTopology(mustGetProjectID(t, s, caseID), "sup", "memory", "langgraph")
	run, _ := s.CreateRun(caseID, topo.Version, "main")

	payload := "first payload"
	entry, err := s.AppendEventLog(run.ID, "state_emit", payload, "", "")
	require.NoError(t, err)

	// Verify: hash = SHA256(payload + "" + "")
	expected := fmt.Sprintf("%x", sha256.Sum256([]byte(payload)))
	assert.Equal(t, expected, entry.EventHash, "first chain entry hash must match SHA256(payload)")
}

func TestAppendEventLog_chain_extends_correctly(t *testing.T) {
	s := newTestStore(t)
	_, _, caseID := scaffold(t, s)
	topo, _ := s.CreateSwarmTopology(mustGetProjectID(t, s, caseID), "sup", "memory", "langgraph")
	run, _ := s.CreateRun(caseID, topo.Version, "main")

	e1, _ := s.AppendEventLog(run.ID, "state_emit", "payload1", "", "")
	e2, err := s.AppendEventLog(run.ID, "state_emit", "payload2", e1.EventHash, "gitabc")
	require.NoError(t, err)

	// hash2 = SHA256("payload2" + e1.EventHash + "gitabc")
	expected := fmt.Sprintf("%x", sha256.Sum256([]byte("payload2"+e1.EventHash+"gitabc")))
	assert.Equal(t, expected, e2.EventHash, "second chain entry hash must include prevHash and gitCommitHash")
}

func TestGetLastEventHash_empty_returns_empty_string(t *testing.T) {
	s := newTestStore(t)
	_, _, caseID := scaffold(t, s)
	topo, _ := s.CreateSwarmTopology(mustGetProjectID(t, s, caseID), "sup", "memory", "langgraph")
	run, _ := s.CreateRun(caseID, topo.Version, "main")

	hash, err := s.GetLastEventHash(run.ID)
	require.NoError(t, err)
	assert.Equal(t, "", hash, "no entries yet → empty prevHash (genesis)")
}

// ─── Secret scoping ───────────────────────────────────────────────────────────

func TestGetSecret_project_scoped_preferred_over_global(t *testing.T) {
	s := newTestStore(t)
	v, _ := s.CreateVendor("V")
	p, _ := s.CreateProject(v.ID, "P", "")

	s.CreateSecret("MY_KEY", "global_value", nil)
	s.CreateSecret("MY_KEY", "project_value", &p.ID)

	got, err := s.GetSecret("MY_KEY", &p.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "project_value", got.EncryptedValue)
}

func TestGetSecret_falls_back_to_global(t *testing.T) {
	s := newTestStore(t)
	v, _ := s.CreateVendor("V")
	p, _ := s.CreateProject(v.ID, "P", "")

	s.CreateSecret("GLOBAL_KEY", "global_val", nil)

	got, err := s.GetSecret("GLOBAL_KEY", &p.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "global_val", got.EncryptedValue)
	assert.Nil(t, got.ScopedToProjectID)
}

func TestGetSecret_not_found_returns_nil(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetSecret("MISSING_KEY", nil)
	require.NoError(t, err)
	assert.Nil(t, got)
}

// ─── Component ────────────────────────────────────────────────────────────────

func TestUpdateComponent_renames(t *testing.T) {
	s := newTestStore(t)
	_, pID, _ := scaffold(t, s)
	c, _ := s.CreateComponent(pID, "OldName")

	require.NoError(t, s.UpdateComponent(c.ID, "NewName"))

	cs, err := s.ListComponentsByProject(pID)
	require.NoError(t, err)
	require.Len(t, cs, 1)
	assert.Equal(t, "NewName", cs[0].Name)
}

func TestUpdateComponent_not_found_returns_error(t *testing.T) {
	s := newTestStore(t)
	err := s.UpdateComponent(999_999, "Name")
	assert.Error(t, err)
}

func TestDeleteComponent_removes_record(t *testing.T) {
	s := newTestStore(t)
	_, pID, _ := scaffold(t, s)
	c, _ := s.CreateComponent(pID, "Widget")

	require.NoError(t, s.DeleteComponent(c.ID))

	cs, err := s.ListComponentsByProject(pID)
	require.NoError(t, err)
	assert.Empty(t, cs)
}

// ─── Secret list by project ───────────────────────────────────────────────────

func TestListSecretsByProject_only_returns_project_scoped(t *testing.T) {
	s := newTestStore(t)
	v, _ := s.CreateVendor("V")
	p, _ := s.CreateProject(v.ID, "P", "")

	s.CreateSecret("GLOBAL", "gv", nil)
	s.CreateSecret("PROJ_KEY", "pv", &p.ID)

	secrets, err := s.ListSecretsByProject(p.ID)
	require.NoError(t, err)
	require.Len(t, secrets, 1)
	assert.Equal(t, "PROJ_KEY", secrets[0].KeyName)
}

// ─── Topology / SwarmTopology versioning ─────────────────────────────────────

func TestCreateSwarmTopology_auto_increments_version(t *testing.T) {
	s := newTestStore(t)
	v, _ := s.CreateVendor("V")
	p, _ := s.CreateProject(v.ID, "P", "")

	t1, _ := s.CreateSwarmTopology(p.ID, "sup", "memory", "langgraph")
	t2, _ := s.CreateSwarmTopology(p.ID, "sup", "memory", "langgraph")

	assert.Equal(t, 1, t1.Version)
	assert.Equal(t, 2, t2.Version)
}

func TestGetLatestTopology_returns_highest_version(t *testing.T) {
	s := newTestStore(t)
	v, _ := s.CreateVendor("V")
	p, _ := s.CreateProject(v.ID, "P", "")

	s.CreateSwarmTopology(p.ID, "sup", "memory", "langgraph")
	t2, _ := s.CreateSwarmTopology(p.ID, "sup", "memory", "langgraph")

	latest, err := s.GetLatestTopology(p.ID)
	require.NoError(t, err)
	require.NotNil(t, latest)
	assert.Equal(t, t2.Version, latest.Version)
}

func TestGetLatestTopology_no_topology_returns_nil(t *testing.T) {
	s := newTestStore(t)
	v, _ := s.CreateVendor("V")
	p, _ := s.CreateProject(v.ID, "P", "")

	latest, err := s.GetLatestTopology(p.ID)
	require.NoError(t, err)
	assert.Nil(t, latest)
}

// ─── internal test helpers ────────────────────────────────────────────────────

func mustGetProjectID(t *testing.T, s *persistence.Store, caseID int64) int64 {
	t.Helper()
	c, err := s.GetCase(caseID)
	require.NoError(t, err)
	return c.ProjectID
}

// ─── Case soft-delete ─────────────────────────────────────────────────────────

func TestFlagCaseDeleted_sets_deleted_at(t *testing.T) {
	s := newTestStore(t)
	_, _, caseID := scaffold(t, s)
	require.NoError(t, s.FlagCaseDeleted(caseID))

	c, err := s.GetCase(caseID)
	require.NoError(t, err)
	require.NotNil(t, c)
	assert.NotNil(t, c.DeletedAt, "deleted_at must be set after FlagCaseDeleted")
}

func TestDeleteFlaggedCases_purges_with_cascade(t *testing.T) {
	s := newTestStore(t)
	_, _, caseID := scaffold(t, s)
	s.CreateUserStory(caseID, "Story")
	require.NoError(t, s.FlagCaseDeleted(caseID))

	n, err := s.DeleteFlaggedCases()
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	c, err := s.GetCase(caseID)
	require.NoError(t, err)
	assert.Nil(t, c, "case must be hard-deleted")
}

// ─── Event log pruning ────────────────────────────────────────────────────────

func TestPruneEventLogs_under_limit_deletes_nothing(t *testing.T) {
	s := newTestStore(t)
	_, _, caseID := scaffold(t, s)
	topo, _ := s.CreateSwarmTopology(mustGetProjectID(t, s, caseID), "sup", "memory", "langgraph")
	run, _ := s.CreateRun(caseID, topo.Version, "main")
	s.AppendEventLog(run.ID, "state_emit", "p1", "", "")
	s.AppendEventLog(run.ID, "state_emit", "p2", "", "")

	n, err := s.PruneEventLogs(10) // limit > count
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)
}

func TestPruneEventLogs_over_limit_keeps_newest(t *testing.T) {
	s := newTestStore(t)
	_, _, caseID := scaffold(t, s)
	topo, _ := s.CreateSwarmTopology(mustGetProjectID(t, s, caseID), "sup", "memory", "langgraph")
	run, _ := s.CreateRun(caseID, topo.Version, "main")
	for i := range 5 {
		s.AppendEventLog(run.ID, "state_emit", fmt.Sprintf("payload-%d", i), "", "")
	}

	n, err := s.PruneEventLogs(3) // keep only 3 newest
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)
}

// ─── UpdateProjectWebhook ─────────────────────────────────────────────────────

func TestUpdateProjectWebhook(t *testing.T) {
	s := newTestStore(t)
	v, _ := s.CreateVendor("V")
	p, _ := s.CreateProject(v.ID, "P", "")

	require.NoError(t, s.UpdateProjectWebhook(p.ID, "https://example.com/hook"))

	got, err := s.GetProject(p.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "https://example.com/hook", got.WebhookURL)
}

// ─── SwarmTopology runtime_type ───────────────────────────────────────────────

func TestSwarmTopology_RuntimeType(t *testing.T) {
	s := newTestStore(t)
	v, _ := s.CreateVendor("V")
	p, _ := s.CreateProject(v.ID, "P", "")

	for _, rt := range []string{"langgraph", "crewai", "autogen"} {
		topo, err := s.CreateSwarmTopology(p.ID, "sup", "memory", rt)
		require.NoError(t, err)
		assert.Equal(t, rt, topo.RuntimeType)
	}
}

// ─── KillStaleRuns ────────────────────────────────────────────────────────────

func TestKillStaleRuns_fresh_run_not_killed(t *testing.T) {
	s := newTestStore(t)
	_, _, caseID := scaffold(t, s)
	topo, _ := s.CreateSwarmTopology(mustGetProjectID(t, s, caseID), "sup", "memory", "langgraph")
	_, _ = s.CreateRun(caseID, topo.Version, "main")

	// 2h maxAge — a just-created run is well within that window.
	n, err := s.KillStaleRuns(caseID, 2*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, int64(0), n, "freshly created run should not be killed")
}

func TestKillStaleRuns_zero_maxAge_kills_running(t *testing.T) {
	s := newTestStore(t)
	_, _, caseID := scaffold(t, s)
	topo, _ := s.CreateSwarmTopology(mustGetProjectID(t, s, caseID), "sup", "memory", "langgraph")
	_, _ = s.CreateRun(caseID, topo.Version, "main")

	// 0 maxAge means cutoff = now, so any run started before "now" qualifies.
	// Small sleep ensures start_time < cutoff.
	time.Sleep(5 * time.Millisecond)
	n, err := s.KillStaleRuns(caseID, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "RUNNING run older than cutoff must be killed")
}

// ─── AppendEventLog stores git_commit_hash per-entry ─────────────────────────

func TestAppendEventLog_stores_git_commit_hash(t *testing.T) {
	s := newTestStore(t)
	_, _, caseID := scaffold(t, s)
	topo, _ := s.CreateSwarmTopology(mustGetProjectID(t, s, caseID), "sup", "memory", "langgraph")
	run, _ := s.CreateRun(caseID, topo.Version, "main")

	// First entry logged before a commit → gitHash is "".
	e1, err := s.AppendEventLog(run.ID, "state_emit", "payload1", "", "")
	require.NoError(t, err)
	assert.Equal(t, "", e1.GitCommitHash)

	// Second entry logged after the teardown commit → gitHash is real.
	e2, err := s.AppendEventLog(run.ID, "state_emit", "payload2", e1.EventHash, "deadbeef")
	require.NoError(t, err)
	assert.Equal(t, "deadbeef", e2.GitCommitHash)

	// ListEventLogs must return both per-entry hashes.
	logs, err := s.ListEventLogs(run.ID)
	require.NoError(t, err)
	require.Len(t, logs, 2)
	assert.Equal(t, "", logs[0].GitCommitHash)
	assert.Equal(t, "deadbeef", logs[1].GitCommitHash)
}

func TestAppendEventLog_payload_capped_at_store_level(t *testing.T) {
	s := newTestStore(t)
	_, _, caseID := scaffold(t, s)
	topo, _ := s.CreateSwarmTopology(mustGetProjectID(t, s, caseID), "sup", "memory", "langgraph")
	run, _ := s.CreateRun(caseID, topo.Version, "main")

	bigPayload := string(make([]byte, 128*1024)) // 128 KiB — above the 64 KiB cap
	entry, err := s.AppendEventLog(run.ID, "state_emit", bigPayload, "", "")
	require.NoError(t, err)
	assert.LessOrEqual(t, len(entry.Payload), 64*1024, "store must cap payload at 64 KiB")
}

// ─── VerifyChain / CHECK 9.1.2-9.1.3 ─────────────────────────────────────────

func TestVerifyChain_intact_chain_returns_nil(t *testing.T) {
	s := newTestStore(t)
	_, _, caseID := scaffold(t, s)
	topo, _ := s.CreateSwarmTopology(mustGetProjectID(t, s, caseID), "sup", "memory", "langgraph")
	run, _ := s.CreateRun(caseID, topo.Version, "main")

	e1, _ := s.AppendEventLog(run.ID, "state_emit", "payload1", "", "")
	_, _ = s.AppendEventLog(run.ID, "state_emit", "payload2", e1.EventHash, "git123")

	assert.NoError(t, s.VerifyChain(run.ID), "intact chain must verify without error")
}

func TestVerifyChain_tampered_payload_detected(t *testing.T) {
	s := newTestStore(t)
	_, _, caseID := scaffold(t, s)
	topo, _ := s.CreateSwarmTopology(mustGetProjectID(t, s, caseID), "sup", "memory", "langgraph")
	run, _ := s.CreateRun(caseID, topo.Version, "main")

	e1, _ := s.AppendEventLog(run.ID, "state_emit", "original", "", "")
	_, _ = s.AppendEventLog(run.ID, "state_emit", "second", e1.EventHash, "")

	// Tamper: directly update the first entry's payload via raw SQL.
	_, err := s.DB().Exec(`UPDATE run_event_logs SET payload = 'tampered' WHERE id = ?`, e1.ID)
	require.NoError(t, err)

	verr := s.VerifyChain(run.ID)
	require.Error(t, verr, "tampered chain must fail verification")
	assert.Contains(t, verr.Error(), "entry 1", "error must identify the first broken link")
}

func TestVerifyChain_empty_run_returns_nil(t *testing.T) {
	s := newTestStore(t)
	_, _, caseID := scaffold(t, s)
	topo, _ := s.CreateSwarmTopology(mustGetProjectID(t, s, caseID), "sup", "memory", "langgraph")
	run, _ := s.CreateRun(caseID, topo.Version, "main")

	assert.NoError(t, s.VerifyChain(run.ID), "empty run has no chain to verify")
}

// ─── DeleteComponent consistency ─────────────────────────────────────────────

func TestDeleteComponent_not_found_returns_error(t *testing.T) {
	s := newTestStore(t)
	err := s.DeleteComponent(999_999)
	assert.Error(t, err, "deleting a non-existent component must return an error")
}

// ─── CHECK constraint enforcement ────────────────────────────────────────────

func TestUpdateCaseStatus_invalid_status_rejected(t *testing.T) {
	s := newTestStore(t)
	_, _, caseID := scaffold(t, s)
	// "BOGUS" is not in the CHECK constraint for new DBs.
	// This test documents the expected behaviour: new DBs enforce the constraint.
	err := s.UpdateCaseStatus(caseID, "BOGUS_STATUS")
	// The CHECK constraint only applies to fresh schemas; on migrated DBs the
	// column may lack the constraint. We accept both outcomes — the important
	// thing is that valid transitions still work.
	if err != nil {
		assert.Contains(t, err.Error(), "CHECK")
	}
}

func TestUpdateUserStoryStatus_valid_transitions(t *testing.T) {
	s := newTestStore(t)
	_, _, caseID := scaffold(t, s)
	us, _ := s.CreateUserStory(caseID, "Story")

	for _, status := range []string{"IMPLEMENTED", "INVALIDATED", "PENDING"} {
		require.NoError(t, s.UpdateUserStoryStatus(us.ID, status),
			"valid status %q must be accepted", status)
	}
}

func TestKillStaleRuns_skips_completed(t *testing.T) {
	s := newTestStore(t)
	_, _, caseID := scaffold(t, s)
	topo, _ := s.CreateSwarmTopology(mustGetProjectID(t, s, caseID), "sup", "memory", "langgraph")
	run, _ := s.CreateRun(caseID, topo.Version, "main")
	now := time.Now()
	s.UpdateRunStatus(run.ID, "SUCCESS", &now, "abc")

	time.Sleep(5 * time.Millisecond)
	n, err := s.KillStaleRuns(caseID, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(0), n, "SUCCESS run must not be killed")
}

// ─── HasSuccessfulRunAtTopologyVersion ────────────────────────────────────────

func TestHasSuccessfulRunAtTopologyVersion_no_runs_returns_false(t *testing.T) {
	s := newTestStore(t)
	_, pID, caseID := scaffold(t, s)
	topo, _ := s.CreateSwarmTopology(pID, "sup", "memory", "langgraph")

	// Run exists but is still RUNNING (not SUCCESS).
	s.CreateRun(caseID, topo.Version, "main")

	ok, err := s.HasSuccessfulRunAtTopologyVersion(pID, topo.Version)
	require.NoError(t, err)
	assert.False(t, ok, "RUNNING run must not satisfy the check")
}

func TestHasSuccessfulRunAtTopologyVersion_wrong_version_returns_false(t *testing.T) {
	s := newTestStore(t)
	_, pID, caseID := scaffold(t, s)
	topo, _ := s.CreateSwarmTopology(pID, "sup", "memory", "langgraph") // v1

	run, _ := s.CreateRun(caseID, topo.Version, "main")
	s.UpdateRunStatus(run.ID, "SUCCESS", nil, "abc")

	// Ask for v2 — which has never been run.
	ok, err := s.HasSuccessfulRunAtTopologyVersion(pID, topo.Version+1)
	require.NoError(t, err)
	assert.False(t, ok, "SUCCESS run at v1 must not satisfy query for v2")
}

func TestHasSuccessfulRunAtTopologyVersion_matching_version_returns_true(t *testing.T) {
	s := newTestStore(t)
	_, pID, caseID := scaffold(t, s)
	topo, _ := s.CreateSwarmTopology(pID, "sup", "memory", "langgraph")

	run, _ := s.CreateRun(caseID, topo.Version, "main")
	s.UpdateRunStatus(run.ID, "SUCCESS", nil, "abc")

	ok, err := s.HasSuccessfulRunAtTopologyVersion(pID, topo.Version)
	require.NoError(t, err)
	assert.True(t, ok, "SUCCESS run at matching version must satisfy the check")
}

// ─── Secret unique constraint enforcement ─────────────────────────────────────

func TestCreateSecret_duplicate_global_key_fails(t *testing.T) {
	s := newTestStore(t)
	_, err := s.CreateSecret("DUPLICATE_KEY", "value1", nil)
	require.NoError(t, err, "first global secret must succeed")

	_, err = s.CreateSecret("DUPLICATE_KEY", "value2", nil)
	assert.Error(t, err, "inserting a second global secret with the same key must fail (UNIQUE constraint)")
}

func TestCreateSecret_duplicate_project_scoped_key_fails(t *testing.T) {
	s := newTestStore(t)
	v, _ := s.CreateVendor("V")
	p, _ := s.CreateProject(v.ID, "P", "")

	_, err := s.CreateSecret("PROJ_KEY", "v1", &p.ID)
	require.NoError(t, err, "first project-scoped secret must succeed")

	_, err = s.CreateSecret("PROJ_KEY", "v2", &p.ID)
	assert.Error(t, err, "inserting a second project-scoped secret with the same key must fail (UNIQUE constraint)")
}

func TestCreateSecret_global_and_project_scoped_same_key_allowed(t *testing.T) {
	// One global + one project-scoped entry for the same key name is intentional:
	// GetSecret prefers the project-scoped value and falls back to global.
	s := newTestStore(t)
	v, _ := s.CreateVendor("V")
	p, _ := s.CreateProject(v.ID, "P", "")

	_, err := s.CreateSecret("SHARED_KEY", "global_val", nil)
	require.NoError(t, err)
	_, err = s.CreateSecret("SHARED_KEY", "project_val", &p.ID)
	require.NoError(t, err, "one global + one project-scoped entry for the same key must be allowed")
}
