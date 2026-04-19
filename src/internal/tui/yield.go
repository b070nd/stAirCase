package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/b070nd/staircase-core/src/internal/ipc"
)

// RunYieldTUI blocks until the operator approves or rejects the proposed edits.
// It renders the yield request in a full-screen Bubbletea TUI with glamour
// Markdown rendering for the reasoning trace.
func RunYieldTUI(req ipc.IpcYieldRequest) ipc.IpcYieldResponse {
	m := newYieldModel(req)
	p := tea.NewProgram(m, tea.WithAltScreen())
	result, err := p.Run()
	if err != nil {
		// Fallback to rejection on TUI failure.
		return ipc.IpcYieldResponse{Type: "yield_response", Approved: false, Feedback: "TUI error: " + err.Error()}
	}
	return result.(yieldModel).resp
}

// ─── Model ────────────────────────────────────────────────────────────────────

type yieldState int

const (
	stateReviewing yieldState = iota
	stateFeedback
	stateDone
)

type yieldModel struct {
	req      ipc.IpcYieldRequest
	state    yieldState
	feedback strings.Builder
	resp     ipc.IpcYieldResponse
	width    int
	height   int
	rendered string // glamour-rendered reasoning trace
}

func newYieldModel(req ipc.IpcYieldRequest) yieldModel {
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
		switch m.state {
		case stateReviewing:
			switch msg.String() {
			case "y", "Y":
				m.resp = ipc.IpcYieldResponse{Type: "yield_response", Approved: true}
				m.state = stateDone
				return m, tea.Quit
			case "n", "N", "q", "esc":
				m.state = stateFeedback
			}

		case stateFeedback:
			switch msg.Type {
			case tea.KeyEnter:
				m.resp = ipc.IpcYieldResponse{
					Type:     "yield_response",
					Approved: false,
					Feedback: m.feedback.String(),
				}
				m.state = stateDone
				return m, tea.Quit
			case tea.KeyBackspace:
				s := m.feedback.String()
				if len(s) > 0 {
					m.feedback.Reset()
					m.feedback.WriteString(s[:len(s)-1])
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

	sb.WriteString(styleLabel.Render("Agent:  "))
	sb.WriteString(m.req.AgentName)
	sb.WriteString("   ")
	sb.WriteString(styleLabel.Render("Action: "))
	sb.WriteString(m.req.ActionType)
	if m.req.ConfidenceScore > 0 {
		sb.WriteString(styleMeta.Render(fmt.Sprintf("   Confidence: %.0f%%", m.req.ConfidenceScore*100)))
	}
	sb.WriteString("\n\n")

	sb.WriteString(styleLabel.Render("Reasoning"))
	sb.WriteString("\n")
	sb.WriteString(m.rendered)

	for i, edit := range m.req.ProposedEdits {
		sb.WriteString(styleLabel.Render(fmt.Sprintf("── Edit %d: %s ──", i+1, edit.File)))
		sb.WriteString("\n")
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
