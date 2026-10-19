package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/persistence"
)

// statusLines are a run's morning status (V11-C01 scope 6): its outcome and why, its attempts, and the time, tokens and
// estimated cost it used against what is left.
func statusLines(st orchestrator.Status) []string {
	out := []string{fmt.Sprintf("outcome    %s: %s", st.Outcome, st.Why)}
	attempts := fmt.Sprintf("attempts   %d (times the agent was launched: the run, then each continuation)", st.Attempts)
	if st.Parked > 0 {
		attempts += fmt.Sprintf("; %d proposal(s) parked for you", st.Parked)
	}
	if st.LostLaunches > 0 {
		attempts += fmt.Sprintf("; %d launch(es) ended without an established end", st.LostLaunches)
	}
	out = append(out, attempts)
	if len(st.Failures) > 0 {
		var each []string
		for _, l := range st.Failures {
			each = append(each, fmt.Sprintf("attempt %d: %s", l.Attempt, history.ClassPhrase(l.Class)))
		}
		out = append(out, "launches   "+strings.Join(each, "; ")+" (about the agent, not its checks or any acceptance)")
	}
	elapsed := st.Elapsed.Round(time.Second)
	switch {
	case st.TimeUnknown != "":
		out = append(out, fmt.Sprintf("time       %s used; no time limit could be read: %s", elapsed, st.TimeUnknown))
	case st.TimeLimit > 0:
		rest := "nothing left"
		if d := st.TimeLimit - elapsed; d > 0 {
			rest = d.String() + " left"
		}
		out = append(out, fmt.Sprintf("time       %s of %s, %s", elapsed, st.TimeLimit, rest))
	default:
		out = append(out, fmt.Sprintf("time       %s used (no time limit)", elapsed))
	}
	out = append(out, fmt.Sprintf("tokens     %d in, %d out, as the agents reported them (no token limit)", st.InputTokens, st.OutputTokens))
	if st.BudgetCap > 0 {
		rest := "nothing left"
		if c := st.BudgetCap - st.CostUSD; c > 0 {
			rest = fmt.Sprintf("about $%.2f left", c)
		}
		out = append(out, fmt.Sprintf("cost       about $%.2f of $%.2f (the project's cap per run), %s, estimated from list prices", st.CostUSD, st.BudgetCap, rest))
	} else {
		out = append(out, fmt.Sprintf("cost       about $%.2f, estimated from list prices (no budget cap)", st.CostUSD))
	}
	return out
}

// printStatus prints a run's morning status, indented under its heading.
func printStatus(st orchestrator.Status) {
	away := ""
	if st.Away {
		away = " (away)"
	}
	fmt.Printf("Run #%d%s\n", st.RunID, away)
	for _, l := range statusLines(st) {
		fmt.Printf("   %s\n", l)
	}
}

// alsoAway are the away runs, besides those awaiting review, that are still running or wait for their owner (blocked or partial):
// one line each, for the inbox.
func alsoAway(store *persistence.Store, runner *orchestrator.Runner) []string {
	var out []string
	for _, status := range []string{persistence.RunStatusRunning, persistence.RunStatusKilled, persistence.RunStatusSuccess, persistence.RunStatusFailed} {
		runs, err := store.ListRunsByStatus(status)
		if err != nil {
			continue
		}
		for _, r := range runs {
			st, err := runner.StatusOf(r.ID)
			if err != nil || !st.Away || (st.Outcome != history.OutcomeRunning && st.Outcome != history.OutcomeBlocked) {
				continue
			}
			out = append(out, fmt.Sprintf("run #%d   %s: %s", r.ID, st.Outcome, st.Why))
		}
	}
	return out
}

// outcomeOf is a run's outcome for a table: what its history says, or that it cannot be read.
func outcomeOf(runner *orchestrator.Runner, runID int64) string {
	st, err := runner.StatusOf(runID)
	if err != nil {
		return "unreadable history"
	}
	return string(st.Outcome)
}
