package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
)

// BootstrapToken generates a cryptographically secure 32-byte random token
// used to authenticate the Python process to the IPC server.
// A fresh token is generated per run; it is passed over stdin (not env vars).
func BootstrapToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// BootstrapMessage is the first JSON-Lines message written to Python's stdin.
// After reading it Python must close its stdin reader and connect to the UDS socket.
type BootstrapMessage struct {
	Type       string `json:"type"`        // "bootstrap"
	SocketPath string `json:"socket_path"` // path to the Unix Domain Socket
	Token      string `json:"token"`       // session auth token
}

// PythonProcess wraps a managed Python subprocess.
// Done is closed with the process exit error when the process terminates.
type PythonProcess struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	Done   <-chan error
}

// PID returns the OS process ID, or -1 if the process has not started yet.
func (p *PythonProcess) PID() int {
	if p.cmd.Process == nil {
		return -1
	}
	return p.cmd.Process.Pid
}

// Kill cancels the process context, which causes exec.CommandContext to send
// SIGKILL to the Python process and all of its children.
func (p *PythonProcess) Kill() {
	p.cancel()
}

// LaunchPython starts the isolated Python runtime inside the workspace venv.
//
// Bootstrap sequence (spec §2.3):
//  1. Go writes a single JSON-Lines BootstrapMessage to Python's stdin.
//  2. stdin is closed — all subsequent communication happens over the UDS.
//  3. Python reads the message, connects to the socket, and authenticates
//     with the token before sending any telemetry or secret requests.
//
// The process is wrapped in context.WithCancel so that calling Kill() or
// cancelling the parent ctx sends SIGKILL (zombie reaper).
func LaunchPython(ctx context.Context, wsDir, scriptPath, socketPath, token string) (*PythonProcess, error) {
	venvPath := filepath.Join(wsDir, "venv")
	pythonBin := venvPythonBin(venvPath)

	procCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(procCtx, pythonBin, scriptPath)

	// Inherit stderr so Python tracebacks surface in the terminal.
	cmd.Stderr = nil // nil → inherited from parent process

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start python: %w", err)
	}

	// Write the bootstrap message then seal stdin.
	enc := json.NewEncoder(stdin)
	if err := enc.Encode(BootstrapMessage{
		Type:       "bootstrap",
		SocketPath: socketPath,
		Token:      token,
	}); err != nil {
		cancel()
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("write bootstrap: %w", err)
	}
	stdin.Close()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	return &PythonProcess{cmd: cmd, cancel: cancel, Done: done}, nil
}
