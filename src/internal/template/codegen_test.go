package template_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tmpl "github.com/b070nd/staircase-core/src/internal/template"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── PyStr ────────────────────────────────────────────────────────────────────

func TestPyStr_plain(t *testing.T) {
	assert.Equal(t, `"hello"`, tmpl.PyStr("hello"))
}

func TestPyStr_backslash(t *testing.T) {
	assert.Equal(t, `"a\\b"`, tmpl.PyStr(`a\b`))
}

func TestPyStr_double_quote(t *testing.T) {
	assert.Equal(t, `"a\"b"`, tmpl.PyStr(`a"b`))
}

func TestPyStr_newline(t *testing.T) {
	assert.Equal(t, `"a\nb"`, tmpl.PyStr("a\nb"))
}

func TestPyStr_carriage_return(t *testing.T) {
	assert.Equal(t, `"a\rb"`, tmpl.PyStr("a\rb"))
}

func TestPyStr_tab(t *testing.T) {
	assert.Equal(t, `"a\tb"`, tmpl.PyStr("a\tb"))
}

func TestPyStr_all_controls_in_one(t *testing.T) {
	in := "a\\\"\r\n\tb"
	out := tmpl.PyStr(in)
	assert.Contains(t, out, `\\`)
	assert.Contains(t, out, `\"`)
	assert.Contains(t, out, `\r`)
	assert.Contains(t, out, `\n`)
	assert.Contains(t, out, `\t`)
}

// ─── PyIdent ──────────────────────────────────────────────────────────────────

func TestPyIdent_passthrough_clean_name(t *testing.T) {
	assert.Equal(t, "hello_world", tmpl.PyIdent("hello_world"))
}

func TestPyIdent_hyphen_becomes_underscore(t *testing.T) {
	assert.Equal(t, "hello_world", tmpl.PyIdent("hello-world"))
}

func TestPyIdent_space_becomes_underscore(t *testing.T) {
	assert.Equal(t, "my_agent", tmpl.PyIdent("my agent"))
}

func TestPyIdent_leading_digit_replaced(t *testing.T) {
	result := tmpl.PyIdent("1agent")
	assert.True(t, strings.HasPrefix(result, "_"),
		"leading digit must produce a leading underscore, got %q", result)
}

func TestPyIdent_empty_returns_sentinel(t *testing.T) {
	assert.Equal(t, "_empty", tmpl.PyIdent(""))
}

func TestPyIdent_all_special_chars(t *testing.T) {
	assert.Equal(t, "___", tmpl.PyIdent("---"))
}

var pyKeywords = []string{
	"False", "None", "True",
	"and", "as", "assert", "async", "await",
	"break", "class", "continue", "def", "del",
	"elif", "else", "except", "finally", "for",
	"from", "global", "if", "import", "in",
	"is", "lambda", "nonlocal", "not", "or",
	"pass", "raise", "return", "try", "while",
	"with", "yield",
}

func TestPyIdent_all_python_keywords_get_suffix(t *testing.T) {
	for _, kw := range pyKeywords {
		result := tmpl.PyIdent(kw)
		assert.Equal(t, kw+"_", result, "keyword %q must get trailing underscore", kw)
	}
}

// ─── BuildConditionalGroups ───────────────────────────────────────────────────

func TestBuildConditionalGroups_empty_edges(t *testing.T) {
	groups := tmpl.BuildConditionalGroups(nil, "supervisor")
	assert.Empty(t, groups)
}

func TestBuildConditionalGroups_unconditional_edges_excluded(t *testing.T) {
	edges := []tmpl.EdgeParams{
		{From: "a", To: "b", Condition: ""},
	}
	groups := tmpl.BuildConditionalGroups(edges, "supervisor")
	assert.Empty(t, groups)
}

func TestBuildConditionalGroups_supervisor_flagged(t *testing.T) {
	edges := []tmpl.EdgeParams{
		{From: "supervisor", To: "worker", Condition: "go"},
	}
	groups := tmpl.BuildConditionalGroups(edges, "supervisor")
	require.Len(t, groups, 1)
	assert.True(t, groups[0].FromIsSupervisor)
	assert.Equal(t, "supervisor", groups[0].From)
}

