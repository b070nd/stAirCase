package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ManagedClaudeSettings is the managed-settings.json a company deploys so that
// every Claude Code session on its machines goes through stAirCase: the
// --require hook blocks tool calls outside a governed session and governs
// them inside one, and allowManagedHooksOnly keeps user and repository hooks
// from loading. bin is where staircase is installed on those machines.
func ManagedClaudeSettings(bin string) ([]byte, error) {
	var s map[string]any
	if err := json.Unmarshal(hookSettings(HookCommand(bin, "claude-code", "--require"), nil), &s); err != nil {
		return nil, err
	}
	s["allowManagedHooksOnly"] = true
	return json.MarshalIndent(s, "", "  ")
}

// ManagedCodexHooks is the [hooks] block of a company's managed Codex
// configuration, with the same --require hook for every event staircase uses.
func ManagedCodexHooks(bin string) string {
	var b strings.Builder
	for _, event := range []string{"SessionStart", "PreToolUse", "PostToolUse"} {
		fmt.Fprintf(&b, "[[hooks.%s]]\nmatcher = \"*\"\n\n[[hooks.%s.hooks]]\ntype = \"command\"\ncommand = %s\ntimeout = %d\n\n",
			event, event, tomlString(HookCommand(bin, "codex", "--require")), hookTimeout)
	}
	return b.String()
}
