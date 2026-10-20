package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPolicyReplay: past proposals replayed against a new policy show what it
// would have decided differently - above all, what a person rejected that the
// policy would approve. Shell commands, guarded changes and refusals are
// never the policy's to decide.
func TestPolicyReplay(t *testing.T) {
	_, s := e2eWorkspace(t)
	caseID, topo := seedFullCase(t, s)
	run, err := s.CreateRun(caseID, topo, "main")
	require.NoError(t, err)
	_, err = s.AppendEventLogChained(run.ID, "run_bound", `{"base_sha":"b","branch":"staircase/run-1"}`, "")
	require.NoError(t, err)
	seq := 0
	record := func(req domain.YieldRequest, decided map[string]any) {
		seq++
		decided["seq"], decided["action_type"] = seq, req.ActionType
		b, _ := json.Marshal(req)
		_, err := s.AppendEventLogChained(run.ID, "yield_request", string(b), "")
		require.NoError(t, err)
		b, _ = json.Marshal(decided)
		_, err = s.AppendEventLogChained(run.ID, "yield_decided", string(b), "")
		require.NoError(t, err)
	}
	edit := func(file string) domain.YieldRequest {
		return domain.YieldRequest{AgentName: "coder", ActionType: domain.ActionFileEdit,
			ProposedEdits: []domain.ProposedEdit{{File: file, SearchBlock: "a", ReplaceBlock: "b"}}}
	}
	record(edit("README.md"), map[string]any{"source": "operator", "approved": true})
	record(edit("CHANGES.md"), map[string]any{"source": "operator", "approved": false})
	record(edit("main.go"), map[string]any{"source": "operator", "approved": true})
	record(domain.YieldRequest{AgentName: "coder", ActionType: domain.ActionShellExec}, map[string]any{"source": "operator", "approved": true})
	record(edit("docs/x.md"), map[string]any{"source": "operator", "approved": true, "guard": "it writes what looks like a secret"})

	f := filepath.Join(t.TempDir(), "policy.json")
	require.NoError(t, os.WriteFile(f, []byte(`{"rules":[{"action_types":["file_edit"],"allowed_extensions":[".md"],"effect":"approve"}]}`), 0o600))
	e, err := policy.LoadEngineFile(f)
	require.NoError(t, err)

	rep, err := policyReplay(s, e)
	require.NoError(t, err)
	assert.Equal(t, 5, rep.Proposals)
	assert.Equal(t, 1, rep.ApprovedByPersonToo, "README.md")
	require.Len(t, rep.RejectedByPerson, 1, "CHANGES.md: the change to look at")
	assert.Contains(t, rep.RejectedByPerson[0], "CHANGES.md")
	assert.Equal(t, 3, rep.Unchanged, "main.go (no rule), the shell command, the guarded change")
	assert.Empty(t, rep.Skipped)

	// a run whose chain no run can have written (a decision nothing asked for) is named, not counted (ADR 0010)
	other, err := s.CreateRun(caseID, topo, "main")
	require.NoError(t, err)
	_, err = s.AppendEventLogChained(other.ID, "run_bound", `{"base_sha":"b"}`, "")
	require.NoError(t, err)
	_, err = s.AppendEventLogChained(other.ID, "yield_decided", `{"seq":1,"action_type":"file_edit","source":"operator","approved":false}`, "")
	require.NoError(t, err)
	rep, err = policyReplay(s, e)
	require.NoError(t, err)
	assert.Equal(t, 5, rep.Proposals)
	require.Len(t, rep.Skipped, 1)
	assert.Contains(t, rep.Skipped[0], "has no request before it")
}

