package orchestrator

import (
	"fmt"
	"os"
	"time"

	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/monitor"
	"github.com/b070nd/stAirCase/src/internal/plan"
)

// Status is a run's morning status (V11-C01 scope 6): its outcome and why, how many times its agent was started, what it parked,
// and what it used of its bounds. Everything but the bounds is read from the verified history; the time limit is the saved
// policy's and plan's, each checked against the digest the chain names; the budget cap is the project's as it is set now.
type Status struct {
	RunID   int64
	Away    bool
	Outcome history.Outcome
	Why     string
	Commit  string // the delivered commit, when there is one
	// Attempts is how many times the run's agent was started: its segments, the first run and each continuation.
	Attempts int
	Parked   int // proposals parked for the owner and not adopted

	Elapsed   time.Duration // the active time its segments used
	TimeLimit time.Duration // 0: none, or none that can be read (TimeUnknown)
	// TimeUnknown says why the time limit could not be read; "" when it was.
	TimeUnknown string

	InputTokens, OutputTokens int     // as the agents reported them
	CostUSD                   float64 // estimated from the reported tokens and the models' list prices
	BudgetCap                 float64 // the project's cap in USD, per run; 0: none
}

// StatusOf is run runID's morning status. It reads and changes nothing.
func (r *Runner) StatusOf(runID int64) (Status, error) {
	st := Status{RunID: runID}
	run, err := r.store.GetRun(runID)
	if err != nil || run == nil {
		return st, fmt.Errorf("run #%d not found", runID)
	}
	h, err := history.Read(r.store, runID)
	if err != nil {
		return st, err
	}
	st.Away = h.Bound.AwayBrief != nil
	st.Outcome, st.Why = h.Outcome()
	if st.Outcome == history.OutcomeDelivered || st.Outcome == history.OutcomeDeliveredIncomplete {
		st.Commit = run.GitCommitHash
		if st.Commit == "" && len(h.Delivered) > 0 {
			st.Commit = h.Delivered[len(h.Delivered)-1].Commit
		}
	}
	st.Attempts, st.Parked, st.Elapsed = h.Segment, len(h.Parked()), h.Elapsed
	for _, u := range h.Usage {
		st.InputTokens += u.State.In
		st.OutputTokens += u.State.Out
		st.CostUSD += monitor.EstimateCost(u.State.Model, u.State.In, u.State.Out)
	}
	st.TimeLimit, st.TimeUnknown = r.timeLimit(runID, h)
	if cs, err := r.store.GetCase(run.CaseID); err == nil && cs != nil {
		_, st.BudgetCap, _ = r.store.GetProjectConfig(cs.ProjectID)
	}
	return st, nil
}

// timeLimit is the run's time limit, as its saved policy and plan set it (the stricter of the two), or why it cannot be read.
func (r *Runner) timeLimit(runID int64, h history.History) (time.Duration, string) {
	snap, err := r.savedPolicy(runID, h)
	if err != nil {
		return 0, err.Error()
	}
	limits := snap.Engine.Limits.Limits
	if h.Bound.PlanDigest != "" {
		raw, err := os.ReadFile(savedPlanPath(r.wsDir, runID))
		if err != nil {
			return 0, fmt.Sprintf("run #%d saved no plan when it started", runID)
		}
		p, err := plan.Parse(raw, h.Bound.PlanDigest)
		if err != nil {
			return 0, fmt.Sprintf("the plan saved for run #%d is not the one it ran under: %v", runID, err)
		}
		limits = limits.Tighter(p.Limits)
	}
	return time.Duration(limits.MaxRunSecs) * time.Second, ""
}
