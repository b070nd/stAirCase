package monitor

import (
	"sync"
	"time"
)

// AgentStats holds accumulated telemetry for one agent.
type AgentStats struct {
	Name         string
	Model        string
	Steps        int
	InputTokens  int
	OutputTokens int
	Active       bool
	FirstSeen    time.Time
	LastActive   time.Time
}

// CostUSD returns the estimated USD cost for this agent's token usage.
func (a AgentStats) CostUSD() float64 {
	return EstimateCost(a.Model, a.InputTokens, a.OutputTokens)
}

// Tracker accumulates per-agent telemetry emitted via IPC state_emit.
type Tracker struct {
	mu      sync.Mutex
	agents  map[string]*AgentStats
	order   []string // insertion order for stable display
	started time.Time

	RunID   int64
	CaseID  int64
	Project string
	Branch  string
}

// NewTracker creates a Tracker for a run.
func NewTracker(runID, caseID int64, project, branch string) *Tracker {
	return &Tracker{
		agents:  make(map[string]*AgentStats),
		started: time.Now(),
		RunID:   runID,
		CaseID:  caseID,
		Project: project,
		Branch:  branch,
	}
}

// Record applies a state_emit event to the tracker.
func (t *Tracker) Record(agentName, model string, inputTokens, outputTokens int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	a, ok := t.agents[agentName]
	if !ok {
		a = &AgentStats{Name: agentName, Model: model, FirstSeen: time.Now()}
		t.agents[agentName] = a
		t.order = append(t.order, agentName)
	}
	if model != "" {
		a.Model = model
	}
	a.Steps++
	a.InputTokens += inputTokens
	a.OutputTokens += outputTokens
	a.LastActive = time.Now()

	for _, s := range t.agents {
		s.Active = s.Name == agentName
	}
}

// Snapshot returns a read-only copy of current per-agent stats and elapsed time.
func (t *Tracker) Snapshot() ([]AgentStats, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()

	out := make([]AgentStats, 0, len(t.order))
	for _, name := range t.order {
		out = append(out, *t.agents[name])
	}
	return out, time.Since(t.started)
}

// Totals returns summed token/cost/step counts across all agents.
func (t *Tracker) Totals() AgentStats {
	t.mu.Lock()
	defer t.mu.Unlock()

	var total AgentStats
	for _, a := range t.agents {
		total.Steps += a.Steps
		total.InputTokens += a.InputTokens
		total.OutputTokens += a.OutputTokens
	}
	return total
}

// Elapsed returns time since the tracker was created.
func (t *Tracker) Elapsed() time.Duration {
	return time.Since(t.started)
}
