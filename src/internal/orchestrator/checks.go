package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/b070nd/stAirCase/src/internal/history"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/sandbox"
	"github.com/b070nd/stAirCase/src/internal/tui"
)

// defaultCheckTimeout bounds one check, such as a test suite, unless the run sets
// its own (--check-timeout).
const defaultCheckTimeout = 15 * time.Minute

// runChecks runs each check on a clean checkout of commit, in the sandbox
// (mode as for approved commands), and returns their results for the
// certificate. A check that fails is reported and recorded; it does not undo
// the commit (staircase verify refuses it).
func (r *Runner) runChecks(ctx context.Context, runID int64, repo *GitRepo, commit string, checks []string, mode string, timeout time.Duration, overlays []sandbox.Overlay) []certificate.Check {
	if len(checks) == 0 {
		return nil
	}
	dir, cleanup, err := scratchCheckout(repo.path, commit, "staircase-check-")
	var out []certificate.Check
	for _, command := range checks {
		c := certificate.Check{Command: command, ExitCode: -1}
		var tail string
		if err == nil {
			c, tail = runCheck(ctx, dir, command, mode, r.wsDir, timeout, overlays)
		} else {
			tail = "no checkout of the commit: " + err.Error()
		}
		if c.ExitCode == 0 {
			fmt.Fprintf(os.Stdout, "   Check passed: %s\n", command)
		} else {
			fmt.Fprintf(os.Stdout, "   Check failed (exit %d): %s\n%s\n", c.ExitCode, command, indent(tail))
		}
		if !c.Sandboxed {
			fmt.Fprintf(os.Stdout, "      (ran without a sandbox)\n")
		}
		_ = r.log(runID).Append(history.CheckRan{Command: command, ExitCode: c.ExitCode, OutputSHA256: c.OutputSHA256, Sandboxed: c.Sandboxed, Environment: c.Environment})
		out = append(out, c)
	}
	if cleanup != nil {
		cleanup()
	}
	return out
}

// runCheck runs one check in dir; it returns the result and the end of the
// output, for the terminal only.
func runCheck(ctx context.Context, dir, command, mode, workspace string, timeout time.Duration, overlays []sandbox.Overlay) (certificate.Check, string) {
	c := certificate.Check{Command: command, ExitCode: -1, Environment: environmentNames(overlays)}
	if timeout <= 0 {
		timeout = defaultCheckTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd, sandboxed, cleanup, err := sandbox.CommandWith(ctx, dir, dir, command, mode, overlays, workspace)
	if err != nil {
		return c, err.Error()
	}
	defer cleanup()
	c.Sandboxed = sandboxed
	cmd.WaitDelay = 5 * time.Second
	sandbox.KillGroup(cmd)
	h, last := sha256.New(), &lastBytes{max: 2048}
	cmd.Stdout = io.MultiWriter(h, last)
	cmd.Stderr = cmd.Stdout
	err = cmd.Run()
	c.OutputSHA256 = hex.EncodeToString(h.Sum(nil))
	var exitErr *exec.ExitError
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return c, fmt.Sprintf("timed out after %s", timeout)
	case errors.As(err, &exitErr):
		c.ExitCode = exitErr.ExitCode()
	case err != nil:
		return c, err.Error()
	default:
		c.ExitCode = 0
	}
	return c, string(last.b)
}

// lastBytes keeps the last max bytes written to it.
type lastBytes struct {
	max int
	b   []byte
}

func (l *lastBytes) Write(p []byte) (int, error) {
	l.b = append(l.b, p...)
	if len(l.b) > l.max {
		l.b = l.b[len(l.b)-l.max:]
	}
	return len(p), nil
}

func indent(s string) string {
	s = strings.TrimRight(tui.Safe(strings.ToValidUTF8(s, "")), "\n") // the output of code an agent wrote
	if s == "" {
		return ""
	}
	return "      │ " + strings.ReplaceAll(s, "\n", "\n      │ ")
}
