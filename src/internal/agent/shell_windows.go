package agent

import "os/exec"

// killProcessGroup: Windows execution is untested (build only); the default
// cancellation kills the shell.
func killProcessGroup(*exec.Cmd) {}
