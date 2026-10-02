package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/b070nd/stAirCase/src/internal/domain"
)

// RunYieldTUI blocks until the operator approves or rejects the proposed edits,
// or ctx ends (the run was cancelled or ran out of time), which rejects them. It
// renders the yield request in a full-screen Bubbletea TUI with glamour
// Markdown rendering for the reasoning trace. Ctrl-C rejects the change and
// interrupts the process, as it does outside the dialog.
func RunYieldTUI(ctx context.Context, req domain.YieldRequest) domain.YieldResponse {
	return runYield(ctx, req, tea.WithAltScreen())
}

// interrupt stops the run the way an operator's Ctrl-C does outside the
// dialog (replaced in tests).
var interrupt = raiseInterrupt

func runYield(ctx context.Context, req domain.YieldRequest, opts ...tea.ProgramOption) domain.YieldResponse {
	m := newYieldModel(req)
	p := tea.NewProgram(m, append(opts, tea.WithContext(ctx))...)
	result, err := p.Run()
	switch {
	case ctx.Err() != nil:
		return domain.Decide(false, "the run ended while this waited for a decision")
	case err != nil:
		// Fallback to rejection on TUI failure.
		return domain.Decide(false, "TUI error: "+err.Error())
	}
	final := result.(yieldModel)
	if final.interrupted {
		interrupt()
	}
	return final.resp
}

// ─── Model ────────────────────────────────────────────────────────────────────

type yieldState int

const (
	stateReviewing yieldState = iota
	stateFeedback
	stateDone
)

type yieldModel struct {
	req      domain.YieldRequest
	state    yieldState
	feedback strings.Builder
	resp     domain.YieldResponse
	width    int
	height   int
	rendered string // glamour-rendered reasoning trace

	interrupted bool // the operator pressed Ctrl-C
}

func newYieldModel(req domain.YieldRequest) yieldModel {
	rendered, _ := glamour.Render(req.ReasoningTrace, "dark")
	return yieldModel{req: req, rendered: rendered, width: 120, height: 40}
}

// ─── Styles ───────────────────────────────────────────────────────────────────

var (
	styleHeader  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	styleLabel   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	styleCode    = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("235")).Padding(0, 1)
	styleMeta    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	styleApprove = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("46"))
	styleReject  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196"))
	stylePrompt  = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	styleBorder  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("238")).Padding(0, 1)
)

// ─── tea.Model interface ──────────────────────────────────────────────────────

func (m yieldModel) Init() tea.Cmd { return nil }

func (m yieldModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		rendered, _ := glamour.Render(m.req.ReasoningTrace, "dark")
		m.rendered = rendered

	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC { // raw mode: a key, not a signal; stop the run
			m.resp = domain.Decide(false, "the operator interrupted the run")
			m.interrupted, m.state = true, stateDone
			return m, tea.Quit
		}
		switch m.state {
		case stateReviewing:
			switch msg.String() {
			case "y", "Y":
				m.resp = domain.Decide(true, "")
				m.state = stateDone
				return m, tea.Quit
			case "n", "N", "q", "esc":
				m.state = stateFeedback
			}

		case stateFeedback:
			switch msg.Type {
			case tea.KeyEnter:
				m.resp = domain.Decide(false, m.feedback.String())
				m.state = stateDone
				return m, tea.Quit
			case tea.KeyBackspace:
				if r := []rune(m.feedback.String()); len(r) > 0 { // by characters, not bytes
					m.feedback.Reset()
					m.feedback.WriteString(string(r[:len(r)-1]))
				}
			case tea.KeyRunes:
				m.feedback.WriteString(string(msg.Runes))
			case tea.KeyEsc:
				m.state = stateReviewing
			}
		}
	}
	return m, nil
}

func (m yieldModel) View() string {
	var sb strings.Builder

	sb.WriteString(styleHeader.Render("⚠  HITL Yield Request"))
	sb.WriteString("\n\n")
	if m.req.Guard != "" {
		sb.WriteString(styleReject.Render("CHECK: " + m.req.Guard))
		sb.WriteString("\n")
	}
	if m.req.Drift != "" {
		sb.WriteString(styleReject.Render("DRIFT: " + m.req.Drift))
		sb.WriteString("\n\n")
	}

	sb.WriteString(styleLabel.Render("Agent:  "))
	sb.WriteString(m.req.AgentName)
	sb.WriteString("   ")
	sb.WriteString(styleLabel.Render("Action: "))
	sb.WriteString(m.req.ActionType)
	if m.req.ConfidenceScore > 0 {
		sb.WriteString(styleMeta.Render(fmt.Sprintf("   Confidence: %.0f%%", m.req.ConfidenceScore*100)))
	}
	if m.req.BatchID != "" {
		sb.WriteString(styleMeta.Render(fmt.Sprintf("   Batch: %s", m.req.BatchID)))
	}
	sb.WriteString("\n\n")

	sb.WriteString(styleLabel.Render("Reasoning"))
	sb.WriteString("\n")
	sb.WriteString(m.rendered)

	for i, edit := range m.req.ProposedEdits {
		sb.WriteString(styleLabel.Render(fmt.Sprintf("── Edit %d: %s ──", i+1, edit.File)))
		sb.WriteString("\n")
		if edit.BinarySHA256 != "" { // not text: shown by what it is, never as garbage
			sb.WriteString(styleCode.Render("BINARY FILE, whole new content") + "\n")
			sb.WriteString(styleBorder.Render(fmt.Sprintf("%d bytes, sha256 %s", edit.BinaryBytes, edit.BinarySHA256)))
			sb.WriteString("\n\n")
			continue
		}
		sb.WriteString(styleCode.Render("SEARCH  ") + "\n")
		sb.WriteString(styleBorder.Render(edit.SearchBlock))
		sb.WriteString("\n")
		sb.WriteString(styleCode.Render("REPLACE ") + "\n")
		sb.WriteString(styleBorder.Render(edit.ReplaceBlock))
		sb.WriteString("\n\n")
	}

	switch m.state {
	case stateReviewing:
		sb.WriteString(styleApprove.Render("[y] Approve") + "  " + styleReject.Render("[n] Reject"))
	case stateFeedback:
		sb.WriteString(styleReject.Render("Rejected.") + " Feedback (Enter to confirm, Esc to go back):\n")
		sb.WriteString(stylePrompt.Render("> " + m.feedback.String() + "█"))
	}

	return sb.String()
}
