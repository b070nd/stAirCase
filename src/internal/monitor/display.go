package monitor

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// Display renders a live compact monitoring dashboard inline in the terminal.
// It uses ANSI escape codes to redraw in-place. Call Render() on a ticker
// or after each state_emit. Call Pause()/Resume() around the HITL yield TUI.
type Display struct {
	tracker   *Tracker
	mu        sync.Mutex
	paused    bool
	prevLines int
	activity  []string
	budgetCap float64 // 0 = no cap
}

const (
	maxActivityLines = 6
	dashWidth        = 92
)

var (
	dHeader = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	dActive = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("46"))
	dIdle   = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	dMuted  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	dBorder = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	dCost   = lipgloss.NewStyle().Foreground(lipgloss.Color("141"))
	dWarn   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("226"))
	dHITL   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("220"))
)

// NewDisplay creates a Display backed by tracker. budgetCap=0 means no cap.
func NewDisplay(tracker *Tracker, budgetCap float64) *Display {
	return &Display{tracker: tracker, budgetCap: budgetCap}
}

// AddActivity appends a timestamped line to the recent-activity tail.
func (d *Display) AddActivity(msg string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	ts := time.Now().Format("15:04:05")
	d.activity = append(d.activity, ts+"  "+msg)
	if len(d.activity) > maxActivityLines {
		d.activity = d.activity[len(d.activity)-maxActivityLines:]
	}
}

// Pause stops in-place rendering (call before launching the yield TUI).
func (d *Display) Pause() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.paused = true
}

// Resume re-enables rendering (call after the yield TUI exits).
func (d *Display) Resume() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.paused = false
	d.prevLines = 0 // force full redraw at next tick
}

// BudgetExceeded returns true if cost cap is set and total cost has exceeded it.
func (d *Display) BudgetExceeded() bool {
	if d.budgetCap <= 0 {
		return false
	}
	agents, _ := d.tracker.Snapshot()
	var total float64
	for _, a := range agents {
		total += a.CostUSD()
	}
	return total >= d.budgetCap
}

// Render writes (or refreshes) the dashboard to stdout.
func (d *Display) Render() {
	// Snapshot display-local state under lock, then release.
	d.mu.Lock()
	if d.paused {
		d.mu.Unlock()
		return
	}
	prevLines := d.prevLines
	activityCopy := make([]string, len(d.activity))
	copy(activityCopy, d.activity)
	budgetCap := d.budgetCap
	d.mu.Unlock()

	agents, elapsed := d.tracker.Snapshot()

	var sb strings.Builder
	sep := dBorder.Render(strings.Repeat("─", dashWidth))

	// ── Header ────────────────────────────────────────────────────────────────
	headerLine := fmt.Sprintf(
		"stAirCase  Run #%d  │  case=#%d  %s  │  branch: %s  │  %s",
		d.tracker.RunID, d.tracker.CaseID, d.tracker.Project,
		d.tracker.Branch, formatElapsed(elapsed),
	)
	sb.WriteString(dHeader.Render(headerLine))
	sb.WriteString("\n")
	sb.WriteString(sep)
	sb.WriteString("\n")

	// ── Column headings ───────────────────────────────────────────────────────
	sb.WriteString(dMuted.Render(fmt.Sprintf(
		"  %-2s %-16s  %-26s  %6s  %9s  %9s  %10s  %7s",
		"", "AGENT", "MODEL", "STEPS", "IN TOK", "OUT TOK", "COST", "TIME",
	)))
	sb.WriteString("\n")
	sb.WriteString(sep)
	sb.WriteString("\n")

	// ── Per-agent rows ────────────────────────────────────────────────────────
	var totalSteps, totalIn, totalOut int
	var totalCost float64

	for _, a := range agents {
		totalSteps += a.Steps
		totalIn += a.InputTokens
		totalOut += a.OutputTokens
		cost := a.CostUSD()
		totalCost += cost

		dot := dMuted.Render("  ●")
		nameS := dIdle
		if a.Active {
			dot = dActive.Render("  ●")
			nameS = dActive
		}

		agentTime := "—"
		if !a.FirstSeen.IsZero() && a.Steps > 0 {
			agentTime = formatElapsed(a.LastActive.Sub(a.FirstSeen))
		}

		costStr := dMuted.Render("—")
		if cost > 0 {
			costStr = dCost.Render(fmt.Sprintf("$%.4f", cost))
		}

		modelStr := a.Model
		if len(modelStr) > 26 {
			modelStr = modelStr[:23] + "..."
		}

		row := fmt.Sprintf(
			"%s %-16s  %-26s  %6d  %9s  %9s  %10s  %7s",
			dot,
			a.Name,
			modelStr,
			a.Steps,
			formatTokens(a.InputTokens),
			formatTokens(a.OutputTokens),
			costStr,
			agentTime,
		)
		sb.WriteString(nameS.Render(row))
		sb.WriteString("\n")
	}

	// ── Totals row ────────────────────────────────────────────────────────────
	sb.WriteString(sep)
	sb.WriteString("\n")

	totalCostStr := dMuted.Render("—")
	if totalCost > 0 {
		cs := fmt.Sprintf("$%.4f", totalCost)
		if budgetCap > 0 {
			pct := totalCost / budgetCap * 100
			if pct >= 90 {
				cs = dWarn.Render(fmt.Sprintf("$%.4f (%.0f%%)", totalCost, pct))
			} else {
				cs = dCost.Render(fmt.Sprintf("$%.4f / $%.2f", totalCost, budgetCap))
			}
		} else {
			totalCostStr = dCost.Render(cs)
			cs = "" // already set totalCostStr
		}
		if cs != "" {
			totalCostStr = cs
		}
	}

	totalRow := fmt.Sprintf(
		"  %s %-16s  %-26s  %6d  %9s  %9s  %10s  %7s",
		" ",
		"TOTAL",
		"",
		totalSteps,
		formatTokens(totalIn),
		formatTokens(totalOut),
		totalCostStr,
		formatElapsed(elapsed),
	)
	sb.WriteString(dMuted.Render(totalRow))
	sb.WriteString("\n")

	// ── Recent activity ───────────────────────────────────────────────────────
	if len(activityCopy) > 0 {
		sb.WriteString(sep)
		sb.WriteString("\n")
		for _, line := range activityCopy {
			sb.WriteString(dMuted.Render("  " + line))
			sb.WriteString("\n")
		}
	}
	sb.WriteString(sep)
	sb.WriteString("\n")

	out := sb.String()

	// Erase previous render, then print new.
	if prevLines > 0 {
		fmt.Printf("\033[%dA\033[J", prevLines)
	}
	fmt.Print(out)

	newLines := strings.Count(out, "\n")
	d.mu.Lock()
	d.prevLines = newLines
	d.mu.Unlock()
}

// Final prints the dashboard one last time without the erase trick (for run end).
func (d *Display) Final(status string) {
	d.mu.Lock()
	d.paused = false
	d.prevLines = 0
	d.mu.Unlock()
	d.Render()

	statusLine := dActive.Render("✅ " + status)
	if strings.Contains(status, "FAIL") || strings.Contains(status, "KILL") {
		statusLine = dWarn.Render("❌ " + status)
	}
	fmt.Println(statusLine)
}

// ── formatting helpers ────────────────────────────────────────────────────────

func formatElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func formatTokens(n int) string {
	if n == 0 {
		return "—"
	}
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

// ensure dHITL is used (referenced in future HITL integration)
var _ = dHITL
