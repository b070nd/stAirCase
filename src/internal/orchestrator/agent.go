package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/b070nd/staircase-core/src/internal/crypto"
	"github.com/b070nd/staircase-core/src/internal/ipc"
	"github.com/b070nd/staircase-core/src/internal/obs"
	"github.com/b070nd/staircase-core/src/internal/persistence"
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
	Worktree   string // the run's git worktree: the agent's project root
	AllowShell bool   // shell_exec may be proposed (--allow-shell-exec)

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
	req   ipc.IpcYieldRequest
	reply chan ipc.IpcYieldResponse
}

// Propose submits a yield and blocks until the orchestrator decides. When the
// run ends first, the answer is a rejection.
func (e *AgentEnv) Propose(ctx context.Context, req ipc.IpcYieldRequest) ipc.IpcYieldResponse {
	req.Type = "yield_request"
	reject := func(why string) ipc.IpcYieldResponse {
		return ipc.IpcYieldResponse{Type: "yield_response", Approved: false, Feedback: why}
	}
	if req.ActionType == "shell_exec" && !e.AllowShell {
		e.host.audit("shell_exec_rejected", req)
		return reject("shell_exec disabled — restart with --allow-shell-exec to enable")
	}
	e.host.audit("yield_request", req)
	p := proposal{req: req, reply: make(chan ipc.IpcYieldResponse, 1)}
	select {
	case e.proposals <- p:
	case <-ctx.Done():
		return reject("the run ended before this proposal was decided")
	}
	select {
	case r := <-p.reply:
		return r
	case <-ctx.Done():
		return reject("the run ended before this proposal was decided")
	}
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
	if len(payload) > maxAuditPayload {
		payload = payload[:maxAuditPayload]
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
		return "", fmt.Errorf("secret %q not found — store it with 'staircase secret set %s'", name, name)
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
