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
	ws, s := e2eWorkspace(t)
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

	rep, err := policyReplay(s, ws, e)
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
	rep, err = policyReplay(s, ws, e)
	require.NoError(t, err)
	assert.Equal(t, 5, rep.Proposals)
	require.Len(t, rep.Skipped, 1)
	assert.Contains(t, rep.Skipped[0], "has no request before it")
}

// TestPolicyReplay_typed_conditions: the replay reads the typed conditions from what each past
// proposal recorded: its files, whether it created them, and its lines.
func TestPolicyReplay_typed_conditions(t *testing.T) {
	ws, s := e2eWorkspace(t)
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
	rep, err := policyReplay(s, ws, e)
	require.NoError(t, err)
	assert.Equal(t, 4, rep.Proposals)
	assert.Equal(t, 1, rep.ApprovedByPersonToo, "docs/a.md")
	require.Len(t, rep.RejectedByPerson, 1, "docs/new.md is small and under docs/")
	assert.Contains(t, rep.RejectedByPerson[0], "docs/new.md")
	assert.Equal(t, 2, rep.Unchanged, ".github/x.md and the large docs/big.md are not the rule's")
}

// TestPolicyReplay_bars: the protected paths a run's saved terms name and the sensitive list are checked against the paths of
// every past decision, and what a rule, the agreed task, a validator or evidence approved there is reported in a section of its
// own: it would go to a person now. What a person decided stays as it was, and so does history the bars do not reach.
func TestPolicyReplay_bars(t *testing.T) {
	ws, s := e2eWorkspace(t)
	caseID, topo := seedFullCase(t, s)
	run, err := s.CreateRun(caseID, topo, "main")
	require.NoError(t, err)
	_, err = s.AppendEventLogChained(run.ID, "run_bound", `{"base_sha":"b","branch":"staircase/run-1"}`, "")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "journal"), 0o700))
	// the run's terms, as it saved them: its check is `make check`, which defines the Makefile
	require.NoError(t, os.WriteFile(filepath.Join(ws, "journal", "run-1.options.json"), []byte(`{"version":1,"checks":["make check"]}`), 0o600))
	seq := 0
	record := func(file, source string, approved bool) {
		seq++
		req := domain.YieldRequest{AgentName: "coder", ActionType: domain.ActionFileEdit,
			ProposedEdits: []domain.ProposedEdit{{File: file, SearchBlock: "a", ReplaceBlock: "b"}}}
		b, _ := json.Marshal(req)
		_, err := s.AppendEventLogChained(run.ID, "yield_request", string(b), "")
		require.NoError(t, err)
		b, _ = json.Marshal(map[string]any{"seq": seq, "action_type": req.ActionType, "source": source, "approved": approved, "paths": []string{file}})
		_, err = s.AppendEventLogChained(run.ID, "yield_decided", string(b), "")
		require.NoError(t, err)
	}
	record("Makefile", "policy", true)                 // a rule approved the check's own definition
	record(".github/workflows/ci.yml", "policy", true) // a rule approved a CI file
	record("go.mod", "task", true)                     // the agreed task approved a sensitive file
	record("docs/a.md", "policy", true)                // not reached by a bar
	record("Makefile", "operator", true)               // a person decided it: as it was
	record("scripts/x.txt", "operator", true)          // a person decided it: as it was
	record("Makefile", "operator", false)              // a person rejected it: the policy cannot decide it now, so it is no difference

	f := filepath.Join(t.TempDir(), "policy.json")
	require.NoError(t, os.WriteFile(f, []byte(`{"rules":[{"action_types":["file_edit"],"effect":"approve"}]}`), 0o600))
	e, err := policy.LoadEngineFile(f)
	require.NoError(t, err)
	rep, err := policyReplay(s, ws, e)
	require.NoError(t, err)
	assert.Equal(t, 7, rep.Proposals)
	require.Len(t, rep.WouldGoToPerson, 3)
	assert.Contains(t, rep.WouldGoToPerson[0], "Makefile")
	assert.Contains(t, rep.WouldGoToPerson[0], "run #1")
	assert.Contains(t, rep.WouldGoToPerson[0], "policy")
	assert.Contains(t, rep.WouldGoToPerson[0], "holds the definition of a check")
	assert.Contains(t, rep.WouldGoToPerson[1], ".github/workflows/ci.yml")
	assert.Contains(t, rep.WouldGoToPerson[1], "sensitive")
	assert.Contains(t, rep.WouldGoToPerson[2], "go.mod")
	assert.Contains(t, rep.WouldGoToPerson[2], "task")
	assert.Equal(t, 3, rep.Unchanged, "docs/a.md (the rule still approves it) and the Makefile edits a person decided")
	assert.Equal(t, 1, rep.ApprovedByPersonToo, "scripts/x.txt: a person approved it, the policy approves it too")
	assert.Empty(t, rep.RejectedByPerson, "a person's decision on a barred path is not a difference the policy makes")
}

// TestPolicyReplay_bars_unreadable_terms: a run whose saved terms cannot be read is named, and the sensitive list still applies
// to it; its protected paths are unknown, so a Makefile edit is not reported.
func TestPolicyReplay_bars_unreadable_terms(t *testing.T) {
	ws, s := e2eWorkspace(t)
	caseID, topo := seedFullCase(t, s)
	run, err := s.CreateRun(caseID, topo, "main")
	require.NoError(t, err)
	_, err = s.AppendEventLogChained(run.ID, "run_bound", `{"base_sha":"b","branch":"staircase/run-1"}`, "")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "journal"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "journal", "run-1.options.json"), []byte(`not json`), 0o600))
	for i, file := range []string{"docs/a.md", ".github/workflows/ci.yml"} {
		req := domain.YieldRequest{AgentName: "coder", ActionType: domain.ActionFileEdit,
			ProposedEdits: []domain.ProposedEdit{{File: file, SearchBlock: "a", ReplaceBlock: "b"}}}
		b, _ := json.Marshal(req)
		_, err := s.AppendEventLogChained(run.ID, "yield_request", string(b), "")
		require.NoError(t, err)
		b, _ = json.Marshal(map[string]any{"seq": i + 1, "action_type": req.ActionType, "source": "policy", "approved": true, "paths": []string{file}})
		_, err = s.AppendEventLogChained(run.ID, "yield_decided", string(b), "")
		require.NoError(t, err)
	}
	e, err := policy.LoadEngineFile(writePolicy(t, `{"rules":[]}`))
	require.NoError(t, err)
	rep, err := policyReplay(s, ws, e)
	require.NoError(t, err)
	require.Len(t, rep.Unchecked, 1)
	assert.Contains(t, rep.Unchecked[0], "run #1")
	require.Len(t, rep.WouldGoToPerson, 1)
	assert.Contains(t, rep.WouldGoToPerson[0], ".github/workflows/ci.yml")
	assert.Equal(t, 1, rep.Unchanged)
}

func writePolicy(t *testing.T, body string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "policy.json")
	require.NoError(t, os.WriteFile(f, []byte(body), 0o600))
	return f
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
