package policy_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// facts builds the facts of a proposal from its files; the total is the sum of the files' lines.
func facts(files ...policy.File) policy.Facts {
	f := policy.Facts{Files: files}
	for _, x := range files {
		f.Lines += x.Lines
	}
	return f
}

func edited(path string, lines int) policy.File { return policy.File{Path: path, Lines: lines} }
func created(path string, lines int) policy.File {
	return policy.File{Path: path, New: true, Lines: lines}
}
func removed(path string, lines int) policy.File {
	return policy.File{Path: path, Deleted: true, Lines: lines}
}

func approveRule(r policy.Rule) *policy.Engine {
	r.Effect = policy.EffectApprove
	return &policy.Engine{Rules: []policy.Rule{r}}
}

// Each typed condition on its own: it matches, it does not, and an empty edit list satisfies it
// the way it satisfies allowed_extensions.
func TestEvaluate_typed_conditions(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rule  policy.Rule
		facts policy.Facts
		want  bool
	}{
		{"paths: every file under the glob", policy.Rule{Paths: []string{"docs/**"}}, facts(edited("docs/a.md", 1), edited("docs/x/b.md", 1)), true},
		{"paths: one file outside the glob", policy.Rule{Paths: []string{"docs/**"}}, facts(edited("docs/a.md", 1), edited(".github/x.md", 1)), false},
		{"paths: any of several globs", policy.Rule{Paths: []string{"docs/**", "**/*.md"}}, facts(edited("docs/a.txt", 1), edited("README.md", 1)), true},
		{"paths: a root file against a nested glob", policy.Rule{Paths: []string{"**/*.md"}}, facts(edited("README.md", 1)), true},
		{"paths: case folding in the name", policy.Rule{Paths: []string{"docs/**"}}, facts(edited("DOCS/A.md", 1)), true},
		{"paths: case folding in the glob", policy.Rule{Paths: []string{"Docs/**"}}, facts(edited("docs/a.md", 1)), true},
		{"paths: a deleted file is a changed file", policy.Rule{Paths: []string{"docs/**"}}, facts(removed("src/a.go", 3)), false},
		{"paths: a deleted file inside the glob", policy.Rule{Paths: []string{"docs/**"}}, facts(removed("docs/a.md", 3)), true},
		{"paths: empty edit list", policy.Rule{Paths: []string{"docs/**"}}, facts(), true},

		{"max_lines: at the limit", policy.Rule{MaxLines: 10}, facts(edited("a.go", 10)), true},
		{"max_lines: over the limit", policy.Rule{MaxLines: 10}, facts(edited("a.go", 6), edited("b.go", 5)), false},
		{"max_lines: empty edit list", policy.Rule{MaxLines: 1}, facts(), true},

		{"max_files: at the limit", policy.Rule{MaxFiles: 2}, facts(edited("a", 1), edited("b", 1)), true},
		{"max_files: over the limit", policy.Rule{MaxFiles: 2}, facts(edited("a", 1), edited("b", 1), edited("c", 1)), false},
		{"max_files: empty edit list", policy.Rule{MaxFiles: 1}, facts(), true},

		{"new_files_only: all new", policy.Rule{NewFilesOnly: true}, facts(created("a", 1), created("b", 1)), true},
		{"new_files_only: one existing file", policy.Rule{NewFilesOnly: true}, facts(created("a", 1), edited("b", 1)), false},
		{"new_files_only: a deleted file is not new", policy.Rule{NewFilesOnly: true}, facts(removed("a", 1)), false},
		{"new_files_only: empty edit list", policy.Rule{NewFilesOnly: true}, facts(), true},

		{"no_deletions: none deleted", policy.Rule{NoDeletions: true}, facts(created("a", 1), edited("b", 1)), true},
		{"no_deletions: one deleted", policy.Rule{NoDeletions: true}, facts(edited("a", 1), removed("b", 1)), false},
		{"no_deletions: empty edit list", policy.Rule{NoDeletions: true}, facts(), true},

		{"all conditions together, all met", policy.Rule{Paths: []string{"docs/**"}, MaxLines: 5, MaxFiles: 1, NewFilesOnly: true, NoDeletions: true},
			facts(created("docs/a.md", 5)), true},
		{"all conditions together, one missed", policy.Rule{Paths: []string{"docs/**"}, MaxLines: 5, MaxFiles: 1, NewFilesOnly: true, NoDeletions: true},
			facts(created("docs/a.md", 6)), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dec := approveRule(tc.rule).Evaluate(fileEditReq("file_edit", 1, "x"), tc.facts)
			assert.Equal(t, tc.want, dec.Matched)
			assert.Equal(t, tc.want, dec.Approved)
		})
	}
}

// The scenario of the approvals page: docs under docs/ are approved, a markdown file under .github/ goes to a person.
func TestEvaluate_docs_rule_leaves_the_github_directory_to_a_person(t *testing.T) {
	e := approveRule(policy.Rule{ActionTypes: []string{"file_edit"}, Paths: []string{"docs/**"}, AllowedExtensions: []string{".md"}})
	assert.True(t, e.Evaluate(fileEditReq("file_edit", 1, "docs/a.md"), facts(edited("docs/a.md", 2))).Approved)
	assert.False(t, e.Evaluate(fileEditReq("file_edit", 1, ".github/x.md"), facts(edited(".github/x.md", 2))).Matched)
}

