package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/tui"
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
	if st.Votes > 0 {
		line := fmt.Sprintf("reviewers  %d vote(s) of the reviewer models on %d proposal(s)", st.Votes, st.VotedProposals)
		if st.UnsettledProposals > 0 {
			line += fmt.Sprintf("; the models did not settle %d (they disagreed, or one gave no verdict): a person decided", st.UnsettledProposals)
		}
		out = append(out, line)
		out = append(out, panelLines(st.Panel)...)
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
//
// A run whose status cannot be read (its chain does not verify, say) is listed as unreadable, never left out: it may be one of them.
func alsoAway(store *persistence.Store, runner *orchestrator.Runner) []string {
	var out []string
	for _, status := range runStatuses {
		if status == persistence.RunStatusAwaitingReview { // the inbox lists those
			continue
		}
		runs, err := store.ListRunsByStatus(status)
		if err != nil {
			continue
		}
		for _, r := range runs {
			st, err := runner.StatusOf(r.ID)
			if err != nil {
				out = append(out, unreadable(r.ID, err))
				continue
			}
			if !st.Away || (st.Outcome != history.OutcomeRunning && st.Outcome != history.OutcomeBlocked) {
				continue
			}
			out = append(out, fmt.Sprintf("run #%d   %s: %s", r.ID, st.Outcome, st.Why))
		}
	}
	return out
}

// runStatuses are the statuses a run is listed under, in the order the tables show them.
var runStatuses = []string{persistence.RunStatusRunning, persistence.RunStatusAwaitingReview, persistence.RunStatusSuccess, persistence.RunStatusFailed, persistence.RunStatusKilled}

// unreadable is the line for a run whose status could not be read.
func unreadable(runID int64, err error) string {
	return tui.Safe(fmt.Sprintf("could not read run #%d: %v", runID, err))
}

// outcomeOf is a run's outcome for a table: what its history says, or that it cannot be read.
func outcomeOf(runner *orchestrator.Runner, runID int64) string {
	st, err := runner.StatusOf(runID)
	if err != nil {
		return "unreadable history"
	}
	return string(st.Outcome)
}

// maxPanelLines is how many proposals the status lists with the models' votes; inspect log shows every entry.
const maxPanelLines = 12

// panelLines is each proposal the reviewer models voted on, with every model's verdict and reason, newest decisions last. The reasons
// are the models' own words, shown with their control characters made visible and cut short.
func panelLines(panel []history.Panelled) []string {
	var out []string
	for i, p := range panel {
		if i == maxPanelLines {
			out = append(out, fmt.Sprintf("           ... and %d more proposal(s) with votes: staircase inspect log shows each", len(panel)-i))
			break
		}
		var votes []string
		for _, v := range p.Votes {
			reason := strings.Join(strings.Fields(v.Reason), " ")
			if r := []rune(reason); len(r) > 80 {
				reason = string(r[:80]) + "..."
			}
			votes = append(votes, tui.Safe(fmt.Sprintf("%s %s (%s)", v.Model, v.Verdict, reason)))
		}
		verdict := "rejected"
		if p.Approved {
			verdict = "approved"
		}
		out = append(out, fmt.Sprintf("           proposal %d%s: %s -> %s by %s", p.Seq, pathsOf(p.Paths), strings.Join(votes, "; "), verdict, tui.Safe(p.DecidedBy)))
	}
	return out
}

func pathsOf(paths []string) string {
	switch len(paths) {
	case 0:
		return ""
	case 1:
		return " (" + tui.Safe(paths[0]) + ")"
	}
	return fmt.Sprintf(" (%s and %d more)", tui.Safe(paths[0]), len(paths)-1)
}
