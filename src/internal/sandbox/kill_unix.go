//go:build !windows

package sandbox

import (
	"os/exec"
	"syscall"
)

// KillGroup makes cancelling cmd kill everything it started, not just
// the shell, so no child keeps writing to the worktree after its tool call.
func KillGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
