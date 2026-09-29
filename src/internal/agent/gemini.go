package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/sandbox"
)

// Gemini runs Google's Gemini CLI (`gemini -p`) in the run's worktree as the
// run's agent, built from its documentation (geminicli.com/docs/hooks):
// BeforeTool hooks route every tool call through staircase like Claude Code's
// PreToolUse, so edits and commands are decided before they run and every
// other tool is refused.
//
// Gemini lets a call run when a hook fails in any way but exit code 2 or
// answers with something that is not JSON, and it cannot be told to ignore
// the user's and the repository's own hooks; so the hook bridge exits 2 on
// every failure, and a session whose SessionStart hook never arrives fails.
type Gemini struct {
	Prompt  string
	Model   string // optional -m
	Bin     string // default "gemini"
	HookBin string // the staircase program the hooks call; default: this program
}

// geminiRules tell the model how the governed session works.
const geminiRules = `Make file changes with write_file and replace: each one is reviewed before it is applied. ` +
	`Only files inside the project can be read or changed.`

// Run starts the hook endpoint, then Gemini, and waits for it.
func (g *Gemini) Run(ctx context.Context, env *orchestrator.AgentEnv) error {
	root, err := filepath.EvalSymlinks(env.Worktree)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	h := &hookServer{env: env, root: root, token: rand.Text(), pending: map[string]orchestrator.Approval{}, calls: map[string]*hookCall{},
		name: "gemini", label: "Gemini CLI", norm: geminiInput, out: geminiOutput}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	dir, err := os.MkdirTemp("", "staircase-gemini-") // outside the worktree
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	session, _ := json.Marshal(map[string]string{"url": "http://" + ln.Addr().String() + "/hook", "token": h.token})
	hookFile := filepath.Join(dir, "hook.json")
	if err := os.WriteFile(hookFile, session, 0o600); err != nil {
		return err
	}
	bin := g.HookBin
	if bin == "" {
		if bin, err = os.Executable(); err != nil {
			return err
		}
	}
	// Gemini "sanitizes" the environment of a hook, so the command names the
	// session file itself instead of relying on an inherited variable.
	settings := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settings, GeminiSettings(HookCommand(bin, "gemini", "--governed", "--file", hookFile)), 0o600); err != nil {
		return err
	}

	args := []string{"-p", g.Prompt + "\n\n" + geminiRules, "--approval-mode=yolo", "--output-format", "json"}
	if g.Model != "" {
		args = append(args, "-m", g.Model)
	}
	cmd := exec.CommandContext(ctx, orDefault(g.Bin, "gemini"), args...)
	// Gemini logs in itself (its cached login): sandbox.Env carries no key it
	// could hand to a command it runs.
	cmd.Dir, cmd.Env = root, append(sandbox.Env(), HookFileEnv+"="+hookFile, "GEMINI_CLI_SYSTEM_SETTINGS_PATH="+settings)
	cmd.WaitDelay = 5 * time.Second
	sandbox.KillGroup(cmd)
	var stdout bytes.Buffer
	stderr := &tail{max: 4096}
	cmd.Stdout, cmd.Stderr = &stdout, stderr
	runErr := cmd.Run()

	var res struct {
		Response string `json:"response"`
		Error    *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(stdout.Bytes(), &res)
	env.Emit(ctx, orchestrator.Usage{Agent: "gemini", Model: orDefault(g.Model, "gemini"), Content: preview(res.Response)})
	switch {
	case runErr != nil:
		return fmt.Errorf("gemini: %w: %s%s", runErr, preview(stdout.String()), stderr)
	case res.Error != nil:
		return fmt.Errorf("gemini reported an error: %s", preview(res.Error.Message))
	case !h.started.Load():
		return errors.New("gemini never ran staircase's hooks, so the session was not governed; nothing it did is kept")
	}
	return nil
}

// GeminiSettings is the settings file that routes Gemini CLI's tool calls
// through cmd: SessionStart (the handshake), BeforeTool (every tool) and
// AfterTool (what an approved edit or command left behind). Timeouts are in
// milliseconds and outlast any human approval.
func GeminiSettings(cmd string) []byte {
	hook := func(matcher string) []map[string]any {
		return []map[string]any{{"matcher": matcher, "hooks": []map[string]any{
			{"name": "staircase", "type": "command", "command": cmd, "timeout": hookTimeout * 1000}}}}
	}
	b, _ := json.Marshal(map[string]any{"hooks": map[string]any{
		"SessionStart": hook("*"),
		"BeforeTool":   hook("*"),
		"AfterTool":    hook("write_file|replace|run_shell_command"),
	}})
	return b
}

// geminiInput puts a Gemini CLI hook call in Claude Code's shape: BeforeTool
// is PreToolUse, and its tools and arguments are named as Claude Code's
// (write_file is Write, replace is Edit with allow_multiple as replace_all,
// directories are `path`). Tools with no counterpart keep their name and are
// refused as unavailable.
func geminiInput(in hookInput) hookInput {
	switch in.Event {
	case "BeforeTool":
		in.Event = "PreToolUse"
	case "AfterTool":
		in.Event = "PostToolUse"
	}
	tool, ok := map[string]string{"write_file": "Write", "replace": "Edit", "read_file": "Read", "list_directory": "LS",
		"glob": "Glob", "grep_search": "Grep", "search_file_content": "Grep", "run_shell_command": "Bash", "write_todos": "TodoWrite"}[in.Tool]
	if !ok {
		return in
	}
	var args map[string]any
	if json.Unmarshal(in.Input, &args) != nil {
		return in
	}
	if d, ok := args["dir_path"]; ok {
		args["path"] = d
	}
	if m, ok := args["allow_multiple"].(bool); ok && m {
		args["replace_all"] = true
	}
	in.Tool = tool
	in.Input, _ = json.Marshal(args)
	return in
}

// geminiOutput turns the gate's answer into Gemini CLI's: {"decision":"deny",
// "reason":…} to block (a tool call, or an approved edit that could not be
// kept), and plain JSON otherwise, since output that is not JSON lets the
// call run.
func geminiOutput(b []byte) []byte {
	var a struct {
		Specific struct {
			Decision string `json:"permissionDecision"`
			Reason   string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if json.Unmarshal(b, &a) != nil {
		return []byte(`{"decision":"deny","reason":"staircase: unreadable answer"}`)
	}
	switch {
	case a.Specific.Decision == "deny":
		out, _ := json.Marshal(map[string]string{"decision": "deny", "reason": a.Specific.Reason})
		return out
	case a.Decision == "block":
		out, _ := json.Marshal(map[string]string{"decision": "deny", "reason": a.Reason})
		return out
	}
	return []byte(`{"decision":"allow"}`)
}
