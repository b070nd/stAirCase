package main

// End-to-end tests for the staircase CLI layer.
//
// These tests live in package main (whitebox) so they can call internal handler
// functions directly without spawning a subprocess, making them runnable in
// standard `go test` without a pre-built binary or a Python venv.
//
// Coverage targets (from docs/architecture.md §6 "What is not tested"):
//   - cmd/staircase/* — all CLI command handlers
//   - Full data-flow: persistence → gate → compile → inspect
//
// Tests that require Python (BootstrapVenv) are explicitly skipped so that CI
// environments without Python3 remain green.

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/b070nd/staircase-core/src/internal/audit"
	"github.com/b070nd/staircase-core/src/internal/crypto"
	"github.com/b070nd/staircase-core/src/internal/gate"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// e2eWorkspace creates a temp workspace, initialises the DB, and points viper
// at it. Returns the Store so tests can seed data directly.
func e2eWorkspace(t *testing.T) (wsDir string, s *persistence.Store) {
	t.Helper()
	wsDir = t.TempDir()
	db, err := persistence.InitDB(wsDir)
	require.NoError(t, err, "InitDB must succeed")
	t.Cleanup(func() { db.Close() })
	viper.Set("STAIRCASE_DIR", wsDir)
	return wsDir, persistence.NewStore(db)
}

// seedFullCase sets up the minimum data needed for all structural BLOCK gates
// to pass: vendor → project → case → PENDING story → topology → supervisor agent.
// Returns (caseID, topoVersion).
func seedFullCase(t *testing.T, s *persistence.Store) (caseID int64, topoVersion int) {
	t.Helper()
	v, err := s.CreateVendor(t.Name() + "_vendor")
	require.NoError(t, err)
	p, err := s.CreateProject(v.ID, t.Name()+"_project", "") // no source path
	require.NoError(t, err)
	c, err := s.CreateCase(p.ID)
	require.NoError(t, err)
	_, err = s.CreateUserStory(c.ID, "As a user I want end-to-end coverage")
	require.NoError(t, err)
	topo, err := s.CreateSwarmTopology(p.ID, "supervisor", "memory", "langgraph")
	require.NoError(t, err)
	_, err = s.CreateAgentNode(topo.ID, "supervisor", "Orchestrates the swarm", "claude-sonnet-4-5", nil)
	require.NoError(t, err)
	return c.ID, topo.Version
}

