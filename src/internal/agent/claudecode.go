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
	"time"

	"github.com/b070nd/staircase-core/src/internal/domain"
	"github.com/b070nd/staircase-core/src/internal/orchestrator"
)

// ClaudeCode runs Claude Code (`claude -p`) in the run's worktree as the run's
// agent. Claude Code's own tools do the work, but hooks route every tool call
// through staircase first: edits and shell commands become proposals the
// orchestrator decides and audits, reads are confined to the worktree, and
// every other tool is denied. After an approved edit, the approved bytes are
// written again, so Claude Code's edit semantics cannot change what is
// committed; finalize verify stays the backstop.
type ClaudeCode struct {
	Prompt string
	Model  string // optional --model
	Bin    string // default "claude"
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
	h := &hookServer{env: env, root: root, token: rand.Text(), pending: map[string]orchestrator.Approval{}}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	dir, err := os.MkdirTemp("", "staircase-cc-") // outside the worktree: nothing lands in the repo
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	settings := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settings, hookSettings("http://"+ln.Addr().String()+"/hook", h.token), 0o600); err != nil {
		return err
	}

	args := []string{"-p", c.Prompt, "--settings", settings, "--output-format", "json"}
	if c.Model != "" {
		args = append(args, "--model", c.Model)
	}
	cmd := exec.CommandContext(ctx, orDefault(c.Bin, "claude"), args...)
	// ponytail: shellEnv carries no LLM key, so Claude Code must be logged in
	// (keychain under HOME); pass ANTHROPIC_API_KEY via a secret if needed.
	cmd.Dir, cmd.Env = root, shellEnv()
	cmd.WaitDelay = 5 * time.Second
	killProcessGroup(cmd)
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

// hookCommand is the command a hook runs: it posts the hook input to url and
// prints the answer. Any failure exits 2, which blocks the tool call.
func hookCommand(url, token string) string {
	return fmt.Sprintf("curl -sSf -H 'Authorization: Bearer %s' --data-binary @- %s || exit 2", token, url)
}

// hookSettings is the settings file that routes Claude Code's tool calls to
// url. Every hook failure exits 2, which blocks the tool call: any other
// non-zero exit would let it proceed.
func hookSettings(url, token string) []byte {
	cmd := hookCommand(url, token)
	hook := func(matcher string) []map[string]any {
		return []map[string]any{{"matcher": matcher,
			"hooks": []map[string]any{{"type": "command", "command": cmd, "timeout": hookTimeout}}}}
	}
	b, _ := json.Marshal(map[string]any{"hooks": map[string]any{
		"PreToolUse":  hook("*"),
		"PostToolUse": hook("Edit|Write"),
	}})
	return b
}

type hookServer struct {
	env   *orchestrator.AgentEnv
	root  string // the worktree, symlinks resolved
	token string

	mu      sync.Mutex
	pending map[string]orchestrator.Approval // tool_use_id → approved edit, until PostToolUse
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
	switch in.Event {
	case "PreToolUse":
		reason := h.pre(r.Context(), in)
		decision := "allow"
		if reason != "" {
			decision = "deny"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"hookSpecificOutput": map[string]any{
			"hookEventName": "PreToolUse", "permissionDecision": decision, "permissionDecisionReason": reason}})
	case "PostToolUse":
		h.mu.Lock()
		ap, ok := h.pending[in.ToolUseID]
		delete(h.pending, in.ToolUseID)
		h.mu.Unlock()
		if ok {
			if err := ap.Apply(h.root); err != nil { // finalize verify fails the run on any divergence
				_ = json.NewEncoder(w).Encode(map[string]any{"decision": "block", "reason": "staircase: " + err.Error()})
				return
			}
		}
		_, _ = w.Write([]byte("{}"))
	default:
		_, _ = w.Write([]byte("{}"))
	}
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
		cur, err := os.ReadFile(filepath.Join(h.root, filepath.FromSlash(rel)))
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