func TestBuildConditionalGroups_non_supervisor_not_flagged(t *testing.T) {
	edges := []tmpl.EdgeParams{
		{From: "worker", To: "supervisor", Condition: "done"},
	}
	groups := tmpl.BuildConditionalGroups(edges, "supervisor")
	require.Len(t, groups, 1)
	assert.False(t, groups[0].FromIsSupervisor)
}

func TestBuildConditionalGroups_end_sentinels(t *testing.T) {
	edges := []tmpl.EdgeParams{
		{From: "a", To: "END", Condition: "finish"},
		{From: "a", To: "__end__", Condition: "abort"},
	}
	groups := tmpl.BuildConditionalGroups(edges, "supervisor")
	require.Len(t, groups, 1)
	require.Len(t, groups[0].Routes, 2)
	assert.True(t, groups[0].Routes[0].ToIsEND)
	assert.True(t, groups[0].Routes[1].ToIsEND)
}

func TestBuildConditionalGroups_non_end_not_flagged(t *testing.T) {
	edges := []tmpl.EdgeParams{
		{From: "a", To: "b", Condition: "cond"},
	}
	groups := tmpl.BuildConditionalGroups(edges, "sup")
	require.Len(t, groups, 1)
	require.Len(t, groups[0].Routes, 1)
	assert.False(t, groups[0].Routes[0].ToIsEND)
}

func TestBuildConditionalGroups_multiple_routes_grouped(t *testing.T) {
	edges := []tmpl.EdgeParams{
		{From: "a", To: "b", Condition: "x"},
		{From: "a", To: "c", Condition: "y"},
	}
	groups := tmpl.BuildConditionalGroups(edges, "sup")
	require.Len(t, groups, 1)
	assert.Len(t, groups[0].Routes, 2)
}

func TestBuildConditionalGroups_separate_sources_separate_groups(t *testing.T) {
	edges := []tmpl.EdgeParams{
		{From: "a", To: "c", Condition: "x"},
		{From: "b", To: "c", Condition: "y"},
	}
	groups := tmpl.BuildConditionalGroups(edges, "sup")
	assert.Len(t, groups, 2)
}

func TestBuildConditionalGroups_fromIdent_set(t *testing.T) {
	edges := []tmpl.EdgeParams{
		{From: "my-agent", To: "next", Condition: "cond"},
	}
	groups := tmpl.BuildConditionalGroups(edges, "sup")
	require.Len(t, groups, 1)
	assert.Equal(t, "my_agent", groups[0].FromIdent, "FromIdent must be PyIdent(From)")
}

// ─── GenerateGraphExec ────────────────────────────────────────────────────────

func minimalParams() tmpl.GraphExecParams {
	return tmpl.GraphExecParams{
		RunID:          1,
		CaseID:         1,
		ProjectPath:    "/tmp/project",
		PRDContext:     "Build X",
		RepoContext:    "src/",
		SupervisorName: "supervisor",
		CheckpointType: "memory",
		Agents: []tmpl.AgentParams{
			{Name: "supervisor", Role: "You orchestrate agents", Model: "claude-opus-4-6"},
			{Name: "worker", Role: "You do work", Model: "claude-haiku-4-6"},
		},
		Edges: []tmpl.EdgeParams{
			{From: "supervisor", To: "worker"},
			{From: "worker", To: "supervisor"},
		},
	}
}

func TestGenerateGraphExec_creates_output_file(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "exec.py")

	require.NoError(t, tmpl.GenerateGraphExec(out, minimalParams()))
	_, err := os.Stat(out)
	assert.NoError(t, err, "output file must exist")
}

func TestGenerateGraphExec_file_mode_0600(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "exec.py")
	require.NoError(t, tmpl.GenerateGraphExec(out, minimalParams()))

	info, err := os.Stat(out)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestGenerateGraphExec_contains_all_agent_nodes(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "exec.py")
	require.NoError(t, tmpl.GenerateGraphExec(out, minimalParams()))

	content := mustReadFile(t, out)
	assert.Contains(t, content, "def _node_supervisor")
	assert.Contains(t, content, "def _node_worker")
}

