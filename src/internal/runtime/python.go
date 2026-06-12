//go:build !windows

package runtime

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
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

	// RecordLLM, when non-empty, is a file path where the Python harness must
	// write a JSON record of every LLM exchange (for replay in later test runs).
	RecordLLM string `json:"record_llm,omitempty"`

	// ReplayLLM, when non-empty, is a file path from which the Python harness
	// must replay LLM exchanges deterministically instead of calling the real API.
	ReplayLLM string `json:"replay_llm,omitempty"`

	// RunnerPath, when non-empty, is a directory that must be prepended to
	// sys.path so that `import staircase_runner` resolves to the embedded
	// package written by BootstrapVenv into the workspace venv.
	RunnerPath string `json:"runner_path,omitempty"`

	// AllowShellExec, when true, includes run_shell in the agent tool list.
	// Absent (false) means shell execution is disabled — the safe default.
	AllowShellExec bool `json:"allow_shell_exec,omitempty"`

	// Traceparent is the W3C trace-context value of the orchestrator's run
	// span. The Python runtime may use it to join the distributed trace; it
	// is informational and safe to ignore.
	Traceparent string `json:"traceparent,omitempty"`
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
// The script is delivered via an inherited file descriptor (ExtraFiles[0] → fd
// 3) and executed as `python /dev/fd/3`.  This means the orchestrator does not
// need to pass a file-system path to the runtime layer — the topology content
// arrives through the fd, not a well-known tmp path (CHECK 5.4.2).
//
// The process is wrapped in context.WithCancel so that calling Kill() or
// cancelling the parent ctx sends SIGKILL (zombie reaper).
// LaunchPythonOptions carries optional bootstrap parameters for LaunchPython.
// Zero value is safe: all fields default to empty/false (feature disabled).
type LaunchPythonOptions struct {
	// RecordLLM, if non-empty, is forwarded to Python so it records LLM exchanges
	// to the named file for later deterministic replay.
	RecordLLM string
	// ReplayLLM, if non-empty, is forwarded to Python so it replays LLM exchanges
	// from the named file instead of calling the real API.
	ReplayLLM string
	// AllowShellExec, when true, includes run_shell in the agent tool list.
	// Defaults to false (disabled) — callers must explicitly opt in.
	AllowShellExec bool
	// ScrubStderr, when non-nil, is applied to every Python stderr line before
	// it is logged. The orchestrator passes a redactor backed by the IPC
	// server's delivered-secrets cache so an agent printing a plaintext secret
	// to stderr cannot leak it into orchestrator logs (CHECK 4.4.3).
	ScrubStderr func(string) string
	// Traceparent propagates the orchestrator's trace context (W3C format).
	Traceparent string
}

func LaunchPython(ctx context.Context, wsDir string, scriptFile *os.File, socketPath, token string, opts ...LaunchPythonOptions) (*PythonProcess, error) {
	venvPath := filepath.Join(wsDir, "venv")
	pythonBin := venvPythonBin(venvPath)

	procCtx, cancel := context.WithCancel(ctx)
	// Pass the script via /dev/fd/3 (ExtraFiles[0] becomes fd 3 in the child).
	cmd := exec.CommandContext(procCtx, pythonBin, "/dev/fd/3")
	cmd.ExtraFiles = []*os.File{scriptFile} // fd 3 = topology script (CHECK 5.4.2)

	// Place the Python process in its own process group so that signals sent
	// via Kill() (negative PID = group) reach all Python children, not just the
	// direct subprocess (CHECK 5.4.3).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Merge optional bootstrap fields (needed below for stderr scrubbing).
	var lpo LaunchPythonOptions
	if len(opts) > 0 {
		lpo = opts[0]
	}

	// Pipe Python's stderr through a goroutine that prefixes each line with
	// "[python]" so tracebacks are distinguishable in the orchestrator log
	// (CHECK 5.4.4 — stderr must be captured and routed, not silently inherited).
	// Each line is passed through ScrubStderr (when set) so delivered secrets
	// never reach the log in plaintext.
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}
	go func() {
		sc := bufio.NewScanner(stderrPipe)
		for sc.Scan() {
			line := sc.Text()
			if lpo.ScrubStderr != nil {
				line = lpo.ScrubStderr(line)
			}
			obs.Log.Info("python stderr", "line", line)
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
		Type:           "bootstrap",
		SocketPath:     socketPath,
		Token:          token,
		RecordLLM:      lpo.RecordLLM,
		ReplayLLM:      lpo.ReplayLLM,
		RunnerPath:     RunnerInjectDir(venvPath),
		AllowShellExec: lpo.AllowShellExec,
		Traceparent:    lpo.Traceparent,
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
