//go:build !windows

package agent_test

import (
	"os/exec"
	"syscall"
)

// ownGroup makes a fake host's hook command a process group of its own and kills the
// whole group when the host gives up on it, as a host that stops a hook for good does.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
