package history

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPayloads_keep_the_keys_runs_write (ADR 0010, Q3): each payload as today's writers produce it (the "type" key, which every
// event carries, aside) reads into its type with nothing lost, and the type writes it back with the same keys. A renamed key fails
// here before it can make an old chain read differently.
func TestPayloads_keep_the_keys_runs_write(t *testing.T) {
	for name, tc := range map[string]struct {
		written string
		into    any
	}{
		"run_bound": {`{"base_sha":"b","branch":"refs/heads/staircase/run-1","worktree":"/w","topology_version":2,"plan_digest":"p",
			"blueprint_hash":"bp","harness":"claude-code","ack_drift":4,"away_brief":{"version":1,"sha256":"ab"},
			"away_commands":{"commands":["go generate ./..."],"sha256":"cd"},"away_tools":{"tools":["mcp__probe__note"],"sha256":"ef"},
			"terms_sha256":"t","resumes_session":"s","resumes_run":1}`, &Bound{}},
		"yield_request": {`{"type":"yield_request","agent_name":"staircase","action_type":"file_edit",
			"proposed_edits":[{"file":"a.txt","search_block":"","replace_block":"x"}],"reasoning_trace":"the owner adopted parked proposal 1 after a prototype",
			"confidence_score":0,"adopts":1}`, &Request{}},
		"a truncated yield_request": {`{"type":"","agent_name":"","action_type":"file_edit","reasoning_trace":"","confidence_score":0,"truncated":true}`, &Request{}},
		"yield_decided by a person": {`{"seq":3,"source":"operator","agent":"coder","action_type":"file_edit","approved":true,"feedback":"ok",
			"request_sha256":"r","base_sha":"b","request_entry":"e","files":{"a.txt":"1"},"paths":["a.txt"],"drift":"outside","guard":"secret",
			"review_after":true,"sandboxed":true,"adopts":1,"decide_ms":1200,"lines":4,"signature":"s","signed_by":"dev","key":"SHA256:k",
			"trusted":true,"signed_text":"approve"}`, &Decided{}},
		"yield_decided on evidence": {`{"seq":4,"source":"evidence","agent":"coder","action_type":"file_edit","approved":false,"feedback":"",
			"request_sha256":"r","base_sha":"b","unconsumed":true,"evidence":{"checks":[{"command":"go test"}]},"validator_state":{"approvals":1}}`, &Decided{}},
		"yield_decided signed by an untrusted key": {`{"seq":6,"source":"operator","agent":"coder","action_type":"file_edit","approved":true,
			"feedback":"","request_sha256":"r","base_sha":"b","signature":"s","signed_by":"x","key":"SHA256:k","trusted":false,"signed_text":"t"}`, &Decided{}},
		"yield_decided parked": {`{"seq":5,"source":"parked","agent":"coder","action_type":"shell_exec","approved":false,"feedback":"parked",
			"request_sha256":"r","base_sha":"b","parked_why":"it needs a person's decision","signature_refused":"unsigned"}`, &Decided{}},
		"run_resumed": {`{"segment":2,"generation":"g","approvals":[{"seq":1,"source":"policy","request_sha256":"r"}],"next_seq":2,"policy":"p",
			"plan_digest":"d","undecided_requests":1,"ack_drift":true,"elapsed_secs":30,"context":"native_resume","journal_dropped":1,"ack_unknown":1}`, &Resumed{}},
		"launch_claimed":   {`{"attempt":2,"generation":"0f1e","segment":2}`, &LaunchClaimed{}},
		"launch_ended":     {`{"attempt":2,"generation":"0f1e","outcome":"unknown_after_restart"}`, &LaunchEnded{}},
		"policy_snapshot":  {`{"digest":"d","signed":true}`, &PolicySnapshot{}},
		"initiator_signed": {`{"principal":"dev@example.com","signature":"c2ln"}`, &InitiatorSigned{}},
		"agent_session":    {`{"agent":"claude-code","id":"s","mode":"started"}`, &AgentSession{}},
		"state_emit":       {`{"active_agent":"coder","state":{"model":"m","input_tokens":1,"output_tokens":2}}`, &StateEmit{}},
		"shell_ran":        {`{"agent":"coder","command":"make","sandboxed":true}`, &ShellRan{}},
		"phase_checked": {`{"tree":"t","checks":[{"command":"go test","exit_code":0,"sandboxed":true,"output_sha256":"o"}],"passed":true,
			"failed":""}`, &PhaseChecked{}},
		"awaiting_review":    {`{"tree":"t","files":{"a":"1"},"phase_checked":"e","parked":2}`, &AwaitingReview{}},
		"review_rejected":    {`{"awaiting_review":"e","feedback":"no"}`, &ReviewDecided{}},
		"commit_prepared":    {`{"commit":"c","base":"b","tree":"t","branch":"staircase/run-1"}`, &Delivered{}},
		"certificate_issued": {`{"commit":"c","cal":3,"envelope_sha256":"e"}`, &Delivered{}},
		"run_recovered":      {`{"commit":"c","proposals":2,"final_review":true,"forced":true,"evidence_errors":["ledger"]}`, &Delivered{}},
		"recovery_repaired":  {`{"commit":"c","evidence_errors":["certificate"]}`, &Delivered{}},
		"run_path":           {`{"phases":["PRE_FLIGHT","BRANCH_CREATE","BRANCH_RESTORE"],"status":"FAILED"}`, &Ended{}},
		"an older run_path":  {`{"phases":["PRE_FLIGHT"]}`, &Ended{}},
		"drift_halt":         {`{"reason":"the run exceeded its 60 s limit"}`, &DriftHalt{}},
		"evidence_failed":    {`{"commit":"c","errors":["ledger: disk full"]}`, &EvidenceFailed{}},
		"certificate_failed": {`{"commit":"c","error":"no key"}`, &CertificateFailed{}},
	} {
		t.Run(name, func(t *testing.T) {
			v := reflect.New(reflect.TypeOf(tc.into).Elem()).Interface()
			dec := json.NewDecoder(strings.NewReader(tc.written))
			dec.DisallowUnknownFields() // every key a run writes has its field
			require.NoError(t, dec.Decode(v))
			again, err := json.Marshal(v)
			require.NoError(t, err)
			assert.JSONEq(t, tc.written, string(again), "the type writes the keys it read")
		})
	}
}

// TestEncode: a payload is its value's keys and the event's type, in key order, as runs have always written them; a value
// that is not an object is refused.
func TestEncode(t *testing.T) {
	got, err := Encode("yield_decided", map[string]any{"seq": 1, "approved": true})
	require.NoError(t, err)
	assert.Equal(t, `{"approved":true,"seq":1,"type":"yield_decided"}`, got)
	got, err = Encode("policy_snapshot", PolicySnapshot{Digest: "d", Signed: true})
	require.NoError(t, err)
	assert.Equal(t, `{"digest":"d","signed":true,"type":"policy_snapshot"}`, got)
	_, err = Encode("x", []int{1})
	assert.Error(t, err)
}