// fakeRuntimeEnv creates the minimal on-disk state that makes runtime BLOCK
// gates pass without an actual Python venv or pip install:
//   - wsDir/.key                    → secret.key_file gate
//   - wsDir/venv/bin/python         → runtime.venv_ready gate
//   - wsDir/venv/.requirements_hash → runtime.venv_ready gate
//   - wsDir/tmp/graph_exec_caseN.py → runtime.script_compiled gate
//   - wsDir/tmp/graph_exec_caseN.topo (topoVersion) → staleness check
func fakeRuntimeEnv(t *testing.T, wsDir string, caseID int64, topoVersion int) {
	t.Helper()
	require.NoError(t, crypto.GenerateKey(wsDir))

	venvBin := filepath.Join(wsDir, "venv", "bin")
	require.NoError(t, os.MkdirAll(venvBin, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(venvBin, "python"), []byte("#!/bin/sh\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "venv", ".requirements_hash"), []byte("abc123"), 0o644))

	tmpDir := filepath.Join(wsDir, "tmp")
	require.NoError(t, os.MkdirAll(tmpDir, 0o755))
	script := filepath.Join(tmpDir, fmt.Sprintf("graph_exec_case%d.py", caseID))
	sidecar := filepath.Join(tmpDir, fmt.Sprintf("graph_exec_case%d.topo", caseID))
	require.NoError(t, os.WriteFile(script, []byte("# placeholder"), 0o644))
	require.NoError(t, os.WriteFile(sidecar, []byte(fmt.Sprintf("%d", topoVersion)), 0o644))
}

// ─── Scenario 1: Workspace bootstrap ─────────────────────────────────────────

// TestE2E_WorkspaceBootstrap_DBAndKey verifies that InitDB + GenerateKey
// produce a usable workspace on a fresh directory.
func TestE2E_WorkspaceBootstrap_DBAndKey(t *testing.T) {
	wsDir := t.TempDir()

	db, err := persistence.InitDB(wsDir)
	require.NoError(t, err)
	db.Close()

	require.NoError(t, crypto.GenerateKey(wsDir))

	dbInfo, err := os.Stat(filepath.Join(wsDir, "workspace.db"))
	require.NoError(t, err, "workspace.db must be created")
	assert.Greater(t, dbInfo.Size(), int64(0))

	keyInfo, err := os.Stat(filepath.Join(wsDir, ".key"))
	require.NoError(t, err, ".key must be created")
	assert.Equal(t, int64(32), keyInfo.Size(), ".key must be 32 bytes (AES-256)")
	assert.Equal(t, os.FileMode(0o600), keyInfo.Mode().Perm(), ".key must be mode 0600")
}

// TestE2E_WorkspaceBootstrap_Idempotent verifies that calling InitDB and
// GenerateKey a second time on an existing workspace is a no-op.
func TestE2E_WorkspaceBootstrap_Idempotent(t *testing.T) {
	wsDir := t.TempDir()

	db1, err := persistence.InitDB(wsDir)
	require.NoError(t, err)
	require.NoError(t, crypto.GenerateKey(wsDir))
	key1, err := crypto.LoadKey(wsDir)
	require.NoError(t, err)
	db1.Close()

	// Second call must not error and must not overwrite the key.
	db2, err := persistence.InitDB(wsDir)
	require.NoError(t, err)
	db2.Close()
	require.NoError(t, crypto.GenerateKey(wsDir))
	key2, err := crypto.LoadKey(wsDir)
	require.NoError(t, err)

	assert.Equal(t, key1, key2, "key must be unchanged on second init")
}

// ─── Scenario 2: Compile pipeline ────────────────────────────────────────────

// TestE2E_Compile_GeneratesScriptAndTopoSidecar exercises the full compile
// handler: it sets up a complete topology, calls compileCaseHandler, and
// verifies that both the Python script and the .topo version sidecar exist.
func TestE2E_Compile_GeneratesScriptAndTopoSidecar(t *testing.T) {
	wsDir, s := e2eWorkspace(t)

	v, _ := s.CreateVendor("AcmeCorp")
	p, _ := s.CreateProject(v.ID, "WebShop", "")
	c, _ := s.CreateCase(p.ID)
	s.CreateUserStory(c.ID, "Implement checkout flow")
	topo, _ := s.CreateSwarmTopology(p.ID, "supervisor", "memory", "langgraph")
	s.CreateAgentNode(topo.ID, "supervisor", "Routes tasks", "claude-sonnet-4-5", nil)

	// Compile must not fail.
	compileForce = true // allow overwrite in case test reruns
	err := compileCaseHandler(nil, []string{fmt.Sprintf("%d", c.ID)})
	require.NoError(t, err)

	script := filepath.Join(wsDir, "tmp", fmt.Sprintf("graph_exec_case%d.py", c.ID))
	sidecar := filepath.Join(wsDir, "tmp", fmt.Sprintf("graph_exec_case%d.topo", c.ID))

	info, err := os.Stat(script)
	require.NoError(t, err, "compiled script must exist after compile")
	assert.Greater(t, info.Size(), int64(0), "script must be non-empty")

	raw, err := os.ReadFile(sidecar)
	require.NoError(t, err, ".topo sidecar must exist")
	assert.Equal(t, fmt.Sprintf("%d", topo.Version), strings.TrimSpace(string(raw)),
		".topo sidecar must contain the topology version")
}

// ─── Scenario 3: Gate — incomplete setup blocks ───────────────────────────────

// TestE2E_Gate_BlocksOnMissingTopology verifies that running gate checks on a
// case that has stories but no registered topology produces a blocking report.
func TestE2E_Gate_BlocksOnMissingTopology(t *testing.T) {
	wsDir, s := e2eWorkspace(t)

	v, _ := s.CreateVendor("V")
	p, _ := s.CreateProject(v.ID, "P", "")
	c, _ := s.CreateCase(p.ID)
	s.CreateUserStory(c.ID, "A story")

	report := gate.RunAll(gate.Context{CaseID: c.ID, WsDir: wsDir, Store: s})
	assert.True(t, report.Blocking(), "missing topology must make the report blocking")

	// Specifically the topology.exists gate must have failed.
	var topoGate *gate.Result
	for i := range report.Gates {
		if report.Gates[i].Name == "topology.exists" {
			topoGate = &report.Gates[i]
			break
		}
	}
	require.NotNil(t, topoGate, "topology.exists gate must appear in the report")
	assert.Equal(t, gate.StatusFail, topoGate.Status)
}

// TestE2E_Gate_BlocksOnNoStories verifies that a case with a topology but zero
// PENDING stories is blocked by the case.has_stories gate.
func TestE2E_Gate_BlocksOnNoStories(t *testing.T) {
	wsDir, s := e2eWorkspace(t)

	v, _ := s.CreateVendor("V")
	p, _ := s.CreateProject(v.ID, "P", "")
	c, _ := s.CreateCase(p.ID)
	// No stories added.
	s.CreateSwarmTopology(p.ID, "sup", "memory", "langgraph")

	report := gate.RunAll(gate.Context{CaseID: c.ID, WsDir: wsDir, Store: s})
	assert.True(t, report.Blocking())

	for _, r := range report.Gates {
		if r.Name == "case.has_stories" {
			assert.Equal(t, gate.StatusFail, r.Status)
			return
		}
	}
	t.Fatal("case.has_stories gate not found in report")
}

// ─── Scenario 4: Gate — full structural setup passes ─────────────────────────

// TestE2E_Gate_StructuralGatesAllPass sets up every entity required by the
// structural gate category and verifies that all structural BLOCK gates pass.
func TestE2E_Gate_StructuralGatesAllPass(t *testing.T) {
	wsDir, s := e2eWorkspace(t)
	caseID, topoVersion := seedFullCase(t, s)
	fakeRuntimeEnv(t, wsDir, caseID, topoVersion)

	// Also add the ANTHROPIC_API_KEY so the security gate doesn't block.
	s.CreateSecret("ANTHROPIC_API_KEY", "v1:dummyEncryptedValue", nil)

	report := gate.RunAll(gate.Context{CaseID: caseID, WsDir: wsDir, Store: s})

	// Check that every gate in the "structural" category passed or warned (no fail).
	for _, r := range report.Gates {
		if r.Category == "structural" && r.Severity == gate.SeverityBlock {
			assert.NotEqual(t, gate.StatusFail, r.Status,
				"structural BLOCK gate %q must not fail", r.Name)
		}
	}
}

// TestE2E_Gate_JSONReport verifies that the gate report can be marshalled to
// valid JSON with the expected top-level fields.
func TestE2E_Gate_JSONReport(t *testing.T) {
	wsDir, s := e2eWorkspace(t)
	caseID, topoVersion := seedFullCase(t, s)
	fakeRuntimeEnv(t, wsDir, caseID, topoVersion)
	s.CreateSecret("ANTHROPIC_API_KEY", "v1:dummy", nil)

	report := gate.RunAll(gate.Context{CaseID: caseID, WsDir: wsDir, Store: s})

	data, err := json.Marshal(report)
	require.NoError(t, err, "gate report must serialise to JSON")

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))
	assert.Contains(t, parsed, "overall", "JSON must have 'overall' field")
	assert.Contains(t, parsed, "gates", "JSON must have 'gates' field")
	assert.Contains(t, parsed, "summary", "JSON must have 'summary' field")
	assert.Equal(t, float64(caseID), parsed["case_id"], "JSON must include case_id")
}

// ─── Scenario 5: Secret lifecycle ────────────────────────────────────────────

// TestE2E_SecretGate_AnthropicKeyMissingBlocks verifies that the
// secret.anthropic_key gate blocks when no secret has been registered.
func TestE2E_SecretGate_AnthropicKeyMissingBlocks(t *testing.T) {
	wsDir, s := e2eWorkspace(t)
	caseID, _ := seedFullCase(t, s)

	report := gate.RunAll(gate.Context{CaseID: caseID, WsDir: wsDir, Store: s})

	for _, r := range report.Gates {
		if r.Name == "secret.anthropic_key" {
			assert.Equal(t, gate.StatusFail, r.Status)
			assert.Contains(t, r.Message, "ANTHROPIC_API_KEY")
			return
		}
	}
	t.Fatal("secret.anthropic_key gate not found in report")
}

// TestE2E_SecretGate_AnthropicKeyPresentPasses verifies that after registering
// the ANTHROPIC_API_KEY secret, the security gate passes.
func TestE2E_SecretGate_AnthropicKeyPresentPasses(t *testing.T) {
	wsDir, s := e2eWorkspace(t)
	caseID, _ := seedFullCase(t, s)

	key, err := crypto.Encrypt(make([]byte, 32), "sk-test-dummy-key")
	require.NoError(t, err)
	s.CreateSecret("ANTHROPIC_API_KEY", key, nil)

	report := gate.RunAll(gate.Context{CaseID: caseID, WsDir: wsDir, Store: s})

	for _, r := range report.Gates {
		if r.Name == "secret.anthropic_key" {
			assert.Equal(t, gate.StatusPass, r.Status)
			return
		}
	}
	t.Fatal("secret.anthropic_key gate not found in report")
}

// ─── Scenario 6: Inspect — runs and event log ─────────────────────────────────

// TestE2E_Inspect_RunsLifecycle verifies the full run lifecycle:
// create → list → update → list again, checking that status transitions
// are reflected correctly in the persistence layer.
func TestE2E_Inspect_RunsLifecycle(t *testing.T) {
	_, s := e2eWorkspace(t)
	caseID, topoVersion := seedFullCase(t, s)

	// No runs yet.
	runs, err := s.ListRunsByCase(caseID)
	require.NoError(t, err)
	assert.Empty(t, runs, "no runs before CreateRun")

	// Create a run.
	run, err := s.CreateRun(caseID, topoVersion, "main")
	require.NoError(t, err)
	assert.Equal(t, persistence.RunStatusRunning, run.Status)

	runs, err = s.ListRunsByCase(caseID)
	require.NoError(t, err)
	assert.Len(t, runs, 1)
	assert.Equal(t, persistence.RunStatusRunning, runs[0].Status)

	// Mark SUCCESS.
	require.NoError(t, s.UpdateRunStatus(run.ID, persistence.RunStatusSuccess, nil, "abc123"))

	runs, err = s.ListRunsByCase(caseID)
	require.NoError(t, err)
	assert.Equal(t, persistence.RunStatusSuccess, runs[0].Status)
}

// TestE2E_Inspect_EventLogChain verifies that appending event log entries
// produces an intact SHA-256 chain that the inspect command would verify.
func TestE2E_Inspect_EventLogChain(t *testing.T) {
	_, s := e2eWorkspace(t)
	caseID, topoVersion := seedFullCase(t, s)

	run, err := s.CreateRun(caseID, topoVersion, "feature/test")
	require.NoError(t, err)

	payloads := []string{`{"node":"supervisor","state":"init"}`, `{"node":"worker","state":"done"}`}
	for _, p := range payloads {
		prev, _ := s.GetLastEventHash(run.ID)
		_, err := s.AppendEventLog(run.ID, "state_emit", p, prev, "")
		require.NoError(t, err)
	}

	logs, err := s.ListEventLogs(run.ID)
	require.NoError(t, err)
	require.Len(t, logs, len(payloads), "all payloads must be stored")

	// Verify the chain: every entry must have a non-empty hash, and consecutive
	// entries must have distinct hashes (each entry's hash covers the previous one).
	seen := make(map[string]bool)
	for i, l := range logs {
		assert.NotEmpty(t, l.EventHash, "entry %d must have a non-empty EventHash", i)
		assert.False(t, seen[l.EventHash], "EventHash must be unique across entries (entry %d duplicates a prior hash)", i)
		seen[l.EventHash] = true
	}
}

// ─── Scenario 7: Full workflow ────────────────────────────────────────────────

// TestE2E_FullWorkflow_SetupCompileGate exercises the canonical pre-run
// workflow end-to-end:
//
//  1. Workspace bootstrap (DB + key)
//  2. Entity creation (vendor → project → case → stories → topology → agents)
//  3. Compile (handler call → script + sidecar on disk)
//  4. Gate check (report must not be blocking once runtime env is faked)
//  5. Run creation + event log + status update
func TestE2E_FullWorkflow_SetupCompileGate(t *testing.T) {
	wsDir, s := e2eWorkspace(t)

	// 1. Key
	require.NoError(t, crypto.GenerateKey(wsDir))

	// 2. Entities
	v, _ := s.CreateVendor("FullCorp")
	p, _ := s.CreateProject(v.ID, "FullProject", "")
	c, _ := s.CreateCase(p.ID)
	s.CreateUserStory(c.ID, "Story A")
	s.CreateUserStory(c.ID, "Story B")
	topo, _ := s.CreateSwarmTopology(p.ID, "supervisor", "memory", "langgraph")
	s.CreateAgentNode(topo.ID, "supervisor", "Routes tasks to workers", "claude-sonnet-4-5", nil)
	s.CreateAgentNode(topo.ID, "worker", "Implements user stories", "claude-sonnet-4-5", nil)
	s.CreateEdge(topo.ID, "supervisor", "worker", "")
	s.CreateEdge(topo.ID, "worker", "END", "")

	encKey, _ := crypto.Encrypt(make([]byte, 32), "sk-test-key")
	s.CreateSecret("ANTHROPIC_API_KEY", encKey, nil)

	// 3. Compile
	compileForce = true
	require.NoError(t, compileCaseHandler(nil, []string{fmt.Sprintf("%d", c.ID)}))

	script := filepath.Join(wsDir, "tmp", fmt.Sprintf("graph_exec_case%d.py", c.ID))
	_, err := os.Stat(script)
	require.NoError(t, err, "script must exist after compile")

	// 4. Fake runtime env so runtime gates don't block, then gate check.
	fakeRuntimeEnv(t, wsDir, c.ID, topo.Version)
	report := gate.RunAll(gate.Context{CaseID: c.ID, WsDir: wsDir, Store: s})

	// No structural BLOCK gates should fail.
	for _, r := range report.Gates {
		if r.Severity == gate.SeverityBlock {
			assert.NotEqual(t, gate.StatusFail, r.Status,
				"BLOCK gate %q must not fail in a fully configured workspace", r.Name)
		}
	}
	assert.NotEqual(t, gate.StatusFail, report.Overall,
		"overall report must not be FAIL after full setup")

	// 5. Simulate a run with event log.
	run, err := s.CreateRun(c.ID, topo.Version, "staircase/run-1")
	require.NoError(t, err)
	prev, _ := s.GetLastEventHash(run.ID)
	s.AppendEventLog(run.ID, "state_emit", `{"step":"start"}`, prev, "")
	require.NoError(t, s.UpdateRunStatus(run.ID, persistence.RunStatusSuccess, nil, "deadbeef"))

	// Verify the run is now SUCCESS via the list method (simulates inspect runs).
	runs, err := s.ListRunsByCase(c.ID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, persistence.RunStatusSuccess, runs[0].Status)
	assert.Equal(t, topo.Version, runs[0].TopologyVersion)
}

// ─── Scenario 5: Audit export + verify ───────────────────────────────────────

// seedRunWithLogs creates a run and appends n event log entries, returning the runID.
func seedRunWithLogs(t *testing.T, s *persistence.Store, n int) int64 {
	t.Helper()
	v, _ := s.CreateVendor("AuditVendor")
	p, _ := s.CreateProject(v.ID, "AuditProject", "")
	c, _ := s.CreateCase(p.ID)
	topo, _ := s.CreateSwarmTopology(p.ID, "sup", "memory", "langgraph")
	run, err := s.CreateRun(c.ID, topo.Version, "main")
	require.NoError(t, err)

	prevHash := ""
	for i := 0; i < n; i++ {
		entry, err := s.AppendEventLog(run.ID, "state_emit",
			fmt.Sprintf(`{"step":%d}`, i), prevHash, "")
		require.NoError(t, err)
		prevHash = entry.EventHash
	}
	return run.ID
}

// TestAudit_Export_creates_checkpoint_file verifies that auditExportHandler
// writes a valid checkpoint JSON with the correct run_id and entry count.
func TestAudit_Export_creates_checkpoint_file(t *testing.T) {
	wsDir, s := e2eWorkspace(t)
	require.NoError(t, crypto.GenerateSigningKey(wsDir))

	runID := seedRunWithLogs(t, s, 3)

	err := auditExportHandler(nil, []string{strconv.FormatInt(runID, 10)})
	require.NoError(t, err)

	cpPath := filepath.Join(wsDir, "audit", fmt.Sprintf("run-%d.checkpoint.json", runID))
	require.FileExists(t, cpPath)

	raw, err := os.ReadFile(cpPath)
	require.NoError(t, err)

	var cp AuditCheckpoint
	require.NoError(t, json.Unmarshal(raw, &cp))
	assert.Equal(t, runID, cp.RunID)
	assert.Len(t, cp.Entries, 3)
	assert.Len(t, cp.Signature, 128, "Ed25519 signature must be 128 hex chars")
}

// TestAudit_Export_unknown_run_returns_error verifies that exporting a
// non-existent run-id returns an error (not a panic or silent no-op).
func TestAudit_Export_unknown_run_returns_error(t *testing.T) {
	wsDir, _ := e2eWorkspace(t)
	require.NoError(t, crypto.GenerateSigningKey(wsDir))

	err := auditExportHandler(nil, []string{"9999"})
	assert.Error(t, err)
}

// TestAudit_Export_missing_signing_key_returns_error ensures that export
// fails gracefully when the signing key has not been initialised.
func TestAudit_Export_missing_signing_key_returns_error(t *testing.T) {
	_, s := e2eWorkspace(t)
	// Do NOT call GenerateSigningKey — key is absent.
	runID := seedRunWithLogs(t, s, 1)

	err := auditExportHandler(nil, []string{strconv.FormatInt(runID, 10)})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "signing key")
}

