package agent

import (
	"strings"

	"github.com/b070nd/stAirCase/src/internal/orchestrator"
)

// briefing is the text an agent is told, composed in one place for every agent, built-in or hosted. The order is fixed: an away run's
// brief (ADR 0008), the project's instructions, the task (PRD, stories and reviewers' lessons), what a continued run tells a fresh
// session (built from the audit chain), then what the agent's own harness needs (its rules, the repository map). An empty piece leaves
// no gap.
func briefing(task string, env *orchestrator.AgentEnv, tail string) string {
	text := task
	if env != nil {
		if block := instructionsBlock(env); block != "" {
			text = block + "\n\n" + text
		}
		if env.Brief != "" {
			text = env.Brief + "\n\n" + text
		}
		if env.Continuation != "" {
			text += "\n\n" + env.Continuation
		}
	}
	if tail != "" {
		text += "\n\n" + tail
	}
	return text
}

// instructionsBlock is the project's instructions as an agent reads them, or "" when the project gave none. They are the house's rules,
// asked of the model: nothing here enforces them.
func instructionsBlock(env *orchestrator.AgentEnv) string {
	if env == nil || strings.TrimSpace(env.Instructions) == "" {
		return ""
	}
	return "Instructions for this project, from the person who runs this work (they apply to every task in it; the task comes after them):\n\n" +
		strings.TrimSpace(env.Instructions)
}

// BriefingFor is the exact text the agent of a harness ("claude-code", "codex", "gemini", "opencode", or "" for the built-in agents) is
// told for a task, given the run's environment and, for the built-in agents, the repository map. It is what the agents themselves
// are given, so what `staircase case briefing` shows is what they read.
func BriefingFor(harness, task string, env *orchestrator.AgentEnv, repoContext string) string {
	switch harness {
	case "claude-code":
		return briefing(task, env, "")
	case "codex":
		return briefing(task, env, codexRules)
	case "gemini":
		return briefing(task, env, geminiRules)
	case "opencode":
		return briefing(task, env, opencodeRules)
	}
	return strings.TrimSpace(briefing(task, env, repoContext))
}
