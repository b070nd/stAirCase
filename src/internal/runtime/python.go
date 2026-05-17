//go:build !windows

package runtime

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/b070nd/staircase-core/src/internal/obs"
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

// Kill terminates the Python process gracefully: SIGTERM is sent first to
// give the process a chance to flush state, then after a 5-second grace period
// the entire process group receives SIGKILL (CHECK 5.4.5).
//
// Sending signals to -pid (negative) targets the process group created by
// Setpgid:true in LaunchPython (CHECK 5.4.3), so orphaned Python children are
// also cleaned up.
func (p *PythonProcess) Kill() {
	if p.cmd.Process != nil {
		// SIGTERM to the whole process group (negative PID = group).
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM)
		select {
		case <-p.Done:
			p.cancel() // release context resources
			return
		case <-time.After(5 * time.Second):
			// Grace period expired — hard-kill the process group.
			_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
		}
	}
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

	// Place the Python process in its own process group so that signals sent
	// via Kill() (negative PID = group) reach all Python children, not just the
	// direct subprocess (CHECK 5.4.3).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Pipe Python's stderr through a goroutine that prefixes each line with
	// "[python]" so tracebacks are distinguishable in the orchestrator log
	// (CHECK 5.4.4 — stderr must be captured and routed, not silently inherited).
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}
	go func() {
		sc := bufio.NewScanner(stderrPipe)
		for sc.Scan() {
			obs.Log.Info("python stderr", "line", sc.Text())
		}
	}()

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
	_ = stdin.Close()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	return &PythonProcess{cmd: cmd, cancel: cancel, Done: done}, nil
}