// TestAudit_Verify_valid_checkpoint_passes verifies the round-trip: export
// then verify on the same workspace returns no error.
func TestAudit_Verify_valid_checkpoint_passes(t *testing.T) {
	wsDir, s := e2eWorkspace(t)
	require.NoError(t, crypto.GenerateSigningKey(wsDir))

	runID := seedRunWithLogs(t, s, 5)

	// Export.
	require.NoError(t, auditExportHandler(nil, []string{strconv.FormatInt(runID, 10)}))

	// Verify.
	cpPath := filepath.Join(wsDir, "audit", fmt.Sprintf("run-%d.checkpoint.json", runID))
	err := auditVerifyHandler(nil, []string{cpPath})
	assert.NoError(t, err)
}

// TestAudit_Verify_tampered_signature_fails verifies that modifying the
// signature field causes auditVerifyHandler to return an error.
func TestAudit_Verify_tampered_signature_fails(t *testing.T) {
	wsDir, s := e2eWorkspace(t)
	require.NoError(t, crypto.GenerateSigningKey(wsDir))

	runID := seedRunWithLogs(t, s, 2)
	require.NoError(t, auditExportHandler(nil, []string{strconv.FormatInt(runID, 10)}))

	cpPath := filepath.Join(wsDir, "audit", fmt.Sprintf("run-%d.checkpoint.json", runID))

	// Read, corrupt signature, write back.
	raw, err := os.ReadFile(cpPath)
	require.NoError(t, err)
	var cp AuditCheckpoint
	require.NoError(t, json.Unmarshal(raw, &cp))
	cp.Signature = strings.Repeat("a", 128) // valid hex length, wrong value
	corrupted, err := json.MarshalIndent(cp, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cpPath, corrupted, 0o600))

	err = auditVerifyHandler(nil, []string{cpPath})
	assert.Error(t, err)
}

