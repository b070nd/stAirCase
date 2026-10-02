//go:build !windows

package tui

import (
	"os"
	"syscall"
)

// raiseInterrupt sends this process the SIGINT a Ctrl-C would have sent had the
// terminal not been in raw mode.
func raiseInterrupt() { _ = syscall.Kill(os.Getpid(), syscall.SIGINT) }
