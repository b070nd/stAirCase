package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/obs"
	"github.com/b070nd/stAirCase/src/internal/persistence"
)

// Agent is what a run executes in its worktree, inside the staircase process:
// the LLM runtime, or a scripted stand-in in tests. It changes the repository
// only through env: the orchestrator decides every proposal, audits it, and
// later commits only what was approved.
type Agent interface {
	Run(ctx context.Context, env *AgentEnv) error
}

// AgentFunc adapts a function to Agent.
type AgentFunc func(ctx context.Context, env *AgentEnv) error

// Run calls f.
func (f AgentFunc) Run(ctx context.Context, env *AgentEnv) error { return f(ctx, env) }

// AgentEnv is the run as its agent sees it.
type AgentEnv struct {
	Worktree     string        // the run's git worktree: the agent's project root
	AllowShell   bool          // shell_exec may be proposed (--allow-shell-exec)
	Sandbox      string        // approved commands: "auto", "required" or "off" (--sandbox)
	Workspace    string        // the stAirCase workspace: keys and secrets, hidden from commands
	CheckTimeout time.Duration // how long one check may run (0: the default)
	Checks       []string      // run before the agent may end (Done) and on the commit (--check)

	doneMu       sync.Mutex
	doneAttempts int

	proposals chan<- proposal
	usage     chan<- Usage
	host      *agentHost
}