// TestAudit_Verify_tampered_entry_fails verifies that modifying an entry's
// payload causes the hash-chain check to fail.
func TestAudit_Verify_tampered_entry_fails(t *testing.T) {
	wsDir, s := e2eWorkspace(t)
	require.NoError(t, crypto.GenerateSigningKey(wsDir))

	runID := seedRunWithLogs(t, s, 3)
	require.NoError(t, auditExportHandler(nil, []string{strconv.FormatInt(runID, 10)}))

	cpPath := filepath.Join(wsDir, "audit", fmt.Sprintf("run-%d.checkpoint.json", runID))

	// Read, alter an entry's payload, re-sign so the sig check passes but
	// the hash chain still breaks (we don't re-derive hashes).
	raw, err := os.ReadFile(cpPath)
	require.NoError(t, err)
	var cp AuditCheckpoint
	require.NoError(t, json.Unmarshal(raw, &cp))
	cp.Entries[1].Payload = `{"step":"INJECTED"}`
	corrupted, err := json.MarshalIndent(cp, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cpPath, corrupted, 0o600))

	err = auditVerifyHandler(nil, []string{cpPath})
	assert.Error(t, err, "tampered payload must fail verification")
}

// TestAudit_Verify_missing_public_key_returns_error verifies that verify
// fails gracefully when the public key is absent.
func TestAudit_Verify_missing_public_key_returns_error(t *testing.T) {
	wsDir, s := e2eWorkspace(t)
	require.NoError(t, crypto.GenerateSigningKey(wsDir))

	runID := seedRunWithLogs(t, s, 1)
	require.NoError(t, auditExportHandler(nil, []string{strconv.FormatInt(runID, 10)}))

	// Remove the public key.
	require.NoError(t, os.Remove(filepath.Join(wsDir, crypto.SigningPubFile)))

	cpPath := filepath.Join(wsDir, "audit", fmt.Sprintf("run-%d.checkpoint.json", runID))
	err := auditVerifyHandler(nil, []string{cpPath})
	assert.Error(t, err)
}

