package orchestrator

import (
	"fmt"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/persistence"
)

// HostedRun is the latest run whose verified chain names session as its Claude Code session: the run a runner's session was
// governed as (ADR 0008). The chain is the authority, so a chain that cannot be read while looking is an error rather than
// skipped, since it could be the newer run that names the session. nil: no run names it.
func HostedRun(store *persistence.Store, session string) (*domain.Run, error) {
	runs, err := store.ListRuns()
	if err != nil {
		return nil, err
	}
	for i := range runs { // newest first
		h, err := history.Read(store, runs[i].ID)
		if err != nil {
			return nil, fmt.Errorf("looking for the run of session %s: %w", session, err)
		}
		if h.Session.Agent == "claude-code" && h.Session.ID == session {
			return &runs[i], nil
		}
	}
	return nil, nil
}
