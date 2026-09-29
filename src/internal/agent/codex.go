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
	"strings"
	"time"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/sandbox"
)

// codexApp is where the ChatGPT app for macOS keeps its Codex CLI, newest
// layout first (0.158 moved it), used when no codex is on PATH.
var codexApp = []string{
	"/Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex",
	"/Applications/ChatGPT.app/Contents/Resources/codex",
}

// Codex runs OpenAI's Codex CLI (`codex exec`) in the run's worktree as the
// run's agent. Its hooks route every tool call through staircase:
//
//   - apply_patch edits become proposals decided before they are applied,
//     and the approved bytes are written again afterwards;
//   - shell commands run without a decision, in Codex's workspace-write
//     sandbox (no network, writes only in the worktree and temp folders),
//     and every file they change is reviewed afterwards: kept if approved,
//     reverted if not;
//   - every other tool is refused.
//
// Codex skips a hook nobody has reviewed without saying so, so the run passes
// --dangerously-bypass-hook-trust for its own hooks, and fails when the
// SessionStart hook never arrives: then the session was not governed.
type Codex struct {
	Prompt  string
	Model   string // optional -m
	Bin     string // default "codex" on PATH, else the ChatGPT app's
	HookBin string // the staircase program the hooks call; default: this program
}

// Run starts the hook endpoint, then Codex, and waits for it.
func (c *Codex) Run(ctx context.Context, env *orchestrator.AgentEnv) error {
	root, err := filepath.EvalSymlinks(env.Worktree)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	h := &hookServer{env: env, root: root, token: rand.Text(), codex: true, name: "codex", label: "Codex", pending: map[string]orchestrator.Approval{}, calls: map[string]*hookCall{}}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	dir, err := os.MkdirTemp("", "staircase-codex-") // outside the worktree
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
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
	hook := fmt.Sprintf(`[{matcher="*",hooks=[{type="command",command=%s,timeout=%d}]}]`,
		tomlString(HookCommand(bin, "codex", "--governed")), hookTimeout)
	args := []string{"exec", "-s", "workspace-write", "-c", `approval_policy="never"`, "--dangerously-bypass-hook-trust",
		"-c", "hooks.SessionStart=" + hook, "-c", "hooks.PreToolUse=" + hook, "-c", "hooks.PostToolUse=" + hook, "-c", "hooks.Stop=" + hook}
	if c.Model != "" {
		args = append(args, "-m", c.Model)
	}
	args = append(args, c.Prompt+"\n\n"+codexRules)

	cmd := exec.CommandContext(ctx, c.bin(), args...)
	cmd.Dir, cmd.Env = root, append(sandbox.Env(), HookFileEnv+"="+hookFile)
	cmd.WaitDelay = 5 * time.Second
	sandbox.KillGroup(cmd)
	var stdout bytes.Buffer
	stderr := &tail{max: 4096}
	cmd.Stdout, cmd.Stderr = &stdout, stderr // stdin stays empty: codex exec waits on an open one
	runErr := cmd.Run()
	env.Emit(ctx, orchestrator.Usage{Agent: "codex", Model: orDefault(c.Model, "codex"), Content: preview(stdout.String())})
	switch {
	case runErr != nil:
		return fmt.Errorf("codex: %w: %s%s", runErr, preview(stdout.String()), stderr)
	case !h.started.Load():
		return errors.New("codex never ran staircase's hooks, so the session was not governed; nothing it did is kept")
	}
	return nil
}

// codexRules tell the model how the governed session works.
const codexRules = `Make file changes with apply_patch: each one is reviewed before it is applied. ` +
	`Shell commands run in a sandbox without network access; files they change are reviewed afterwards and may be reverted.`

func (c *Codex) bin() string {
	if c.Bin != "" {
		return c.Bin
	}
	if _, err := exec.LookPath("codex"); err != nil {
		for _, app := range codexApp {
			if _, err := os.Stat(app); err == nil {
				return app
			}
		}
	}
	return "codex"
}

// preCodex decides one Codex tool call; an empty reason allows it.
func (h *hookServer) preCodex(ctx context.Context, in hookInput) string {
	var a struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(in.Input, &a)
	switch in.Tool {
	case "Bash", "update_plan": // commands run sandboxed and are reviewed afterwards (postCodexCommand)
		return ""
	case "apply_patch":
		edits, err := parsePatch(a.Command)
		if err != nil {
			return "staircase: " + err.Error()
		}
		for i, e := range edits {
			rel, err := h.rel(e.File)
			if err != nil {
				return "staircase: only files inside the project can be changed"
			}
			edits[i].File = rel
		}
		ap := h.env.Propose(ctx, domain.YieldRequest{AgentName: h.who(), ActionType: domain.ActionFileEdit,
			ProposedEdits: edits, ReasoningTrace: h.title() + " apply_patch", ConfidenceScore: 0.9})
		if !ap.Approved {
			return ap.Refusal()
		}
		h.mu.Lock()
		h.pending[in.pendingKey()] = ap
		h.mu.Unlock()
		return ""
	}
	return "staircase: " + in.Tool + " is not available in a governed run"
}

// postCodexCommand has the files a finished command changed reviewed; a
// non-empty result says they were rejected and reverted.
func (h *hookServer) postCodexCommand(ctx context.Context, in hookInput) string {
	var a struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(in.Input, &a)
	ap := h.env.ProposeWorktreeChanges(ctx, "codex", fmt.Sprintf("The command `%s` changed these files.", a.Command))
	if ap.Approved {
		return ""
	}
	return "staircase: the files this command changed were not approved and have been reverted: " + ap.Refusal()
}

// tomlString is s as a TOML basic string.
func tomlString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
