package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/sandbox"
)

// ClaudeCode runs Claude Code (`claude -p`) in the run's worktree as the run's
// agent. Claude Code's own tools do the work, but hooks route every tool call
// through staircase first: edits and shell commands become proposals the
// orchestrator decides and audits, reads are confined to the worktree, and
// every other tool is denied. After an approved edit, the approved bytes are
// written again, so Claude Code's edit semantics cannot change what is
// committed; finalize verify stays the backstop.
type ClaudeCode struct {
	Prompt  string
	Model   string // optional --model
	Bin     string // default "claude"
	HookBin string // the staircase program the hooks call; default: this program
}

// hookTimeout outlives any human approval: Claude Code lets the tool call
// proceed when a PreToolUse hook times out (30 s by default).
const hookTimeout = 24 * 60 * 60

// Run starts the hook endpoint, then Claude Code, and waits for it.
func (c *ClaudeCode) Run(ctx context.Context, env *orchestrator.AgentEnv) error {
	root, err := filepath.EvalSymlinks(env.Worktree)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	h := &hookServer{env: env, root: root, token: rand.Text(), pending: map[string]orchestrator.Approval{}, calls: map[string]*hookCall{}}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	dir, err := os.MkdirTemp("", "staircase-cc-") // outside the worktree: nothing lands in the repo
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	// The hooks find this run through a file only the user can read, so the
	// token never appears on a command line (see `staircase hook`).
	session, _ := json.Marshal(map[string]string{"url": "http://" + ln.Addr().String() + "/hook", "token": h.token})
	hookFile := filepath.Join(dir, "hook.json")
	if err := os.WriteFile(hookFile, session, 0o600); err != nil {
		return err
	}
	bin := c.HookBin
	if bin == "" {
		if bin, err = os.Executable(); err != nil {
			return err
		}
	}
	settings := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settings, hookSettings(HookCommand(bin, "claude-code", "--governed")), 0o600); err != nil {
		return err
	}

	// Only staircase's settings load: user, project and local settings could
	// bring hooks and MCP servers that act outside governance (F82). Checked
	// against Claude Code 2.1.236: "" loads none of them, --settings still loads.
	args := []string{"-p", c.Prompt, "--settings", settings, "--setting-sources", "", "--strict-mcp-config",
		"--output-format", "json"}
	if c.Model != "" {
		args = append(args, "--model", c.Model)
	}
	cmd := exec.CommandContext(ctx, orDefault(c.Bin, "claude"), args...)
	// ponytail: sandbox.Env carries no LLM key, so Claude Code must be logged in
	// (keychain under HOME); pass ANTHROPIC_API_KEY via a secret if needed.
	cmd.Dir, cmd.Env = root, append(sandbox.Env(), HookFileEnv+"="+hookFile)
	cmd.WaitDelay = 5 * time.Second
	sandbox.KillGroup(cmd)
	var stdout bytes.Buffer
	stderr := &tail{max: 4096}
	cmd.Stdout, cmd.Stderr = &stdout, stderr
	runErr := cmd.Run()

	var res struct {
		IsError bool   `json:"is_error"`
		Result  string `json:"result"`
		Usage   struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(stdout.Bytes(), &res) == nil {
		env.Emit(ctx, orchestrator.Usage{Agent: "claude-code", Model: orDefault(c.Model, "claude-code"),
			InputTokens: res.Usage.InputTokens, OutputTokens: res.Usage.OutputTokens, Content: preview(res.Result)})
	}
	switch {
	case runErr != nil:
		return fmt.Errorf("claude code: %w: %s%s", runErr, preview(res.Result), stderr)
	case res.IsError:
		return fmt.Errorf("claude code reported an error: %s", preview(res.Result))
	}
	return nil
}

// HookCommand is the command a hook runs: `staircase hook`, which passes the
// call to the run and blocks (exit 2) on every failure. mode is --governed
// for the hooks a session brings and --require for a company's managed hook.
// It is the same on every run and holds no secret; the shell that runs hooks
// gets the program path quoted.
func HookCommand(bin, agentName, mode string) string {
	return "'" + strings.ReplaceAll(bin, "'", `'\''`) + "' hook " + agentName + " " + mode
}

// hookSettings is the settings file that routes Claude Code's tool calls
// through cmd.
func hookSettings(cmd string) []byte {
	hook := func(matcher string) []map[string]any {
		return []map[string]any{{"matcher": matcher,
			"hooks": []map[string]any{{"type": "command", "command": cmd, "timeout": hookTimeout}}}}
	}
	b, _ := json.Marshal(map[string]any{"hooks": map[string]any{
		"PreToolUse":  hook("*"),
		"PostToolUse": hook("Edit|Write"),
		"Stop":        hook(""),
	}})
	return b
}

type hookServer struct {
	env   *orchestrator.AgentEnv
	root  string // the worktree, symlinks resolved
	token string
	codex bool // Codex's tools and rules instead of Claude Code's (see codex.go)

	started atomic.Bool // a SessionStart hook arrived: the agent runs staircase's hooks

	mu      sync.Mutex
	pending map[string]orchestrator.Approval // tool_use_id → approved edit, until PostToolUse
	calls   map[string]*hookCall             // event/tool_use_id → its one answer
}

type hookInput struct {
	Event     string          `json:"hook_event_name"`
	Tool      string          `json:"tool_name"`
	Input     json.RawMessage `json:"tool_input"`
	ToolUseID string          `json:"tool_use_id"`
}

