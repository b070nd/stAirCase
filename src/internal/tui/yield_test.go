package tui_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/b070nd/staircase-core/src/internal/domain"
	tui "github.com/b070nd/staircase-core/src/internal/tui"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

func basicReq() domain.YieldRequest {
	return domain.YieldRequest{
		Type:       "yield_request",
		AgentName:  "planner",
		ActionType: "file_edit",
		ProposedEdits: []domain.ProposedEdit{
			{File: "main.go", SearchBlock: "old", ReplaceBlock: "new"},
		},
		ReasoningTrace:  "## Rationale\nThis edit improves things.",
		ConfidenceScore: 0.9,
	}
}

// ─── View tests ───────────────────────────────────────────────────────────────

func TestView_contains_agent_name(t *testing.T) {
	m := tui.NewYieldModel(basicReq())
	v := m.View()
	assert.Contains(t, v, "planner", "View() should render the agent name")
}

func TestView_contains_action_type(t *testing.T) {
	m := tui.NewYieldModel(basicReq())
	v := m.View()
	assert.Contains(t, v, "file_edit", "View() should render the action type")
}

func TestView_shows_proposed_edit_file(t *testing.T) {
	m := tui.NewYieldModel(basicReq())
	v := m.View()
	assert.Contains(t, v, "main.go", "View() should render the proposed edit filename")
}

func TestView_shows_approve_reject_hint(t *testing.T) {
	m := tui.NewYieldModel(basicReq())
	v := m.View()
	assert.True(t, strings.Contains(v, "[y]") || strings.Contains(v, "Approve"),
		"View() should show approve hint in reviewing state")
	assert.True(t, strings.Contains(v, "[n]") || strings.Contains(v, "Reject"),
		"View() should show reject hint in reviewing state")
}

func TestView_shows_confidence(t *testing.T) {
	m := tui.NewYieldModel(basicReq())
	v := m.View()
	assert.Contains(t, v, "90%", "View() should render confidence score as percentage")
}

func TestView_shows_batch_id_when_set(t *testing.T) {
	req := basicReq()
	req.BatchID = "batch-42"
	m := tui.NewYieldModel(req)
	v := m.View()
	assert.Contains(t, v, "batch-42", "View() should render the BatchID when non-empty")
}

func TestView_no_confidence_when_zero(t *testing.T) {
	req := basicReq()
	req.ConfidenceScore = 0
	m := tui.NewYieldModel(req)
	v := m.View()
	assert.NotContains(t, v, "Confidence", "View() should omit confidence line when score is 0")
}

func TestView_multiple_edits(t *testing.T) {
	req := basicReq()
	req.ProposedEdits = append(req.ProposedEdits, domain.ProposedEdit{
		File:         "util.go",
		SearchBlock:  "foo",
		ReplaceBlock: "bar",
	})
	m := tui.NewYieldModel(req)
	v := m.View()
	assert.Contains(t, v, "main.go")
	assert.Contains(t, v, "util.go")
}

// ─── Update tests — approve ───────────────────────────────────────────────────

func TestUpdate_approve_lowercase_y(t *testing.T) {
	m := tui.NewYieldModel(basicReq())
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	resp := tui.RespOf(result)
	assert.True(t, resp.Approved, "key 'y' should approve the request")
	assert.Equal(t, "yield_response", resp.Type)
}

func TestUpdate_approve_uppercase_Y(t *testing.T) {
	m := tui.NewYieldModel(basicReq())
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}})
	resp := tui.RespOf(result)
	assert.True(t, resp.Approved)
}

// ─── Update tests — reject flow ───────────────────────────────────────────────

func TestUpdate_reject_key_n_enters_feedback_state(t *testing.T) {
	m := tui.NewYieldModel(basicReq())
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	// After 'n' we should be in the feedback state (state == 1), not done yet.
	state := tui.StateOf(result)
	assert.Equal(t, 1, state, "key 'n' should move to the feedback state")
}

func TestUpdate_reject_key_N(t *testing.T) {
	m := tui.NewYieldModel(basicReq())
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	assert.Equal(t, 1, tui.StateOf(result))
}

func TestUpdate_reject_key_q(t *testing.T) {
	m := tui.NewYieldModel(basicReq())
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	assert.Equal(t, 1, tui.StateOf(result))
}

func TestUpdate_reject_esc_enters_feedback_state(t *testing.T) {
	m := tui.NewYieldModel(basicReq())
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.Equal(t, 1, tui.StateOf(result))
}

func TestUpdate_reject_confirm_with_enter(t *testing.T) {
	m := tui.NewYieldModel(basicReq())

	// Enter feedback state.
	intermediate, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	require.Equal(t, 1, tui.StateOf(intermediate), "must be in feedback state first")

	// Type some feedback.
	typed, _ := intermediate.(tui.YieldModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b', 'a', 'd'}})

	// Confirm with Enter.
	final, _ := typed.(tui.YieldModel).Update(tea.KeyMsg{Type: tea.KeyEnter})
	resp := tui.RespOf(final)

	assert.False(t, resp.Approved, "Enter in feedback state should reject")
	assert.Equal(t, "yield_response", resp.Type)
	assert.Equal(t, "bad", resp.Feedback, "Feedback text should be captured")
}

func TestUpdate_feedback_backspace(t *testing.T) {
	m := tui.NewYieldModel(basicReq())

	// Enter feedback state and type "ab".
	intermediate, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	typed, _ := intermediate.(tui.YieldModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a', 'b'}})

	// Backspace should remove last char.
	afterBackspace, _ := typed.(tui.YieldModel).Update(tea.KeyMsg{Type: tea.KeyBackspace})

	// View should show only "a".
	v := afterBackspace.(tui.YieldModel).View()
	assert.Contains(t, v, "> a", "backspace should remove the last typed character")
}

func TestUpdate_feedback_esc_returns_to_reviewing(t *testing.T) {
	m := tui.NewYieldModel(basicReq())

	// Enter feedback state.
	feedback, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	require.Equal(t, 1, tui.StateOf(feedback))

	// Esc should go back to reviewing (state 0).
	back, _ := feedback.(tui.YieldModel).Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.Equal(t, 0, tui.StateOf(back), "Esc in feedback state should return to reviewing")
}

// ─── Update tests — window resize ─────────────────────────────────────────────

func TestUpdate_window_size_msg(t *testing.T) {
	m := tui.NewYieldModel(basicReq())
	result, cmd := m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	assert.Nil(t, cmd)
	// After a resize event the model should still be in reviewing state and View() must not panic.
	v := result.(tui.YieldModel).View()
	assert.NotEmpty(t, v)
}

// ─── View in feedback state ───────────────────────────────────────────────────

func TestView_feedback_state_shows_prompt(t *testing.T) {
	m := tui.NewYieldModel(basicReq())
	intermediate, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	v := intermediate.(tui.YieldModel).View()
	assert.Contains(t, v, "Rejected", "feedback state should show rejection prompt")
}
