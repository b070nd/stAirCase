package history_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/chain"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// events lists a value of every event a run writes, by the name it is written under. An event type added to history without a line
// here has no sample in the fixture that it reproduces, and TestWriter_reproduces_the_fixture fails for the name it lacks.
var events = map[string][]func() any{
	"run_bound":                  {func() any { return &history.Bound{} }},
	"yield_request":              {func() any { return &history.Request{} }},
	"shell_exec_rejected":        {func() any { return &history.ShellExecRejected{} }},
	"yield_decided":              {func() any { return &history.Decided{} }},
	"run_resumed":                {func() any { return &history.Resumed{} }},
	"launch_claimed":             {func() any { return &history.LaunchClaimed{} }},
	"launch_ended":               {func() any { return &history.LaunchEnded{} }},
	"launch_acknowledged":        {func() any { return &history.LaunchAcknowledged{} }},
	"policy_snapshot":            {func() any { return &history.PolicySnapshot{} }},
	"initiator_signed":           {func() any { return &history.InitiatorSigned{} }},
	"task_agreed":                {func() any { return &history.TaskAgreed{} }},
	"agent_session":              {func() any { return &history.AgentSession{} }},
	"state_emit":                 {func() any { return &history.StateEmit{} }},
	"shell_ran":                  {func() any { return &history.ShellRan{} }},
	"phase_checked":              {func() any { return &history.PhaseChecked{} }},
	"awaiting_review":            {func() any { return &history.AwaitingReview{} }},
	"delivery_authorized":        {func() any { return &history.DeliveryAuthorized{} }},
	"review_accepted":            {func() any { return &history.ReviewAccepted{} }},
	"review_rejected":            {func() any { return &history.ReviewRejected{} }},
	"commit_prepared":            {func() any { return &history.CommitPrepared{} }},
	"certificate_issued":         {func() any { return &history.CertificateIssued{} }},
	"run_recovered":              {func() any { return &history.RunRecovered{} }},
	"review_delivered":           {func() any { return &history.ReviewDelivered{} }},
	"recovery_repaired":          {func() any { return &history.RecoveryRepaired{} }},
	"run_path":                   {func() any { return &history.Ended{} }},
	"drift_halt":                 {func() any { return &history.DriftHalt{} }},
	"drift_report":               {func() any { return &history.DriftReport{} }},
	"agent_unresponsive":         {func() any { return &history.AgentUnresponsive{} }},
	"approval_path_escape":       {func() any { return &history.ApprovalPathEscape{} }},
	"approval_content_mismatch":  {func() any { return &history.ApprovalContentMismatch{} }},
	"run_branch_moved":           {func() any { return &history.RunBranchMoved{} }},
	"unapproved_worktree_change": {func() any { return &history.UnapprovedWorktreeChange{} }},
	"signal_rated":               {func() any { return &history.SignalRated{} }, func() any { return &history.SignalFailed{} }},
	"check_ran":                  {func() any { return &history.CheckRan{} }},
	"done_checked":               {func() any { return &history.DoneChecked{} }, func() any { return &history.DoneCheckFailed{} }},
	"certificate_failed":         {func() any { return &history.CertificateFailed{} }},
	"evidence_failed":            {func() any { return &history.EvidenceFailed{} }},
	"rolled_back":                {func() any { return &history.RolledBack{} }},
}

