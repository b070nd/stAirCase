package policysim_test

import (
	"context"
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
