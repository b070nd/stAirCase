//go:build !windows

package agent

import (
	"os/exec"
	"syscall"
)

// killProcessGroup makes cancelling cmd kill everything it started, not just
// the shell, so no child keeps writing to the worktree after its tool call.
func killProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
