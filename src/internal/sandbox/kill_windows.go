package sandbox

import "os/exec"

// KillGroup: Windows execution is untested (build only); the default
// cancellation kills the shell.
func KillGroup(*exec.Cmd) {}
