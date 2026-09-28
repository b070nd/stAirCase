package main

import (
	"os"

	"github.com/b070nd/stAirCase/src/internal/agent"
	"github.com/spf13/cobra"
)

// hookCmd documents `staircase hook`; main runs it before cobra, so a
// failure always blocks. This entry serves the rare call with global flags
// in front (`staircase --dir … hook …`).
var hookCmd = &cobra.Command{
	Use:   "hook <agent> [--governed]",
	Short: "Pass an agent's hook call to the run that governs it (used by agent adapters)",
	Long: `Coding agents such as Claude Code call this command before and after each
tool call. It reads the call on standard input, passes it to the stAirCase run
that started the agent, and prints the run's answer.

Exit code 2 blocks the tool call. Every failure blocks: a missing or invalid
session, a run that does not answer or refuses the token, an unknown agent.

--governed is set whenever stAirCase starts the agent: without the run's
session file (STAIRCASE_HOOK_FILE) the call is blocked. Without --governed, as
in a hook installed once for a user or a company, calls pass through when no
governed session is running.

Supported agents: claude-code.`,
	DisableFlagParsing: true, // every argument goes to RunHook, which blocks on anything unknown
	Run: func(_ *cobra.Command, args []string) {
		os.Exit(agent.RunHook(args, os.Stdin, os.Stdout, os.Stderr))
	},
}

func init() {
	rootCmd.AddCommand(hookCmd)
}
