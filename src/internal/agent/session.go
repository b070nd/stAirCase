package agent

import (
	"crypto/rand"
	"fmt"

	"github.com/b070nd/stAirCase/src/internal/orchestrator"
)

// newSessionID is a random version 4 UUID, the form Claude Code and Gemini CLI accept for a session id.
func newSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6], b[8] = (b[6]&0x0f)|0x40, (b[8]&0x3f)|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// sessionFor is the vendor session a segment of the run uses: the one the run resumes, or a new one whose id staircase
// chooses. It is put on the audit chain before the agent starts, so a continuation after a kill can find it.
func sessionFor(env *orchestrator.AgentEnv, agent string) (id string, resume bool) {
	if env.Session != nil && env.Session.Resume && env.Session.ID != "" {
		env.RecordSession(agent, env.Session.ID, "resumed")
		return env.Session.ID, true
	}
	id = newSessionID()
	env.RecordSession(agent, id, "started")
	return id, false
}