func fixtureEntries(t *testing.T) []domain.RunEventLog {
	raw, err := os.ReadFile(filepath.Join("..", "chain", "testdata", "fixture_chain.json"))
	require.NoError(t, err)
	var f struct {
		Entries []domain.RunEventLog `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(raw, &f))
	return f.Entries
}

// without removes the "type" key every Encode payload carries, so the sample reads into a type that does not list it.
func without(t *testing.T, payload, key string) string {
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(payload), &m))
	delete(m, key)
	b, err := json.Marshal(m)
	require.NoError(t, err)
	return string(b)
}

func decode(payload string, v any) error {
	dec := json.NewDecoder(strings.NewReader(payload))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// write appends e through a Writer with s into a fresh store and returns what the chain holds.
func write(t *testing.T, s history.Settings, e history.Event) string {
	store, runID := rowsStore(t)
	logs, err := store.ListEventLogs(runID)
	require.NoError(t, err)
	before := len(logs)
	require.NoError(t, history.NewWriter(store, runID, s).Append(e))
	logs, err = store.ListEventLogs(runID)
	require.NoError(t, err)
	require.Len(t, logs, before+1)
	return logs[len(logs)-1].Payload
}

// TestWriter_reproduces_the_fixture (ADR 0014, section 2): for every entry of the fixture, the code before this change wrote it,
// and the Writer, given the typed value that entry reads into, writes the same bytes. A key renamed, a field dropped or added, an
// order changed: the entry named here is the one that differs.
func TestWriter_reproduces_the_fixture(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range fixtureEntries(t) {
		if strings.HasPrefix(e.Payload, `{"truncated":true`) { // the note of what an oversize request left out: TestWriter_cap
			seen[e.EventType] = true
			continue
		}
		cands, ok := events[e.EventType]
		require.True(t, ok, "entry %d: no type is listed for %s", e.ID, e.EventType)
		seen[e.EventType] = true
		var tried []string
		reproduced := false
		for _, mk := range cands {
			v := mk()
			err := decode(e.Payload, v)
			if err != nil {
				err = decode(without(t, e.Payload, "type"), v)
			}
			if err != nil {
				tried = append(tried, err.Error())
				continue
			}
			ev, isEvent := v.(history.Event)
			if !isEvent {
				ev = eventOf(t, v)
			}
			for _, s := range []history.Settings{{}, {Plain: true}} {
				if write(t, s, ev) == e.Payload {
					reproduced = true
				}
			}
		}
		assert.True(t, reproduced, "entry %d (%s) is not reproduced: %s\n%v", e.ID, e.EventType, e.Payload, tried)
	}
	for name := range events {
		assert.True(t, seen[name], "the fixture has no entry of %s", name)
	}
}

// eventOf turns the pointer a payload was read into back into the event value.
func eventOf(t *testing.T, v any) history.Event {
	t.Helper()
	ev, ok := reflect.ValueOf(v).Elem().Interface().(history.Event)
	require.True(t, ok, "%T is not an event", v)
	return ev
}

func TestWriter_refuses_an_entry_over_the_ceiling_and_writes_nothing(t *testing.T) {
	store, runID := rowsStore(t)
	files := map[string]string{}
	for i := 0; i < 12000; i++ { // about 100 bytes each: over the 1 MiB an entry holds
		files[fmt.Sprintf("internal/generated/pkg%06d/file.go", i)] = strings.Repeat("d", 64)
	}
	before, err := store.ListEventLogs(runID)
	require.NoError(t, err)
	err = history.NewWriter(store, runID, history.Settings{}).Append(history.AwaitingReview{Tree: "t", Files: files, PhaseChecked: "e"})
	require.ErrorIs(t, err, chain.ErrPayloadTooLarge)
	assert.Contains(t, err.Error(), "awaiting_review")
	after, err := store.ListEventLogs(runID)
	require.NoError(t, err)
	assert.Len(t, after, len(before), "nothing was written")
}

// TestWriter_cap (ADR 0014, section 1): what an agent supplies is capped, scrubbed and noted as the agent host has always done, and
// the note is the one the fixture holds.
func TestWriter_cap(t *testing.T) {
	big := history.Request{YieldRequest: domain.YieldRequest{AgentName: "coder", ActionType: domain.ActionFileEdit,
		ProposedEdits: []domain.ProposedEdit{{File: "a.txt", SearchBlock: "(new file)", ReplaceBlock: strings.Repeat("é", 40000)}}}}
	got := write(t, history.Settings{Plain: true, Cap: 64 << 10}, big)
	assert.Regexp(t, `^\{"truncated":true,"action_type":"file_edit","bytes":\d+,"sha256":"[0-9a-f]{64}"\}$`, got)
	for _, e := range fixtureEntries(t) {
		if strings.HasPrefix(e.Payload, `{"truncated":true`) {
			assert.Regexp(t, `^\{"truncated":true,"action_type":"file_edit","bytes":\d+,"sha256":"[0-9a-f]{64}"\}$`, e.Payload)
		}
	}

	scrubbed := write(t, history.Settings{Plain: true, Scrub: func(b []byte) []byte { return bytes.ReplaceAll(b, []byte("hunter2"), []byte("<redacted>")) }},
		history.ShellRan{Agent: "coder", Command: "echo hunter2", Sandboxed: true})
	assert.Equal(t, `{"agent":"coder","command":"echo <redacted>","sandboxed":true}`, scrubbed)

	var debug bytes.Buffer
	_ = write(t, history.Settings{Plain: true, Debug: &debug}, history.ShellRan{Agent: "coder", Command: "ls", Sandboxed: false})
	assert.Contains(t, debug.String(), ` shell_ran {"agent":"coder","command":"ls","sandboxed":false}`)
}

// TestWriter_if: a conditional write is checked under the append lock, and refused means nothing is written and the error is the
// caller's own.
func TestWriter_if(t *testing.T) {
	store, runID := rowsStore(t)
	w := history.NewWriter(store, runID, history.Settings{})
	stop := errors.New("the run ended")
	before, _ := store.ListEventLogs(runID)
	err := w.AppendIf(history.DriftHalt{Reason: "r"}, func() error { return stop })
	require.ErrorIs(t, err, stop)
	failed := false
	w2 := history.NewWriter(store, runID, history.Settings{OnFail: func(string, error) { failed = true }})
	require.ErrorIs(t, w2.AppendIf(history.DriftHalt{Reason: "r"}, func() error { return stop }), stop)
	assert.False(t, failed, "a refusal is not a failure of the store")
	after, _ := store.ListEventLogs(runID)
	assert.Len(t, after, len(before))
	h, err := w.AppendHash(history.DriftHalt{Reason: "r"})
	require.NoError(t, err)
	last, _ := store.ListEventLogs(runID)
	assert.Equal(t, last[len(last)-1].EventHash, h, "AppendHash returns the new entry")
}
