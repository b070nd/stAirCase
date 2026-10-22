package agent

import "github.com/b070nd/stAirCase/src/internal/orchestrator"

// briefing is the text an agent is told, composed in one place for every agent, the built-in runtime and the hosted harnesses alike. The
// order is fixed: an away run's brief first (ADR 0008: how to work when nobody answers), then the task (the PRD, the stories and the
// reviewers' lessons, as the plan compiled them), then what a continued run tells a fresh session (the task as it stood, built from
// the audit chain; a first segment has nothing to add), then the tail the agent's own harness needs (its rules, the repository map).
// An empty piece leaves no gap.
func briefing(task string, env *orchestrator.AgentEnv, tail string) string {
	text := task
	if env != nil {
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