func (h *hookServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+h.token)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var in hookInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if in.ToolUseID == "" {
		_, _ = w.Write(h.answer(r.Context(), in))
		return
	}
	// With a company's managed hook and the session's own both installed, the
	// agent calls each for the same tool call: decide it once, answer both.
	key := in.Event + "/" + in.ToolUseID
	h.mu.Lock()
	c, seen := h.calls[key]
	if !seen {
		c = &hookCall{done: make(chan struct{})}
		h.calls[key] = c
	}
	h.mu.Unlock()
	if !seen {
		c.answer = h.answer(r.Context(), in)
		close(c.done)
	}
	<-c.done
	_, _ = w.Write(c.answer)
}

// hookCall is one tool call's answer, shared by every hook that asks for it.
type hookCall struct {
	done   chan struct{}
	answer []byte
}

// answer decides one hook call and returns the reply in the agent's format.
func (h *hookServer) answer(ctx context.Context, in hookInput) []byte {
	reply := func(v any) []byte { b, _ := json.Marshal(v); return b }
	switch in.Event {
	case "SessionStart":
		h.started.Store(true)
	case "PreToolUse":
		pre := h.pre
		if h.codex {
			pre = h.preCodex
		}
		reason := pre(ctx, in)
		decision := "allow"
		if reason != "" {
			decision = "deny"
		}
		return reply(map[string]any{"hookSpecificOutput": map[string]any{
			"hookEventName": "PreToolUse", "permissionDecision": decision, "permissionDecisionReason": reason}})
	case "Stop": // the definition of done: the checks must pass first
		if reason := h.env.Done(ctx); reason != "" {
			return reply(map[string]any{"decision": "block", "reason": reason})
		}
	case "PostToolUse":
		h.mu.Lock()
		ap, ok := h.pending[in.ToolUseID]
		delete(h.pending, in.ToolUseID)
		h.mu.Unlock()
		if ok {
			if err := ap.Apply(h.root); err != nil { // finalize verify fails the run on any divergence
				return reply(map[string]any{"decision": "block", "reason": "staircase: " + err.Error()})
			}
		}
		if h.codex && in.Tool == "Bash" {
			if reason := h.postCodexCommand(ctx, in); reason != "" {
				return reply(map[string]any{"decision": "block", "reason": reason})
			}
		}
	}
	return []byte("{}")
}

// pre decides one tool call; an empty reason allows it.
func (h *hookServer) pre(ctx context.Context, in hookInput) string {
	var a struct {
		FilePath   string `json:"file_path"`
		Path       string `json:"path"`
		Content    string `json:"content"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
		Command    string `json:"command"`
		Pattern    string `json:"pattern"`
	}
	if err := json.Unmarshal(in.Input, &a); err != nil {
		return "staircase: unreadable tool input"
	}
	switch in.Tool {
	case "TodoWrite":
		return ""
	case "Read", "Glob", "Grep", "LS":
		paths := []string{orDefault(a.FilePath, a.Path)}
		if in.Tool == "Glob" && filepath.IsAbs(a.Pattern) { // an absolute pattern names its own root
			paths = append(paths, a.Pattern[:strings.IndexAny(a.Pattern+"*", "*?[{")])
		}
		for _, p := range paths {
			if p == "" {
				continue
			}
			if _, err := h.rel(p); err != nil {
				return "staircase: only files inside the project can be read"
			}
		}
		return ""
	case "Bash":
		if ap := h.env.ProposeShell(ctx, "claude-code", "Claude Code Bash", ".", a.Command); !ap.Approved {
			return ap.Refusal()
		}
		return ""
	case "Write", "Edit":
		rel, err := h.rel(a.FilePath)
		if err != nil {
			return "staircase: only files inside the project can be changed"
		}
		if in.Tool == "Write" {
			return h.proposeEdit(ctx, in, domain.ProposedEdit{File: rel, SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: a.Content})
		}
		if a.ReplaceAll {
			return "staircase: replace_all is not supported; edit each occurrence on its own"
		}
		// Claude Code needs old_string to match exactly once; the orchestrator
		// replaces the first match. Refuse the ambiguous case before a human sees it.
		cur, err := h.readInside(rel)
		if err != nil || strings.Count(string(cur), a.OldString) != 1 {
			return "staircase: old_string must match exactly once in the file"
		}
		return h.proposeEdit(ctx, in, domain.ProposedEdit{File: rel, SearchBlock: a.OldString, ReplaceBlock: a.NewString})
	}
	return "staircase: " + in.Tool + " is not available in a governed run"
}

// proposeEdit asks for an edit and keeps its approval until PostToolUse.
func (h *hookServer) proposeEdit(ctx context.Context, in hookInput, e domain.ProposedEdit) string {
	ap := h.env.ProposeEdit(ctx, "claude-code", "Claude Code "+in.Tool, e)
	if !ap.Approved {
		return ap.Refusal()
	}
	h.mu.Lock()
	h.pending[in.ToolUseID] = ap
	h.mu.Unlock()
	return ""
}

// readInside reads a worktree file through os.Root, which refuses any path
// leaving the worktree when the file is opened - so a symlink swapped in after
// rel checked the path cannot redirect the read.
func (h *hookServer) readInside(rel string) ([]byte, error) {
	root, err := os.OpenRoot(h.root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.ReadFile(filepath.FromSlash(rel))
}

// rel maps a path Claude Code names (absolute, or relative to the worktree) to
// a slash path inside the worktree, resolving symlinks in its existing part.
func (h *hookServer) rel(p string) (string, error) {
	if !filepath.IsAbs(p) {
		p = filepath.Join(h.root, p)
	}
	dir, rest := filepath.Clean(p), ""
	for {
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			dir = filepath.Join(r, rest)
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errEscape
		}
		rest, dir = filepath.Join(filepath.Base(dir), rest), parent
	}
	rel, err := filepath.Rel(h.root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errEscape
	}
	return filepath.ToSlash(rel), nil
}
