package agent

import (
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/stretchr/testify/assert"
)

// TestBriefing_order (one place composes what an agent is told): the away brief first, the task, what a continuation adds, then the
// harness's own tail; a first segment's task is untouched and an empty piece leaves no gap.
func TestBriefing_order(t *testing.T) {
	assert.Equal(t, "task", briefing("task", &orchestrator.AgentEnv{}, ""), "a first segment's prompt is untouched")
	assert.Equal(t, "task", briefing("task", nil, ""), "no env")
	assert.Equal(t, "task\n\nyou are continuing", briefing("task", &orchestrator.AgentEnv{Continuation: "you are continuing"}, ""))
	assert.Equal(t, "BRIEF\n\nadd a test", briefing("add a test", &orchestrator.AgentEnv{Brief: "BRIEF"}, ""), "an away run's brief comes first")
	assert.Equal(t, "BRIEF\n\nTASK\n\nCONTINUATION\n\nRULES", briefing("TASK", &orchestrator.AgentEnv{Brief: "BRIEF", Continuation: "CONTINUATION"}, "RULES"))
	assert.Equal(t, "TASK\n\nMAP", briefing("TASK", nil, "MAP"))
}

// TestBriefing_the_projects_instructions_come_before_the_task (a project profile): after an away run's brief and before the task, for
// every agent; a project that gave none leaves the text as it was.
func TestBriefing_the_projects_instructions_come_before_the_task(t *testing.T) {
	env := &orchestrator.AgentEnv{Brief: "BRIEF", Instructions: "  Add the house header.\n", Continuation: "CONT"}
	got := briefing("TASK", env, "RULES")
	assert.Equal(t, "BRIEF\n\nInstructions for this project, from the person who runs this work (they apply to every task in it; the task comes after them):\n\nAdd the house header.\n\nTASK\n\nCONT\n\nRULES", got)
	assert.Equal(t, "TASK", briefing("TASK", &orchestrator.AgentEnv{Instructions: " \n"}, ""), "blank instructions say nothing")

	for _, harness := range []string{"claude-code", "codex", "gemini", "opencode", ""} {
		text := BriefingFor(harness, "TASK", &orchestrator.AgentEnv{Instructions: "Add the house header."}, "MAP")
		assert.Contains(t, text, "Add the house header.", "harness %q", harness)
		assert.Less(t, strings.Index(text, "Add the house header."), strings.Index(text, "TASK"), "harness %q: the instructions come first", harness)
	}
	assert.Contains(t, BriefingFor("", "TASK", nil, "MAP"), "MAP", "the built-in agents get the repository map")
	assert.Contains(t, BriefingFor("codex", "TASK", nil, "MAP"), "TASK")
	assert.NotContains(t, BriefingFor("codex", "TASK", nil, "MAP"), "MAP")
}