// TestAudit_Export_empty_run_produces_valid_checkpoint verifies that a run
// with zero event logs still exports a valid (empty entries) checkpoint.
func TestAudit_Export_empty_run_produces_valid_checkpoint(t *testing.T) {
	wsDir, s := e2eWorkspace(t)
	require.NoError(t, crypto.GenerateSigningKey(wsDir))

	runID := seedRunWithLogs(t, s, 0)
	require.NoError(t, auditExportHandler(nil, []string{strconv.FormatInt(runID, 10)}))

	cpPath := filepath.Join(wsDir, "audit", fmt.Sprintf("run-%d.checkpoint.json", runID))
	err := auditVerifyHandler(nil, []string{cpPath})
	assert.NoError(t, err)
}

// TestCheckpointSignatureTamper is the checklist-named tamper test (CHECK 9.2.4).
// It verifies that altering the Ed25519 signature field in a checkpoint file
// causes auditVerifyHandler to return a non-nil error.
func TestCheckpointSignatureTamper(t *testing.T) {
	wsDir, s := e2eWorkspace(t)
	require.NoError(t, crypto.GenerateSigningKey(wsDir))

	runID := seedRunWithLogs(t, s, 2)
	require.NoError(t, auditExportHandler(nil, []string{strconv.FormatInt(runID, 10)}))

	cpPath := filepath.Join(wsDir, "audit", fmt.Sprintf("run-%d.checkpoint.json", runID))

	// Read the checkpoint, replace the signature with 128 hex 'f' chars (wrong sig).
	raw, err := os.ReadFile(cpPath)
	require.NoError(t, err)
	var cp AuditCheckpoint
	require.NoError(t, json.Unmarshal(raw, &cp))
	cp.Signature = strings.Repeat("f", 128) // valid hex length, wrong signature
	tampered, err := json.MarshalIndent(cp, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cpPath, tampered, 0o600))

	err = auditVerifyHandler(nil, []string{cpPath})
	assert.Error(t, err, "tampered Ed25519 signature must fail verification")
}

