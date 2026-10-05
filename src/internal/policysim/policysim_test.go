package policysim_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/policysim"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testPolicy = `{"rules":[
  {"action_types":["file_edit"],"allowed_extensions":[".env"],"effect":"reject"},
  {"action_types":["file_edit"],"allowed_extensions":[".md"],"effect":"approve"}],
 "limits":{"max_auto_approved":2}}`

func writePolicy(t *testing.T, body string) string {
	p := filepath.Join(t.TempDir(), "policy.json")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

func one(file, content, expect string) policysim.Proposal {
	return policysim.Proposal{Edits: []policysim.Edit{{File: file, Content: content}}, Expect: expect}
}

// TestRun_the_policy_decides_as_asserted: each outcome is observed through the
// real admission code, in order, with state: a limit that is reached, a guard that
// escalates a change a rule would approve, drift outside a story's scope, an
// edit the orchestrator refuses.
func TestRun_the_policy_decides_as_asserted(t *testing.T) {
	secret := "token = \"AKIAABCDEFGHIJKLMNOP\"\n"
	res, err := policysim.Run(context.Background(), writePolicy(t, testPolicy), policysim.File{Scenarios: []policysim.Scenario{
		{Name: "a rule approves docs", Proposals: []policysim.Proposal{one("docs/a.md", "hello\n", policysim.Approve)}},
		{Name: "source code goes to a person", Proposals: []policysim.Proposal{one("src/x.go", "package x\n", policysim.Human)}},
		{Name: "a rule rejects env files", Proposals: []policysim.Proposal{one(".env", "A=1\n", policysim.Reject)}},
		{Name: "the third automatic approval is a limit", Proposals: []policysim.Proposal{
			one("docs/1.md", "1\n", policysim.Approve), one("docs/2.md", "2\n", policysim.Approve), one("docs/3.md", "3\n", policysim.Human)}},
		{Name: "a guard escalates what a rule approves", Proposals: []policysim.Proposal{
			{Edits: []policysim.Edit{{File: "notes.md", Content: secret}}, Expect: policysim.Human, ReasonContains: "secret"}}},
		{Name: "outside the story's scope is drift", Scope: []string{"docs/**"}, Proposals: []policysim.Proposal{
			one("docs/in.md", "in\n", policysim.Approve),
			{Edits: []policysim.Edit{{File: "other.md", Content: "out\n"}}, Expect: policysim.Human, ReasonContains: "scope"}}},
		{Name: "an edit that cannot apply is refused", Proposals: []policysim.Proposal{
			{Edits: []policysim.Edit{{File: "README.md", Search: "not there\n", Replace: "x\n"}}, Expect: policysim.Refuse}}},
		{Name: "an existing file is edited", Base: map[string]string{"README.md": "old\n"}, Proposals: []policysim.Proposal{
			{Edits: []policysim.Edit{{File: "README.md", Search: "old\n", Replace: "new\n"}}, Expect: policysim.Approve}}},
	}})
	require.NoError(t, err)
	for _, r := range res {
		assert.True(t, r.Pass, "%s #%d: %s (%s)", r.Scenario, r.Index, r.Detail, r.Reason)
	}
	assert.Len(t, res, 11)
}

// TestRun_expectations_fail_both_ways: a forbidden approval and an unintended
// refusal each fail their expectation; a reason that is not there fails too.
func TestRun_expectations_fail_both_ways(t *testing.T) {
	res, err := policysim.Run(context.Background(), writePolicy(t, testPolicy), policysim.File{Scenarios: []policysim.Scenario{
		{Name: "a forbidden approval", Proposals: []policysim.Proposal{one("docs/a.md", "x\n", policysim.Human)}},           // the rule approves it
		{Name: "an unintended refusal", Proposals: []policysim.Proposal{one("src/x.go", "package x\n", policysim.Approve)}}, // a person is asked
		{Name: "an unintended reject", Proposals: []policysim.Proposal{one(".env", "A=1\n", policysim.Approve)}},
		{Name: "the wrong reason", Proposals: []policysim.Proposal{
			{Edits: []policysim.Edit{{File: "notes.md", Content: "AKIAABCDEFGHIJKLMNOP\n"}}, Expect: policysim.Human, ReasonContains: "dependencies"}}},
	}})
	require.NoError(t, err)
	require.Len(t, res, 4)
	for _, r := range res {
		assert.False(t, r.Pass, "%s should have failed", r.Scenario)
		assert.NotEmpty(t, r.Detail)
	}
	assert.Equal(t, policysim.Approve, res[0].Got)
	assert.Equal(t, policysim.Human, res[1].Got)
	assert.Equal(t, policysim.Reject, res[2].Got)
}

// TestLoad_an_inventory_that_asserts_nothing_is_an_error: a file cannot pass by
// saying nothing.
func TestLoad_an_inventory_that_asserts_nothing_is_an_error(t *testing.T) {
	for name, body := range map[string]string{
		"no scenarios":            `{"scenarios":[]}`,
		"empty object":            `{}`,
		"no proposals":            `{"scenarios":[{"name":"a"}]}`,
		"no edits":                `{"scenarios":[{"name":"a","proposals":[{"expect":"approve"}]}]}`,
		"unknown expectation":     `{"scenarios":[{"name":"a","proposals":[{"edits":[{"file":"a","content":"x"}],"expect":"maybe"}]}]}`,
		"no expectation":          `{"scenarios":[{"name":"a","proposals":[{"edits":[{"file":"a","content":"x"}]}]}]}`,
		"an edit of no kind":      `{"scenarios":[{"name":"a","proposals":[{"edits":[{"file":"a"}],"expect":"approve"}]}]}`,
		"an edit of two kinds":    `{"scenarios":[{"name":"a","proposals":[{"edits":[{"file":"a","content":"x","delete":true}],"expect":"approve"}]}]}`,
		"duplicate names":         `{"scenarios":[{"name":"a","proposals":[{"edits":[{"file":"a","content":"x"}],"expect":"approve"}]},{"name":"a","proposals":[{"edits":[{"file":"b","content":"x"}],"expect":"approve"}]}]}`,
		"an unknown field":        `{"scenarios":[{"name":"a","expectations":1,"proposals":[{"edits":[{"file":"a","content":"x"}],"expect":"approve"}]}]}`,
		"a misspelt human_answer": `{"scenarios":[{"name":"a","human_answer":"yes","proposals":[{"edits":[{"file":"a","content":"x"}],"expect":"approve"}]}]}`,
		"not JSON":                `scenarios`,
	} {
		p := filepath.Join(t.TempDir(), "s.json")
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
		_, err := policysim.Load(p)
		assert.Error(t, err, name)
	}
	p := filepath.Join(t.TempDir(), "ok.json")
	require.NoError(t, os.WriteFile(p, []byte(`{"scenarios":[{"name":"a","proposals":[{"edits":[{"file":"a","content":"x"}],"expect":"approve"}]}]}`), 0o600))
	_, err := policysim.Load(p)
	assert.NoError(t, err)
}

// TestRun_a_policy_that_does_not_load_is_an_error: the policy is read as a real run reads it.
func TestRun_a_policy_that_does_not_load_is_an_error(t *testing.T) {
	_, err := policysim.Run(context.Background(), writePolicy(t, `{"rules":[{"action_types":["file_edit"],"effect":"allow"}]}`), policysim.File{Scenarios: []policysim.Scenario{
		{Name: "a", Proposals: []policysim.Proposal{one("a.md", "x\n", policysim.Approve)}}}})
	assert.ErrorContains(t, err, "effect")
}

// TestRun_approve_in_scope_is_a_run_option: with the option, a change inside the
// story's scope is approved without a rule; without it a person is asked.
func TestRun_approve_in_scope_is_a_run_option(t *testing.T) {
	pol := writePolicy(t, `{"rules":[]}`)
	res, err := policysim.Run(context.Background(), pol, policysim.File{Scenarios: []policysim.Scenario{
		{Name: "in scope, option on", Scope: []string{"src/**"}, ApproveInScope: true,
			Proposals: []policysim.Proposal{one("src/a.go", "package a\n", policysim.Approve)}},
		{Name: "in scope, option off", Scope: []string{"src/**"},
			Proposals: []policysim.Proposal{one("src/a.go", "package a\n", policysim.Human)}},
		{Name: "outside scope, option on", Scope: []string{"src/**"}, ApproveInScope: true,
			Proposals: []policysim.Proposal{one("go.mod", "module x\n", policysim.Human)}},
	}})
	require.NoError(t, err)
	for _, r := range res {
		assert.True(t, r.Pass, "%s: %s (%s)", r.Scenario, r.Detail, r.Reason)
	}
}

func delivered(files ...string) *[]string { return &files }

// TestRun_a_checkpoint_goes_to_a_person_who_can_approve_or_reject: with the task approving what is
// in scope, checkpoint_every sends every third proposal to a person. Their answer is what the
// agent is told, and what reaches the branch is exactly what was approved (delivery and the
// certificate are asserted from Git and the chain, not from the decisions).
func TestRun_a_checkpoint_goes_to_a_person_who_can_approve_or_reject(t *testing.T) {
	pol := writePolicy(t, `{"rules":[],"limits":{"checkpoint_every":3}}`)
	proposals := func(third string) []policysim.Proposal {
		return []policysim.Proposal{
			one("src/a.go", "package a\n", policysim.Approve),
			one("src/b.go", "package b\n", policysim.Approve),
			{Edits: []policysim.Edit{{File: "src/c.go", Content: "package c\n"}}, Expect: policysim.Human, ReasonContains: "checkpoint", Then: third},
			one("src/d.go", "package d\n", policysim.Approve),
		}
	}
	res, err := policysim.Run(context.Background(), pol, policysim.File{Scenarios: []policysim.Scenario{
		{Name: "the person approves the checkpoint", Scope: []string{"src/**"}, ApproveInScope: true, HumanAnswer: "approve", Proposals: proposals("approved"),
			ExpectDelivered: delivered("src/a.go", "src/b.go", "src/c.go", "src/d.go"), ExpectCertificate: true},
		{Name: "the person rejects the checkpoint", Scope: []string{"src/**"}, ApproveInScope: true, HumanAnswer: "reject", Proposals: proposals("rejected"),
			ExpectDelivered: delivered("src/a.go", "src/b.go", "src/d.go"), ExpectCertificate: true},
		{Name: "a final review that is rejected delivers nothing", Scope: []string{"src/**"}, ApproveInScope: true, HumanAnswer: "approve", FinalReviewAnswer: "reject", Proposals: proposals("approved"),
			ExpectDelivered: delivered()},
	}})
	require.NoError(t, err)
	for _, r := range res {
		assert.True(t, r.Pass, "%s #%d: %s (%s)", r.Scenario, r.Index, r.Detail, r.Reason)
	}
	assert.Len(t, res, 15, "12 proposals and 3 deliveries")
}

// TestRun_evidence_decides_what_is_in_scope: with approve_on_evidence the explicit checks
// decide an in-scope change: passing evidence approves it with no person; failing evidence
// does not, it goes to a person, who is told which check failed, and their answer is final.
func TestRun_evidence_decides_what_is_in_scope(t *testing.T) {
	pol := writePolicy(t, `{"rules":[]}`)
	check := []string{`grep -q good src/a.txt`}
	scenario := func(name, answer, then string, deliver []string) policysim.Scenario {
		return policysim.Scenario{Name: name, Scope: []string{"src/**"}, ApproveOnEvidence: true, Checks: check, HumanAnswer: answer,
			Proposals: []policysim.Proposal{
				{Edits: []policysim.Edit{{File: "src/a.txt", Content: "good\n"}}, Expect: policysim.Approve, ReasonContains: "evidence"},
				{Edits: []policysim.Edit{{File: "src/a.txt", Search: "good\n", Replace: "broken\n"}}, Expect: policysim.Human, Then: then},
			},
			ExpectDelivered: &deliver, ExpectCertificate: len(deliver) > 0}
	}
	res, err := policysim.Run(context.Background(), pol, policysim.File{Scenarios: []policysim.Scenario{
		scenario("failing evidence goes to a person who rejects it", "reject", "rejected", []string{"src/a.txt"}),
		scenario("failing evidence goes to a person who approves it", "approve", "approved", []string{"src/a.txt"}),
	}})
	require.NoError(t, err)
	for _, r := range res {
		assert.True(t, r.Pass, "%s #%d: %s (%s)", r.Scenario, r.Index, r.Detail, r.Reason)
	}
	// the person who was asked is told the evidence failed
	assert.Contains(t, res[1].Reason, "grep", "the reason names the failing check")
}

// TestRun_wrong_delivery_and_answer_expectations_fail: expecting what did not happen fails, both ways.
func TestRun_wrong_delivery_and_answer_expectations_fail(t *testing.T) {
	pol := writePolicy(t, `{"rules":[{"action_types":["file_edit"],"allowed_extensions":[".md"],"effect":"approve"}]}`)
	res, err := policysim.Run(context.Background(), pol, policysim.File{Scenarios: []policysim.Scenario{
		{Name: "expects another delivery", Proposals: []policysim.Proposal{one("docs/a.md", "x\n", policysim.Approve)}, ExpectDelivered: delivered("docs/b.md")},
		{Name: "expects nothing delivered but a change was", Proposals: []policysim.Proposal{one("docs/a.md", "x\n", policysim.Approve)}, ExpectDelivered: delivered()},
		{Name: "expects a rejection the agent did not get", Proposals: []policysim.Proposal{
			{Edits: []policysim.Edit{{File: "docs/a.md", Content: "x\n"}}, Expect: policysim.Approve, Then: "rejected"}}},
	}})
	require.NoError(t, err)
	failed := map[string]bool{}
	for _, r := range res {
		if !r.Pass {
			failed[r.Scenario+fmt.Sprintf("/%d", r.Index)] = true
			assert.NotEmpty(t, r.Detail)
		}
	}
	assert.True(t, failed["expects another delivery/0"], "%+v", res)
	assert.True(t, failed["expects nothing delivered but a change was/0"], "%+v", res)
	assert.True(t, failed["expects a rejection the agent did not get/1"], "%+v", res)
	assert.Len(t, failed, 3, "and nothing else fails: the decisions themselves were as expected")
}