func TestGenerateGraphExec_sets_entry_point(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "exec.py")
	require.NoError(t, tmpl.GenerateGraphExec(out, minimalParams()))

	content := mustReadFile(t, out)
	assert.Contains(t, content, `_g.set_entry_point("supervisor")`)
}

func TestGenerateGraphExec_outgoing_conditions_inject_route_parsing(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "exec.py")

	params := minimalParams()
	params.Edges = []tmpl.EdgeParams{
		{From: "supervisor", To: "worker"},
		{From: "worker", To: "supervisor", Condition: "done"},
		{From: "worker", To: "END", Condition: "abort"},
	}

	require.NoError(t, tmpl.GenerateGraphExec(out, params))
	content := mustReadFile(t, out)

	assert.Contains(t, content, "ROUTE:", "routing instruction must be in worker system prompt")
	assert.Contains(t, content, `"done"`, "condition label must appear in path map")
	assert.Contains(t, content, `"abort"`, "condition label must appear in path map")
	assert.Contains(t, content, "_re.search", "ROUTE: extraction regex must be present")
}

func TestGenerateGraphExec_no_route_parsing_without_conditions(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "exec.py")
	require.NoError(t, tmpl.GenerateGraphExec(out, minimalParams()))

	content := mustReadFile(t, out)
	workerBlock := extractNodeBlock(content, "_node_worker")
	assert.NotContains(t, workerBlock, "_re.search",
		"unconditional agent must not have ROUTE: regex")
}

func TestGenerateGraphExec_supervisor_fan_out(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "exec.py")
	require.NoError(t, tmpl.GenerateGraphExec(out, minimalParams()))

	content := mustReadFile(t, out)
	assert.Contains(t, content, "_supervisor_route", "supervisor must use fan-out router")
}

func TestGenerateGraphExec_keyword_agent_name_sanitised(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "exec.py")

	params := minimalParams()
	params.Agents = append(params.Agents, tmpl.AgentParams{
		Name: "while", Role: "I am a loop", Model: "claude-haiku-4-6",
	})

	require.NoError(t, tmpl.GenerateGraphExec(out, params))
	content := mustReadFile(t, out)
	assert.Contains(t, content, "_node_while_")
}

func TestGenerateGraphExec_no_outgoing_conditions_means_no_pyset_in_node(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "exec.py")
	require.NoError(t, tmpl.GenerateGraphExec(out, minimalParams()))

	content := mustReadFile(t, out)
	assert.NotContains(t, content, "OutgoingConditions",
		"OutgoingConditions is a Go field, must not leak into generated Python")
}

func TestGenerateGraphExec_list_dir_tool_present(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "exec.py")
	require.NoError(t, tmpl.GenerateGraphExec(out, minimalParams()))

	content := mustReadFile(t, out)
	assert.Contains(t, content, "def list_dir", "generated script must include the list_dir tool")
	assert.Contains(t, content, "_BUILTIN_TOOLS", "generated script must declare _BUILTIN_TOOLS")
	assert.Contains(t, content, "list_dir", "list_dir must appear in _BUILTIN_TOOLS")
}

func TestGenerateGraphExec_read_file_tool_present(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "exec.py")
	require.NoError(t, tmpl.GenerateGraphExec(out, minimalParams()))

	content := mustReadFile(t, out)
	assert.Contains(t, content, "def read_file", "generated script must include the read_file tool")
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

func extractNodeBlock(content, nodeName string) string {
	lines := strings.Split(content, "\n")
	start := -1
	for i, l := range lines {
		if strings.Contains(l, "def "+nodeName) {
			start = i
			break
		}
	}
	if start == -1 {
		return ""
	}
	var buf strings.Builder
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "def ") {
			break
		}
		buf.WriteString(lines[i])
		buf.WriteByte('\n')
	}
	return buf.String()
}
