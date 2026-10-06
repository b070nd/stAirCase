package agent

import "github.com/b070nd/stAirCase/src/internal/orchestrator"

// withContinuation is the agent's first message, with what a continued run tells a fresh session appended: the task as it
// stood, built from the audit chain. A first segment has nothing to add.
func withContinuation(prompt string, env *orchestrator.AgentEnv) string {
	if env == nil || env.Continuation == "" {
		return prompt
	}
	return prompt + "\n\n" + env.Continuation
}
