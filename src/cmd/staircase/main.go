// Command staircase is the command line of stAirCase: it stands between an AI agent and your repository, asks for a decision on every
// change, commits only what was approved, and writes the audit chain and the change certificate that prove it.
package main

import (
	"os"

	"github.com/b070nd/stAirCase/src/internal/agent"
)

func main() {
	// A hook must block on every failure (exit 2): agents let a tool call run
	// when its hook fails any other way. So it runs before cobra and the
	// workspace setup, which exit 1 on their errors.
	if len(os.Args) > 1 && os.Args[1] == "hook" {
		os.Exit(agent.RunHook(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
	Execute()
}