// TestPolicyReplay_typed_conditions: the replay reads the typed conditions from what each past
// proposal recorded: its files, whether it created them, and its lines.
func TestPolicyReplay_typed_conditions(t *testing.T) {
	_, s := e2eWorkspace(t)
	caseID, topo := seedFullCase(t, s)
	run, err := s.CreateRun(caseID, topo, "main")
	require.NoError(t, err)
	_, err = s.AppendEventLogChained(run.ID, "run_bound", `{"base_sha":"b","branch":"staircase/run-1"}`, "")
	require.NoError(t, err)
	seq := 0
	record := func(req domain.YieldRequest, approved bool, lines int) {
		seq++
		b, _ := json.Marshal(req)
		_, err := s.AppendEventLogChained(run.ID, "yield_request", string(b), "")
		require.NoError(t, err)
		b, _ = json.Marshal(map[string]any{"seq": seq, "action_type": req.ActionType, "source": "operator", "approved": approved, "lines": lines})
		_, err = s.AppendEventLogChained(run.ID, "yield_decided", string(b), "")
		require.NoError(t, err)
	}
	edit := func(file, search, replace string) domain.YieldRequest {
		return domain.YieldRequest{AgentName: "coder", ActionType: domain.ActionFileEdit,
			ProposedEdits: []domain.ProposedEdit{{File: file, SearchBlock: search, ReplaceBlock: replace}}}
	}
	record(edit("docs/a.md", "a", "b"), true, 2)                             // under docs/, small: the rule approves what a person approved
	record(edit(".github/x.md", "a", "b"), false, 2)                         // outside docs/: the rule does not decide it
	record(edit("docs/big.md", "a", "b"), false, 40)                         // too many lines: the rule does not decide it
	record(edit("docs/new.md", orchestrator.MarkerNewFile, "x\n"), false, 0) // a new file under docs/, lines estimated from its text

	f := filepath.Join(t.TempDir(), "policy.json")
	require.NoError(t, os.WriteFile(f, []byte(`{"rules":[{"action_types":["file_edit"],"paths":["docs/**"],"max_lines":5,"effect":"approve"}]}`), 0o600))
	e, err := policy.LoadEngineFile(f)
	require.NoError(t, err)
	rep, err := policyReplay(s, e)
	require.NoError(t, err)
	assert.Equal(t, 4, rep.Proposals)
	assert.Equal(t, 1, rep.ApprovedByPersonToo, "docs/a.md")
	require.Len(t, rep.RejectedByPerson, 1, "docs/new.md is small and under docs/")
	assert.Contains(t, rep.RejectedByPerson[0], "docs/new.md")
	assert.Equal(t, 2, rep.Unchanged, ".github/x.md and the large docs/big.md are not the rule's")
}

// TestPolicyScenarios_command: --scenarios asserts. A met expectation exits
// zero; a forbidden approval or an unintended escalation exits non-zero with the
// scenario named; an inventory that asserts nothing is an error, not a pass.
func TestPolicyScenarios_command(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
		return p
	}
	pol := write("policy.json", `{"rules":[{"action_types":["file_edit"],"allowed_extensions":[".md"],"effect":"approve"}]}`)
	sc := func(expect string) string {
		return `{"scenarios":[{"name":"docs","proposals":[{"edits":[{"file":"docs/a.md","content":"x\n"}],"expect":"` + expect + `"}]}]}`
	}
	run := func(scen string) error {
		cmd := policyTestCmd
		require.NoError(t, cmd.Flags().Set("scenarios", scen))
		defer func() { _ = cmd.Flags().Set("scenarios", "") }()
		return cmd.RunE(cmd, []string{pol})
	}
	assert.NoError(t, run(write("ok.json", sc("approve"))))
	assert.ErrorContains(t, run(write("bad.json", sc("human"))), "1 expectation(s) failed")
	assert.Error(t, run(write("empty.json", `{"scenarios":[]}`)))
	// delivery is asserted from the run branch, not from the decision
	deliver := func(file string) string {
		return `{"scenarios":[{"name":"docs","expect_delivered":["` + file + `"],"proposals":[{"edits":[{"file":"docs/a.md","content":"x\n"}],"expect":"approve"}]}]}`
	}
	assert.NoError(t, run(write("delivers.json", deliver("docs/a.md"))))
	assert.ErrorContains(t, run(write("delivers-wrong.json", deliver("docs/other.md"))), "1 expectation(s) failed")
}
