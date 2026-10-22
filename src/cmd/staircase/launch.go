package main

import (
	"github.com/b070nd/stAirCase/src/internal/agent"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/plan"
)

// launch is everything one run needs from the command line: its terms, how it is carried out, and the session around it. The
// flags of every command fill cli; a handler passes its launch on, and what derives one (host, review, seal) makes a copy.
type launch struct {
	terms orchestrator.Terms // as the flags give them; NewTerms judges them

	dryRun, force, skipGates, autoStash, debug, reconcile, ackDrift bool
	approvalPort                                                    int
	approvalToken, metricsAddr, otelEndpoint, recordLLM, replayLLM  string
	agent, model                                                    string // the built-in agent or a harness, and its model
	passEnv                                                         []string

	allow []string // the paths a session's task may change
	// profileScope says the allow list came from the project's profile, not from a flag
	profileScope bool
	scopeInTerms bool         // the run command: the scope narrows the plan's, in the run's terms (a session's becomes its story's scope instead)
	yes          bool         // start without asking to confirm the task
	review       *plan.Review // what staircase review and seal bring in
	finish       func(root, branch string, storyID int64) error
	host         *agent.HostIO // a runner's session the Claude Code agent stands in for (staircase host)
	// resumesSession and resumesRun are the hosted session a new run resumes and the decided run it follows (ADR 0008).
	resumesSession string
	resumesRun     int64
}

// cli is what the flags give.
var cli = launch{finish: defaultFinish}
