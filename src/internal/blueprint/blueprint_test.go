package blueprint_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/staircase-core/src/internal/blueprint"
	"github.com/b070nd/staircase-core/src/internal/plan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const good = `name: hello
supervisor: supervisor
agents:
  - {name: supervisor, model: demo/scripted, prompt: "You coordinate the work."}
  - {name: coder, model: demo/scripted, prompt_file: prompts/coder.md}
edges:
  - {from: supervisor, to: coder}
  - {from: coder, to: supervisor}
cases:
  - slug: greet
    prd_file: prd.md
    stories:
      - text: Create a greeting file
        scope: {allow: ["GREETING.md"], max_files: 1}
limits: {checkpoint_every: 5, max_files_changed: 10, max_scope_violations: 2, max_run_secs: 600}
`

func write(t *testing.T, dir string, files map[string]string) {
	for name, content := range files {
		p := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
}

func dir(t *testing.T, yaml string) string {
	d := t.TempDir()
	write(t, d, map[string]string{"blueprint.yaml": yaml, "prompts/coder.md": "You write code.", "prd.md": "Say hello."})
	return d
}

func TestLoad_resolves_files_and_hashes_content(t *testing.T) {
	d := dir(t, good)
	b, err := blueprint.Load(d)
	require.NoError(t, err)
	assert.Equal(t, "You write code.", b.Agents[1].Prompt)
	assert.Equal(t, "Say hello.", b.Cases[0].PRD)
	h1 := b.Hash()
	assert.Len(t, h1, 64)

	again, err := blueprint.Load(d)
	require.NoError(t, err)
	assert.Equal(t, h1, again.Hash(), "same files, same hash")

	write(t, d, map[string]string{"prompts/coder.md": "You write careful code."})
	edited, err := blueprint.Load(d)
	require.NoError(t, err)
	assert.NotEqual(t, h1, edited.Hash(), "a prompt edit is a new blueprint")

	back, err := blueprint.Parse(b.JSON())
	require.NoError(t, err)
	assert.Equal(t, h1, back.Hash(), "stored content round-trips to the same hash")
}

func TestLoad_refuses(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "secret.md")
	require.NoError(t, os.WriteFile(outside, []byte("x"), 0o600))
	for name, tc := range map[string]struct {
		yaml  string
		setup func(d string)
	}{
		"unknown field":         {yaml: good + "promtp: typo\n"},
		"prompt file escapes":   {yaml: replace(good, "prompts/coder.md", "../secret.md")},
		"absolute prompt file":  {yaml: replace(good, "prompts/coder.md", outside)},
		"symlink escapes":       {yaml: replace(good, "prompts/coder.md", "link.md"), setup: func(d string) { _ = os.Symlink(outside, filepath.Join(d, "link.md")) }},
		"prompt and file":       {yaml: replace(good, `prompt_file: prompts/coder.md`, `prompt_file: prompts/coder.md, prompt: x`)},
		"no prompt":             {yaml: replace(good, `, prompt: "You coordinate the work."`, ``)},
		"unknown supervisor":    {yaml: replace(good, "supervisor: supervisor\n", "supervisor: boss\n")},
		"edge to unknown agent": {yaml: replace(good, "{from: coder, to: supervisor}", "{from: coder, to: tester}")},
		"unknown model":         {yaml: replace(good, "model: demo/scripted, prompt_file", "model: nonsense, prompt_file")},
		"duplicate slug":        {yaml: replace(good, "limits:", "  - {slug: greet, prd: again}\nlimits:")},
		"missing name":          {yaml: replace(good, "name: hello\n", "")},
	} {
		t.Run(name, func(t *testing.T) {
			d := dir(t, tc.yaml)
			if tc.setup != nil {
				tc.setup(d)
			}
			_, err := blueprint.Load(d)
			assert.Error(t, err)
			t.Log(err)
		})
	}
}

// TestCheck_plan_against_blueprint: a compiled plan runs the blueprint only
// if it holds exactly the blueprint's topology, PRD and stories.
func TestCheck_plan_against_blueprint(t *testing.T) {
	b, err := blueprint.Load(dir(t, good))
	require.NoError(t, err)
	p := plan.Plan{BlueprintHash: b.Hash(), Supervisor: "supervisor", PRD: "Say hello.",
		Agents:  []plan.Agent{{Name: "supervisor", Role: "You coordinate the work.", Model: "demo/scripted"}, {Name: "coder", Role: "You write code.", Model: "demo/scripted"}},
		Edges:   []plan.Edge{{From: "supervisor", To: "coder"}, {From: "coder", To: "supervisor"}},
		Stories: []plan.Story{{ID: 7, Text: "Create a greeting file", Allow: []string{"GREETING.md"}, MaxFiles: 1}},
		Limits:  plan.Limits{CheckpointEvery: 5, MaxFilesChanged: 10, MaxScopeViolations: 2, MaxRunSecs: 600}}
	require.NoError(t, b.Check(p, "greet"))

	extra := p
	extra.Agents = append(append([]plan.Agent(nil), p.Agents...), plan.Agent{Name: "tester", Role: "x", Model: "demo/scripted"})
	assert.ErrorContains(t, b.Check(extra, "greet"), "agents")
	scope := p
	scope.Stories = []plan.Story{{Text: "Create a greeting file", Allow: []string{"**"}, MaxFiles: 1}}
	assert.ErrorContains(t, b.Check(scope, "greet"), "stories")
	limits := p
	limits.Limits.MaxScopeViolations = 0
	assert.ErrorContains(t, b.Check(limits, "greet"), "limits")
	prd := p
	prd.PRD = "Say goodbye."
	assert.ErrorContains(t, b.Check(prd, "greet"), "PRD")
	assert.ErrorContains(t, b.Check(p, "nope"), "no case")
}

func replace(s, old, new string) string {
	if !strings.Contains(s, old) {
		panic("not found: " + old)
	}
	return strings.Replace(s, old, new, 1)
}
