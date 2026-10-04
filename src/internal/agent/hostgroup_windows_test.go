//go:build windows

package agent_test

import "os/exec"

func ownGroup(*exec.Cmd) {}