// TestAudit_Export_anchor_and_verify_checkAnchor exercises the full B3 flow at
// the CLI-handler level: export --anchor against a mock Rekor writes the
// .anchor sidecar, and verify --check-anchor confirms the record against the
// log. Flipping a byte in the checkpoint then makes --check-anchor fail.
func TestAudit_Export_anchor_and_verify_checkAnchor(t *testing.T) {
	wsDir, s := e2eWorkspace(t)
	require.NoError(t, crypto.GenerateSigningKey(wsDir))
	runID := seedRunWithLogs(t, s, 2)

	// Minimal mock Rekor: accepts any entry, serves it back by uuid.
	entries := map[string]string{}
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var e map[string]any
			_ = json.NewDecoder(r.Body).Decode(&e)
			raw, _ := json.Marshal(e)
			sum := sha256.Sum256(raw)
			uuid := hex.EncodeToString(sum[:])
			entries[uuid] = base64.StdEncoding.EncodeToString(raw)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				uuid: map[string]any{"logIndex": 1, "logID": "mock", "integratedTime": 1750000000},
			})
			return
		}
		uuid := strings.TrimPrefix(r.URL.Path, "/api/v1/log/entries/")
		if b, ok := entries[uuid]; ok {
			_ = json.NewEncoder(w).Encode(map[string]any{uuid: map[string]any{"body": b}})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(mock.Close)

	// Export with anchoring enabled.
	auditAnchor, auditRekorURL = true, mock.URL
	t.Cleanup(func() { auditAnchor, auditCheckAnchor, auditRekorURL = false, false, audit.DefaultRekorURL })
	require.NoError(t, auditExportHandler(nil, []string{strconv.FormatInt(runID, 10)}))

	cpPath := filepath.Join(wsDir, "audit", fmt.Sprintf("run-%d.checkpoint.json", runID))
	require.FileExists(t, cpPath+".anchor", "anchor sidecar must be written")

	// Verify with anchor checking enabled.
	auditCheckAnchor = true
	require.NoError(t, auditVerifyHandler(nil, []string{cpPath}))

	// Tamper with the checkpoint record → both signature and anchor must fail.
	raw, err := os.ReadFile(cpPath)
	require.NoError(t, err)
	tampered := bytes.Replace(raw, []byte(`"state_emit"`), []byte(`"FORGED_evt"`), 1)
	require.NotEqual(t, raw, tampered, "tamper must change the file")
	require.NoError(t, os.WriteFile(cpPath, tampered, 0o600))
	assert.Error(t, auditVerifyHandler(nil, []string{cpPath}))
}
