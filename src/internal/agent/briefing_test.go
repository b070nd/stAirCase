package agent

import (
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
