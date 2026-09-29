package agent_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/agent"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeOpenCodeJS stands in for `opencode run` as its documentation describes
// it (opencode.ai/docs/plugins): it loads the plugin from OPENCODE_CONFIG_DIR,
// which is run here for real (under Node), and calls its tool.execute.before
// and tool.execute.after hooks with {tool, sessionID, callID} and {args}; a
// hook that throws blocks the call. FAKE_OPENCODE=nohooks loads nothing.
const fakeOpenCodeJS = `
import fs from "node:fs"; import path from "node:path"; import os from "node:os";
import { execSync } from "node:child_process"; import { pathToFileURL } from "node:url";
if (process.env.FAKE_OPENCODE_ARGS) fs.writeFileSync(process.env.FAKE_OPENCODE_ARGS, JSON.stringify({args: process.argv.slice(2), config: process.env.OPENCODE_CONFIG_CONTENT || ""}));
if (process.env.FAKE_OPENCODE === "nohooks") { fs.writeFileSync("hello.txt", "ungoverned\n"); process.exit(0); }
const src = fs.readFileSync(path.join(process.env.OPENCODE_CONFIG_DIR, "plugins", "staircase.js"), "utf8");
const tmp = path.join(os.tmpdir(), "sc-plugin-" + process.pid + ".mjs");
fs.writeFileSync(tmp, src);
const mod = await import(pathToFileURL(tmp).href);
const hooks = await mod.Staircase({ directory: process.cwd() });
if (process.env.FAKE_OPENCODE === "dead") fs.writeFileSync(process.env.STAIRCASE_HOOK_FILE, JSON.stringify({ url: "http://127.0.0.1:1/hook", token: "x" }));
const calls = JSON.parse(fs.readFileSync(process.env.FAKE_OPENCODE_SCRIPT, "utf8").replaceAll("$WT", process.cwd()));
const log = fs.createWriteStream(process.env.FAKE_OPENCODE_LOG);
let n = 0;
for (const c of calls) {
  n++;
  const input = { tool: c.tool, sessionID: "s1", callID: "call" + n };
  let decision = "allow", reason = "";
  try { await hooks["tool.execute.before"](input, { args: c.input }); } catch (e) { decision = "deny"; reason = String(e.message); }
  log.write(c.tool + " " + decision + " " + reason.replaceAll("\n", " ") + "\n");
  if (decision === "deny") continue;
  const a = c.input;
  if (c.tool === "write") fs.writeFileSync(a.filePath, c.mangle ? a.content.replaceAll("\n", "\r\n") : a.content);
  if (c.tool === "edit") fs.writeFileSync(a.filePath, fs.readFileSync(a.filePath, "utf8").replace(a.oldString, a.newString));
  if (c.tool === "bash") execSync(a.command);
  if (c.tool === "apply_patch") fs.writeFileSync("hello.txt", "hello\n");
  if (["write", "edit", "bash", "apply_patch"].includes(c.tool)) await hooks["tool.execute.after"]({ ...input, args: a }, { title: "", output: "ok", metadata: {} });
}
log.end();
console.log("done");
`

func openCodeBin(t *testing.T, mode string, calls []fakeCall) (bin, logFile, argsFile string) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is needed to run the generated plugin")
	}
	script, err := json.Marshal(calls)
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "script.json"), script, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fake.mjs"), []byte(fakeOpenCodeJS), 0o600))
	logFile, argsFile, bin = filepath.Join(dir, "decisions.log"), filepath.Join(dir, "args.json"), filepath.Join(dir, "opencode")
	require.NoError(t, os.WriteFile(bin, fmt.Appendf(nil, "#!/bin/sh\nFAKE_OPENCODE=%s FAKE_OPENCODE_SCRIPT=%s FAKE_OPENCODE_LOG=%s FAKE_OPENCODE_ARGS=%s exec node %s \"$@\"\n",
		mode, filepath.Join(dir, "script.json"), logFile, argsFile, filepath.Join(dir, "fake.mjs")), 0o755))
	return bin, logFile, argsFile
}