// Usage is one agent step's LLM use, for the monitor, the budget and the audit log.
type Usage struct {
	Agent        string `json:"-"`
	Model        string `json:"model"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	Content      string `json:"content,omitempty"` // start of the reply, for the audit log
	HasToolCalls bool   `json:"has_tool_calls"`
}

// proposal is a yield waiting for the decision loop.
type proposal struct {
	req   domain.YieldRequest
	reply chan decision
}

// decision is the loop's answer, with the approved state of every proposed
// path (file_edit approvals only).
type decision struct {
	resp  domain.YieldResponse
	files map[string]*approvedFile
}

// Approval is the orchestrator's answer to a proposal.
type Approval struct {
	Approved bool
	Feedback string
	files    map[string]*approvedFile
}

// Apply makes the worktree hold what was approved for the proposed paths: the
// bytes and modes the orchestrator derived, each written atomically, and
// approved deletions removed. Tools call it rather than computing content, so
// what they write is by construction what finalize verifies and commits.
func (a Approval) Apply(worktree string) error {
	if !a.Approved {
		return errors.New("not approved")
	}
	paths := make([]string, 0, len(a.files))
	for p := range a.files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if _, err := cleanApprovedPath(worktree, p); err != nil { // a symlink may have appeared since
			return err
		}
		full, f := filepath.Join(worktree, filepath.FromSlash(p)), a.files[p]
		if f.deleted {
			if err := os.Remove(full); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			continue
		}
		if err := writeAtomic(full, f.content, f.mode); err != nil {
			return fmt.Errorf("write %s: %w", p, err)
		}
	}
	return nil
}

func writeAtomic(full string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(full), ".staircase-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // gone after a successful rename
	_, err = tmp.Write(content)
	if err == nil {
		err = tmp.Chmod(mode)
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), full)
}

// Propose submits a yield and blocks until the orchestrator decides. When the
// run ends first, the answer is a rejection.
func (e *AgentEnv) Propose(ctx context.Context, req domain.YieldRequest) Approval {
	req.Type = "yield_request"
	reject := func(why string) Approval { return Approval{Feedback: why} }
	if req.ActionType == domain.ActionShellExec && !e.AllowShell {
		e.host.audit("shell_exec_rejected", req)
		return reject("shell_exec disabled - restart with --allow-shell-exec to enable")
	}
	if !req.ReviewAfter { // the decision loop records it once it knows the changes
		e.host.audit("yield_request", req)
	}
	p := proposal{req: req, reply: make(chan decision, 1)}
	select {
	case e.proposals <- p:
	case <-ctx.Done():
		return reject("the run ended before this proposal was decided")
	}
	select {
	case d := <-p.reply:
		return Approval{Approved: d.resp.Approved, Feedback: d.resp.Feedback, files: d.files}
	case <-ctx.Done():
		return reject("the run ended before this proposal was decided")
	}
}

// ProposeEdit proposes one file change (see the Marker constants) for agent.
func (e *AgentEnv) ProposeEdit(ctx context.Context, agent, reasoning string, edit domain.ProposedEdit) Approval {
	return e.propose(ctx, agent, reasoning, domain.ActionFileEdit, edit)
}

// ProposeWorktreeChanges asks for a decision on every change in the
// worktree that was not approved, such as files a command wrote. Approved,
// they are kept; rejected, the worktree is put back to its approved state.
// With no such change there is nothing to decide.
func (e *AgentEnv) ProposeWorktreeChanges(ctx context.Context, agent, reasoning string) Approval {
	return e.Propose(ctx, domain.YieldRequest{AgentName: agent, ActionType: domain.ActionFileEdit,
		ReasoningTrace: reasoning, ReviewAfter: true, ConfidenceScore: 0.9})
}

// ShellRan records that an approved command ran, and whether in the
// sandbox, then asks for a decision on the files it changed (see
// ProposeWorktreeChanges).
func (e *AgentEnv) ShellRan(ctx context.Context, agent, command string, sandboxed bool) Approval {
	e.host.audit("shell_ran", map[string]any{"agent": agent, "command": command, "sandboxed": sandboxed})
	return e.Propose(ctx, domain.YieldRequest{AgentName: agent, ActionType: domain.ActionFileEdit,
		ReasoningTrace: "files changed by the approved command: " + command, ReviewAfter: true, Sandboxed: sandboxed, ConfidenceScore: 0.9})
}

// ProposeShell proposes running command in dir, relative to the worktree.
func (e *AgentEnv) ProposeShell(ctx context.Context, agent, reasoning, dir, command string) Approval {
	return e.propose(ctx, agent, reasoning, domain.ActionShellExec, domain.ProposedEdit{File: dir, SearchBlock: MarkerShell, ReplaceBlock: command})
}

func (e *AgentEnv) propose(ctx context.Context, agent, reasoning, action string, edit domain.ProposedEdit) Approval {
	return e.Propose(ctx, domain.YieldRequest{AgentName: agent, ActionType: action,
		ProposedEdits: []domain.ProposedEdit{edit}, ReasoningTrace: reasoning, ConfidenceScore: 0.9})
}

// Refusal is what the agent is told when a proposal was not approved.
func (a Approval) Refusal() string {
	if a.Feedback == "" {
		return "rejected: no feedback"
	}
	return "rejected: " + a.Feedback
}

// Emit reports one step's usage. It waits for the monitor, so the budget
// counts every step, and gives up only when the run ends.
func (e *AgentEnv) Emit(ctx context.Context, u Usage) {
	e.host.audit("state_emit", map[string]any{"type": "state_emit", "active_agent": u.Agent, "state": u})
	select {
	case e.usage <- u:
	case <-ctx.Done():
	}
}

// Secret returns a project secret for the agent, such as an LLM provider key.
func (e *AgentEnv) Secret(name string) (string, error) { return e.host.secret(name) }

// maxAuditPayload caps one audit entry, as the IPC server did (64 KiB).
const maxAuditPayload = 64 << 10

// agentHost holds what the orchestrator tracks for an in-process agent.
type agentHost struct {
	store     *persistence.Store
	runID     int64
	projectID int64
	aesKey    []byte

	debug io.Writer // --debug: every audited message, scrubbed, one per line

	mu        sync.Mutex
	delivered []string // secret values (and JSON-escaped forms) handed to the agent
}

// audit appends v to the run's chain with every delivered secret scrubbed.
func (h *agentHost) audit(event string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		obs.Log.Warn("audit marshal", "event", event, "err", err)
		return
	}
	payload := string(crypto.ScrubBytes(b, h.deliveredSecrets()))
	if len(payload) > maxAuditPayload { // a cut payload would be neither JSON nor, perhaps, UTF-8: say what was dropped instead
		sum := sha256.Sum256([]byte(payload))
		payload = fmt.Sprintf(`{"truncated":true,"bytes":%d,"sha256":%q}`, len(payload), hex.EncodeToString(sum[:]))
	}
	if h.debug != nil {
		_, _ = fmt.Fprintf(h.debug, "%s %s %s\n", time.Now().UTC().Format(time.RFC3339Nano), event, payload)
	}
	if _, err := h.store.AppendEventLogChained(h.runID, event, payload, ""); err != nil {
		obs.Log.Warn("append audit event", "event", event, "err", err)
		return
	}
	obs.AuditChainLength.Inc()
}

func (h *agentHost) deliveredSecrets() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.delivered...)
}

// secret delivers a secret of the run's own project. Reserved keys (such as
// the webhook HMAC secret) are the orchestrator's and never the agent's.
func (h *agentHost) secret(name string) (string, error) {
	outcome := "error"
	defer func() {
		_ = h.store.LogSecretAccess(&h.runID, name, outcome) // CHECK 4.4.1
		obs.SecretAccessTotal.WithLabelValues(outcome).Inc()
	}()
	if name == "" {
		return "", errors.New("secret name required")
	}
	if strings.HasPrefix(name, "__") {
		return "", fmt.Errorf("secret %q is reserved and not available to agents", name)
	}
	sec, err := h.store.GetSecret(name, &h.projectID)
	if err != nil {
		return "", fmt.Errorf("secret %q: %w", name, err)
	}
	if sec == nil {
		outcome = "not_found"
		return "", fmt.Errorf("secret %q not found - store it with 'staircase secret set %s'", name, name)
	}
	plain, err := crypto.Decrypt(h.aesKey, sec.EncryptedValue)
	if err != nil {
		return "", fmt.Errorf("secret %q: decrypt: %w", name, err)
	}
	outcome = "success"
	h.mu.Lock()
	h.delivered = append(h.delivered, plain)
	if b, err := json.Marshal(plain); err == nil && string(b[1:len(b)-1]) != plain {
		h.delivered = append(h.delivered, string(b[1:len(b)-1])) // its JSON-escaped form
	}
	h.mu.Unlock()
	return plain, nil
}
