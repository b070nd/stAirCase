package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Provenance of a fixture: where its tool call came from. Only "synthetic" ones
// exist so far (written from each host's documentation); a "captured" one is the
// output of a real host, with the version that made it.
const (
	synthetic   = "synthetic"
	capturedOut = "captured, logged out"
	capturedLiv = "captured, live"
)

// normalizeFixture is one tool call of one host version, and what the governed
// tools must read from it.
type normalizeFixture struct {
	name, host, version, provenance string
	norm                            func(hookInput) hookInput
	in                              hookInput
	wantTool                        string
	wantArgs                        map[string]any // nil: the call is ambiguous and refused
}

func call(tool, args string) hookInput {
	return hookInput{Event: "BeforeTool", Tool: tool, Input: json.RawMessage(args)}
}

// TestNormalization_fixtures: each host names tools and arguments its own way.
// What is approved and what the host runs must be read from the same key: a name
// and its alias that agree give one deterministic call; ones that disagree give no
// call at all.
func TestNormalization_fixtures(t *testing.T) {
	gem := func(name, tool, args, wantTool string, want map[string]any) normalizeFixture {
		return normalizeFixture{name, "gemini", "0.46.0", synthetic, geminiInput, call(tool, args), wantTool, want}
	}
	oc := func(name, tool, args, wantTool string, want map[string]any) normalizeFixture {
		return normalizeFixture{name, "opencode", "unreleased: not installed", synthetic, opencodeInput, call(tool, args), wantTool, want}
	}
	for _, f := range []normalizeFixture{
		gem("write", "write_file", `{"file_path":"/w/a.txt","content":"x"}`, "Write", map[string]any{"file_path": "/w/a.txt", "content": "x"}),
		gem("read by the old name", "read_file", `{"absolute_path":"/w/a.txt"}`, "Read", map[string]any{"file_path": "/w/a.txt"}),
		gem("read, both names agree", "read_file", `{"absolute_path":"/w/a.txt","file_path":"/w/a.txt"}`, "Read", map[string]any{"file_path": "/w/a.txt"}),
		gem("read, the names disagree", "read_file", `{"absolute_path":"/etc/hosts","file_path":"/w/a.txt"}`, ambiguousTool, nil),
		gem("list", "list_directory", `{"dir_path":"/w"}`, "LS", map[string]any{"path": "/w"}),
		gem("list, the names disagree", "list_directory", `{"dir_path":"/etc","path":"/w"}`, ambiguousTool, nil),
		gem("replace everywhere", "replace", `{"file_path":"/w/a","old_string":"a","new_string":"b","allow_multiple":true}`, "Edit",
			map[string]any{"file_path": "/w/a", "old_string": "a", "new_string": "b", "replace_all": true}),
		gem("replace once", "replace", `{"file_path":"/w/a","old_string":"a","new_string":"b","allow_multiple":false}`, "Edit",
			map[string]any{"file_path": "/w/a", "old_string": "a", "new_string": "b", "replace_all": false}),
		gem("replace, the flags disagree", "replace", `{"file_path":"/w/a","old_string":"a","new_string":"b","allow_multiple":false,"replace_all":true}`, ambiguousTool, nil),
		gem("a tool with no counterpart keeps its name", "web_fetch", `{"prompt":"x"}`, "web_fetch", map[string]any{"prompt": "x"}),
		oc("edit", "edit", `{"filePath":"/w/a","oldString":"a","newString":"b","replaceAll":false}`, "Edit",
			map[string]any{"file_path": "/w/a", "old_string": "a", "new_string": "b", "replace_all": false}),
		oc("edit, the names disagree", "edit", `{"filePath":"/w/a","file_path":"/w/other","oldString":"a","newString":"b"}`, ambiguousTool, nil),
		oc("patch", "apply_patch", `{"patchText":"*** Begin Patch\n*** End Patch"}`, "apply_patch", map[string]any{"command": "*** Begin Patch\n*** End Patch"}),
		oc("write", "write", `{"filePath":"/w/a","content":"x"}`, "Write", map[string]any{"file_path": "/w/a", "content": "x"}),
	} {
		t.Run(f.host+"/"+f.name, func(t *testing.T) {
			assert.Contains(t, []string{synthetic, capturedOut, capturedLiv}, f.provenance)
			got := f.norm(f.in)
			assert.Equal(t, f.wantTool, got.Tool)
			if f.wantArgs == nil {
				reason := (&hookServer{}).pre(context.Background(), got)
				assert.Contains(t, reason, "different values", "an ambiguous call is refused with the reason")
				return
			}
			var args map[string]any
			require.NoError(t, json.Unmarshal(got.Input, &args))
			assert.Equal(t, f.wantArgs, args)
		})
	}
}

// TestPre_refuses_what_it_cannot_read_unambiguously: Claude Code's own arguments.
func TestPre_refuses_what_it_cannot_read_unambiguously(t *testing.T) {
	h := &hookServer{root: t.TempDir()}
	for name, in := range map[string]hookInput{
		"Read with file_path and path that differ": {Tool: "Read", Input: json.RawMessage(`{"file_path":"/w/a","path":"/etc/hosts"}`)},
		"Write that names no file":                 {Tool: "Write", Input: json.RawMessage(`{"content":"x"}`)},
		"Edit that names no file":                  {Tool: "Edit", Input: json.RawMessage(`{"old_string":"a","new_string":"b"}`)},
		"a path of the wrong type":                 {Tool: "Write", Input: json.RawMessage(`{"file_path":42,"content":"x"}`)},
		"input that is not an object":              {Tool: "Write", Input: json.RawMessage(`"x"`)},
	} {
		assert.NotEmpty(t, h.pre(context.Background(), in), name)
	}
}