// TestOpenCode_every_tool_call_is_governed runs the generated plugin: approved
// edits and patches land byte for byte, everything else is refused by a
// thrown error before it runs, and OpenCode's own prompts and other config are
// out of the way.
func TestOpenCode_every_tool_call_is_governed(t *testing.T) {
	abs := func(p string) string { return "$WT/" + p }
	patch := "*** Begin Patch\n*** Add File: hello.txt\n+hello\n*** End Patch\n"
	bin, logFile, argsFile := openCodeBin(t, "1", []fakeCall{
		{Tool: "write", Input: map[string]any{"filePath": abs("new.txt"), "content": "hello\n"}, Mangle: true},
		{Tool: "edit", Input: map[string]any{"filePath": abs("f.txt"), "oldString": "b\n", "newString": "B\n"}},
		{Tool: "edit", Input: map[string]any{"filePath": abs("f.txt"), "oldString": "a", "newString": "X"}}, // ambiguous
		{Tool: "edit", Input: map[string]any{"filePath": abs("f.txt"), "oldString": "b", "newString": "c", "replaceAll": true}},
		{Tool: "read", Input: map[string]any{"filePath": abs("f.txt")}},
		{Tool: "read", Input: map[string]any{"filePath": "/etc/hosts"}},
		{Tool: "glob", Input: map[string]any{"pattern": "/etc/*"}},
		{Tool: "write", Input: map[string]any{"filePath": abs("../escape.txt"), "content": "x"}},
		{Tool: "bash", Input: map[string]any{"command": "touch shell.txt"}},
		{Tool: "webfetch", Input: map[string]any{"url": "https://example.com"}},
		{Tool: "todowrite", Input: map[string]any{"todos": []string{"a"}}},
		{Tool: "apply_patch", Input: map[string]any{"patchText": patch}},
	})
	r := runtest.Run(t, runtest.Options{
		Base:  map[string]runtest.File{"f.txt": {Content: "a\nb\na2\n", Mode: 0o644}},
		Agent: &agent.OpenCode{Prompt: "p", Bin: bin},
	})
	require.NoError(t, r.Err)
	log, err := os.ReadFile(logFile)
	require.NoError(t, err)
	decisions := strings.Split(strings.TrimSpace(string(log)), "\n")
	want := []string{"write allow", "edit allow", "edit deny", "edit deny", "read allow", "read deny", "glob deny", "write deny",
		"bash deny", "webfetch deny", "todowrite allow", "apply_patch allow"}
	require.Len(t, decisions, len(want), string(log))
	for i, w := range want {
		assert.True(t, strings.HasPrefix(decisions[i], w), "call %d: %s", i, decisions[i])
	}
	assert.Contains(t, decisions[8], "--allow-shell-exec")

	for path, want := range map[string]string{"new.txt": "hello\n", "f.txt": "a\nB\na2\n", "hello.txt": "hello\n"} {
		got, err := r.OnBranch(path)
		require.NoError(t, err, path)
		assert.Equal(t, want, got, path)
	}

	var seen struct {
		Args   []string
		Config string
	}
	b, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &seen))
	assert.Equal(t, "run", seen.Args[0])
	var cfg struct {
		Permission map[string]string `json:"permission"`
	}
	require.NoError(t, json.Unmarshal([]byte(seen.Config), &cfg), seen.Config)
	assert.Equal(t, "allow", cfg.Permission["*"], "stAirCase's plugin decides; OpenCode's own prompts would stall a headless run")
	assert.Equal(t, "deny", cfg.Permission["external_directory"])
}

// TestOpenCode_without_its_plugin_nothing_is_kept: an OpenCode that never
// loaded the plugin was not governed; the run fails and commits nothing.
func TestOpenCode_without_its_plugin_nothing_is_kept(t *testing.T) {
	bin, _, _ := openCodeBin(t, "nohooks", nil)
	r := runtest.Run(t, runtest.Options{Agent: &agent.OpenCode{Prompt: "p", Bin: bin}})
	require.Error(t, r.Err)
	assert.Contains(t, r.Err.Error(), "never ran staircase's hooks")
	assert.Empty(t, r.Run.GitCommitHash)
}

// TestOpenCode_plugin_fails_closed: when the run cannot be reached after the
// plugin loaded, the plugin refuses the call instead of letting it through,
// and nothing is written.
func TestOpenCode_plugin_fails_closed(t *testing.T) {
	bin, logFile, _ := openCodeBin(t, "dead", []fakeCall{
		{Tool: "write", Input: map[string]any{"filePath": "$WT/x.txt", "content": "x\n"}},
	})
	r := runtest.Run(t, runtest.Options{Agent: &agent.OpenCode{Prompt: "p", Bin: bin}})
	require.NoError(t, r.Err)
	log, err := os.ReadFile(logFile)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(log), "write deny"), string(log))
	assert.Empty(t, r.Run.GitCommitHash)
	src := string(agent.OpenCodePlugin("/no/such/session.json"))
	assert.NotContains(t, src, "catch (", "no error is swallowed: any failure blocks")
}
