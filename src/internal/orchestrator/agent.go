package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/sandbox"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/diag"
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
// AgentSession is a vendor session an agent program can continue: its id, and whether this run asks the agent to resume it
// (a continued run whose agent can carry its own conversation) or to start it under that id.
type AgentSession struct {
	ID     string
	Resume bool
}

// SessionResumer is implemented by agents that can resume their own program's session, so a continued run need not start the
// agent's conversation again. An agent that does not implement it, or answers false, is continued with a fresh session grounded
// by the audit chain, and only when a person asked for that.
type SessionResumer interface{ NativeResume() bool }

type AgentEnv struct {
	// Session is set when a continued run resumes the agent's own session (see AgentSession); nil otherwise.
	Session *AgentSession
	// Continuation is what a fresh agent session is told when the run is a continuation of an interrupted one: the
	// task as it stood, built by Go from the audit chain ("" for a first segment). Add it to the agent's first message.
	Continuation string
	// Brief is the pinned Away brief when the run is away (ADR 0008): how to work when nobody answers. Put it before the task.
	Brief string

	Worktree   string // the run's git worktree: the agent's project root
	AllowShell bool   // shell_exec may be proposed (--allow-shell-exec)
	Sandbox    string // approved commands: "auto", "required" or "off" (--sandbox)
	// Unreadable are paths or patterns, from the worktree's root, that the project keeps out of the agent's reach (Terms.Unreadable).
	Unreadable []string
	reach      reach
	// Instructions are the project's, told to the agent before its task (Terms.Instructions).
	Instructions string
	// Environment is the run's declared environment, shown read-only to the checks run when the agent says it is done.
	Environment  []sandbox.Overlay
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
	entry string // the chain entry of its request, which its decision names
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

// RecordSession puts the agent's vendor session on the audit chain (`agent_session`): its id, and whether it was started or
// resumed in this segment. A later continuation resumes the session only if the chain names it. An agent calls this as soon as
// it knows the id.
func (e *AgentEnv) RecordSession(agent, id, mode string) {
	e.host.audit(history.AgentSession{Agent: agent, ID: id, Mode: mode})
}

// Propose submits a yield and blocks until the orchestrator decides. When the
// run ends first, the answer is a rejection.
func (e *AgentEnv) Propose(ctx context.Context, req domain.YieldRequest) Approval {
	req.Type = "yield_request"
	reject := func(why string) Approval { return Approval{Feedback: why} }
	if req.ActionType == domain.ActionShellExec && !e.AllowShell {
		e.host.audit(history.ShellExecRejected{YieldRequest: req})
		return reject("shell_exec disabled - restart with --allow-shell-exec to enable")
	}
	p := proposal{req: req, reply: make(chan decision, 1)}
	if !req.ReviewAfter { // the decision loop records it once it knows the changes
		var err error
		if p.entry, err = e.host.auditRequest(req); err != nil {
			return reject("the orchestrator could not record this request, so it was not decided: " + err.Error())
		}
	}
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
	e.host.audit(history.ShellRan{Agent: agent, Command: command, Sandboxed: sandboxed})
	return e.Propose(ctx, domain.YieldRequest{AgentName: agent, ActionType: domain.ActionFileEdit,
		ReasoningTrace: "files changed by the approved command: " + command, ReviewAfter: true, Sandboxed: sandboxed, ConfidenceScore: 0.9})
}

// ProposeShell proposes running command in dir, relative to the worktree.
func (e *AgentEnv) ProposeShell(ctx context.Context, agent, reasoning, dir, command string) Approval {
	return e.propose(ctx, agent, reasoning, domain.ActionShellExec, domain.ProposedEdit{File: dir, SearchBlock: MarkerShell, ReplaceBlock: command})
}

// ProposeToolCall asks for a call of an MCP server's tool, with its exact input (JSON): decided like a command (ADR 0008).
func (e *AgentEnv) ProposeToolCall(ctx context.Context, agent, reasoning, tool, input string) Approval {
	return e.propose(ctx, agent, reasoning, domain.ActionToolCall, domain.ProposedEdit{File: tool, SearchBlock: MarkerToolCall, ReplaceBlock: input})
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
	step := history.StateEmit{ActiveAgent: u.Agent, Type: "state_emit"}
	step.State.Model, step.State.In, step.State.Out, step.State.Content, step.State.HasToolCalls = u.Model, u.InputTokens, u.OutputTokens, u.Content, u.HasToolCalls
	e.host.audit(step)
	select {
	case e.usage <- u:
	case <-ctx.Done():
	}
}

// Secret returns a project secret for the agent, such as an LLM provider key.
func (e *AgentEnv) Secret(name string) (string, error) { return e.host.secret(name) }

// Deliver declares values the harness hands the agent from outside the secret store (an environment variable of the person's,
// --pass-env), so that no log and no record of the run carries them.
func (e *AgentEnv) Deliver(values ...string) { e.host.deliver(values...) }

// maxAuditPayload caps one audit entry, as the IPC server did (64 KiB).
const maxAuditPayload = 64 << 10

// agentHost holds what the orchestrator tracks for an in-process agent.
type agentHost struct {
	store     *persistence.Store
	runID     int64
	projectID int64
	aesKey    []byte

	debug io.Writer // --debug: every audited message, scrubbed, one per line
	w     *history.Writer

	// storageFailed is set when a record of the run could not be written to the chain; the launch's class says so.
	storageFailed atomic.Bool

	mu        sync.Mutex
	delivered []string // secret values (and JSON-escaped forms) handed to the agent
}

// newAgentHost is the host of one run's in-process agent. Its writer is the chain's, set for what an agent supplies (ADR 0014): the
// values delivered to the agent are scrubbed from every entry, an entry over 64 KiB is written as a note of what it left out, and a
// failure to write marks the run's launch as a storage failure.
func newAgentHost(store *persistence.Store, runID, projectID int64, aesKey []byte, debug io.Writer) *agentHost {
	h := &agentHost{store: store, runID: runID, projectID: projectID, aesKey: aesKey, debug: debug}
	h.w = history.NewWriter(store, runID, history.Settings{
		Plain: true, Cap: maxAuditPayload, Debug: debug,
		Scrub:  func(b []byte) []byte { return crypto.ScrubBytes(b, h.deliveredSecrets()) },
		OnFail: func(string, error) { h.storageFailed.Store(true) },
	})
	return h
}

// audit appends e to the run's chain with every delivered secret scrubbed. A failure is logged: for events that are only a record
// (usage, a command that ran) the run goes on. A request that a decision will follow uses auditRequest, which reports it.
func (h *agentHost) audit(e history.Event) { _ = h.w.Append(e) }

// auditRequest records a proposal's request before anything is decided about it. Recovery reads the decision
// together with its request, so when the request cannot be recorded the proposal must not be decided at
// all: the caller refuses it.
func (h *agentHost) auditRequest(req domain.YieldRequest) (entry string, err error) {
	return h.w.AppendHash(history.Request{YieldRequest: req})
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
	h.deliver(plain)
	return plain, nil
}

// deliver records values handed to the agent, and their JSON-escaped forms, for everything the run writes to scrub. Every log of
// the process scrubs them too (diag.Deliver): a hosted session's log is not the run's.
func (h *agentHost) deliver(values ...string) {
	var all []string
	for _, v := range values {
		if v == "" {
			continue
		}
		all = append(all, v)
		if b, err := json.Marshal(v); err == nil && string(b[1:len(b)-1]) != v {
			all = append(all, string(b[1:len(b)-1])) // its JSON-escaped form
		}
	}
	h.mu.Lock()
	h.delivered = append(h.delivered, all...)
	h.mu.Unlock()
	diag.Deliver(all...)
}
