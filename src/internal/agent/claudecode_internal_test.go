package agent

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestClaudeCode_hook_failure_blocks: when staircase cannot answer, the hook
// must exit 2 (Claude Code lets the tool run on any other failure).
func TestClaudeCode_hook_failure_blocks(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", hookCommand("http://127.0.0.1:1/hook", "tok"))
	cmd.Stdin = strings.NewReader("{}")
	_ = cmd.Run()
	assert.Equal(t, 2, cmd.ProcessState.ExitCode())
}
