package agent

import "github.com/b070nd/stAirCase/src/internal/orchestrator"

// withContinuation is the agent's first message, with what a continued run tells a fresh session appended: the task as it
// stood, built from the audit chain. A first segment has nothing to add.
// An away run's brief (ADR 0008) comes first: how to work when nobody answers.
func withContinuation(prompt string, env *orchestrator.AgentEnv) string {
	if env == nil {
		return prompt
	}
	if env.Brief != "" {
		prompt = env.Brief + "\n\n" + prompt
	}
	if env.Continuation == "" {
		return prompt
	}
	return prompt + "\n\n" + env.Continuation
}