// A rule whose conditions are all typed ones is a targeted rule, not a blanket deny.
func TestLoad_a_reject_rule_with_only_a_typed_condition_is_not_a_blanket_deny(t *testing.T) {
	for _, cond := range []string{`"paths":["secrets/**"]`, `"max_lines":500`, `"max_files":20`, `"new_files_only":true`, `"no_deletions":true`} {
		file := filepath.Join(t.TempDir(), "policy.json")
		require.NoError(t, os.WriteFile(file, []byte(`{"version":1,"rules":[{"effect":"reject",`+cond+`}]}`), 0o600))
		e, err := policy.LoadEngineFile(file)
		require.NoError(t, err, cond)
		require.Len(t, e.Rules, 1)
	}
	// and with none of them it still is one
	file := filepath.Join(t.TempDir(), "policy.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"rules":[{"effect":"reject"}]}`), 0o600))
	_, err := policy.LoadEngineFile(file)
	assert.ErrorContains(t, err, "blanket-deny")
}

func TestLoad_refuses_what_a_typed_field_cannot_mean(t *testing.T) {
	for name, rule := range map[string]string{
		"a glob that is not valid": `{"paths":["docs/[a"]}`,
		"an empty glob":            `{"paths":[""]}`,
		"a negative line limit":    `{"max_lines":-1}`,
		"a negative file limit":    `{"max_files":-3}`,
	} {
		file := filepath.Join(t.TempDir(), "policy.json")
		require.NoError(t, os.WriteFile(file, []byte(`{"rules":[`+rule+`]}`), 0o600))
		_, err := policy.LoadEngineFile(file)
		assert.Error(t, err, name)
	}
}

// Commands and tool calls are a person's decision under every rule shape: all subsets of the conditions,
// both effects, facts that satisfy every condition.
func TestEvaluate_no_generated_rule_matches_a_command_or_a_tool_call(t *testing.T) {
	rng := rand.New(rand.NewSource(13))
	everything := facts(created("docs/a.md", 1))
	for subset := 0; subset < 1<<9; subset++ {
		var r policy.Rule
		if subset&1 != 0 {
			r.ActionTypes = []string{"shell_exec", "tool_call", "file_edit"}
		}
		if subset&2 != 0 {
			r.MinConfidence = rng.Float64() / 2
		}
		if subset&4 != 0 {
			r.AllowedExtensions = []string{".md"}
		}
		if subset&8 != 0 {
			r.AgentNames = []string{"test-agent"}
		}
		if subset&16 != 0 {
			r.Paths = []string{"**"}
		}
		if subset&32 != 0 {
			r.MaxLines = 1 + rng.Intn(100)
		}
		if subset&64 != 0 {
			r.MaxFiles = 1 + rng.Intn(100)
		}
		r.NewFilesOnly = subset&128 != 0
		r.NoDeletions = subset&256 != 0
		for _, effect := range []policy.Effect{policy.EffectApprove, policy.EffectReject, ""} {
			r.Effect = effect
			e := &policy.Engine{Rules: []policy.Rule{r}}
			for _, at := range []string{domain.ActionShellExec, domain.ActionToolCall} {
				dec := e.Evaluate(fileEditReq(at, 1, "docs/a.md"), everything)
				assert.False(t, dec.Matched, "%s under %+v", at, r)
			}
		}
	}
}

// The previous release's policy parser, as a fixture: its Rule and Engine had these fields and
// it read the file with DisallowUnknownFields.
type previousRule struct {
	ActionTypes       []string `json:"action_types"`
	MinConfidence     float64  `json:"min_confidence"`
	AllowedExtensions []string `json:"allowed_extensions"`
	AgentNames        []string `json:"agent_names,omitempty"`
	Effect            string   `json:"effect"`
}

type previousEngine struct {
	Version          int            `json:"version"`
	Rules            []previousRule `json:"rules"`
	Limits           map[string]int `json:"limits"`
	AllowBlanketDeny bool           `json:"allow_blanket_deny"`
}

func previousParse(data []byte) error {
	var e previousEngine
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(&e)
}

func TestTypedFields_an_older_parser_refuses_the_file_and_the_version_stays_1(t *testing.T) {
	for _, cond := range []string{`"paths":["docs/**"]`, `"max_lines":10`, `"max_files":2`, `"new_files_only":true`, `"no_deletions":true`} {
		file := `{"version":1,"rules":[{"effect":"approve",` + cond + `}]}`
		assert.Error(t, previousParse([]byte(file)), "the previous release must refuse "+cond)
		e, err := policy.SnapshotFromBytes([]byte(file), true)
		require.NoError(t, err, cond)
		assert.Equal(t, 1, e.Engine.Version)
	}
	// a file without the new fields reads in both, with the same rule
	old := []byte(`{"version":1,"rules":[{"action_types":["file_edit"],"min_confidence":0.9,"allowed_extensions":[".md"],"effect":"approve"}]}`)
	require.NoError(t, previousParse(old))
	snap, err := policy.SnapshotFromBytes(old, true)
	require.NoError(t, err)
	assert.Equal(t, policy.Rule{ActionTypes: []string{"file_edit"}, MinConfidence: 0.9, AllowedExtensions: []string{".md"}, Effect: policy.EffectApprove}, snap.Engine.Rules[0])
}

// A rule written without the new fields serializes without them, so signing and digests of an
// existing file do not change.
func TestTypedFields_are_omitted_when_unset(t *testing.T) {
	b, err := json.Marshal(policy.Rule{ActionTypes: []string{"file_edit"}, Effect: policy.EffectApprove})
	require.NoError(t, err)
	for _, k := range []string{"paths", "max_lines", "max_files", "new_files_only", "no_deletions"} {
		assert.NotContains(t, string(b), fmt.Sprintf("%q", k))
	}
}
