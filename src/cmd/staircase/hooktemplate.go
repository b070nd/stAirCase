package main

import (
	"fmt"
	"os"

	"github.com/b070nd/stAirCase/src/internal/agent"
	"github.com/spf13/cobra"
)

var hookTemplateBin string

var hookTemplateCmd = &cobra.Command{
	Use:   "hook-template <claude-code | codex>",
	Short: "Print the managed settings that make every agent session on a company's machines go through stAirCase",
	Long: `Prints settings a company deploys through its device management so that every
Claude Code or Codex session on its machines goes through stAirCase. The hook
they install blocks every tool call outside a governed session (start one with
staircase claude or staircase codex) and governs the calls inside one.

  claude-code  managed-settings.json (it also sets allowManagedHooksOnly, so
               user and repository hooks do not load)
  codex        the [hooks] block of the managed Codex configuration
  gemini       Gemini CLI's system settings.json (work in progress)

--bin is where staircase is installed on those machines (default: this
program). See docs/managed.md.`,
	Args: cobra.ExactArgs(1),
	RunE: hookTemplateHandler,
}

func init() {
	hookTemplateCmd.Flags().StringVar(&hookTemplateBin, "bin", "", "Path of staircase on the managed machines (default: this program)")
	rootCmd.AddCommand(hookTemplateCmd)
}

func hookTemplateHandler(_ *cobra.Command, args []string) error {
	bin := hookTemplateBin
	if bin == "" {
		var err error
		if bin, err = os.Executable(); err != nil {
			return err
		}
	}
	switch args[0] {
	case "claude-code":
		b, err := agent.ManagedClaudeSettings(bin)
		if err != nil {
			return err
		}
		fmt.Println(string(b))
	case "codex":
		fmt.Print(agent.ManagedCodexHooks(bin))
	case "gemini":
		b, err := agent.ManagedGeminiSettings(bin)
		if err != nil {
			return err
		}
		fmt.Println(string(b))
	default:
		return fmt.Errorf("no managed settings for %q: use claude-code, codex or gemini", args[0])
	}
	return nil
}
