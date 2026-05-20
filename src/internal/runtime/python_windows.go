//go:build windows

package runtime

import (
	"context"
	"fmt"
	"os"
)

// BootstrapToken is not supported on Windows.
func BootstrapToken() (string, error) {
	return "", fmt.Errorf("stAirCase Python runtime not supported on Windows; use WSL2 or Docker Desktop")
}

// BootstrapMessage mirrors the non-Windows definition so the orchestrator
// compiles on Windows even though LaunchPython always returns an error.
type BootstrapMessage struct {
	Type       string `json:"type"`
	SocketPath string `json:"socket_path"`
	Token      string `json:"token"`
	RecordLLM  string `json:"record_llm,omitempty"`
	ReplayLLM  string `json:"replay_llm,omitempty"`
	RunnerPath string `json:"runner_path,omitempty"`
}


// LaunchPythonOptions mirrors the non-Windows definition.
type LaunchPythonOptions struct {
	RecordLLM string
	ReplayLLM string
}

// PythonProcess mirrors the non-Windows definition.
// On Windows it is never populated because LaunchPython always errors.
type PythonProcess struct {
	Done <-chan error
}

// PID returns -1; the process is never started on Windows.
func (p *PythonProcess) PID() int { return -1 }

// Kill is a no-op on Windows.
func (p *PythonProcess) Kill() {}

// LaunchPython always returns an error on Windows.
// Use WSL2 or Docker Desktop to run stAirCase on Windows hosts.
func LaunchPython(_ context.Context, _ string, _ *os.File, _, _ string, _ ...LaunchPythonOptions) (*PythonProcess, error) {
	return nil, fmt.Errorf("stAirCase Python runtime is not supported on Windows; use WSL2 or Docker Desktop")
}
