package gate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/b070nd/staircase-core/src/internal/obs"
)

// pluginGateDef is one entry in $wsDir/gates.json.
type pluginGateDef struct {
	Name           string `json:"name"`
	Category       string `json:"category"`
	Severity       string `json:"severity"`
	Script         string `json:"script"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

// PluginGate is a Gate backed by an external script.
// The script receives a JSON line on stdin and must write a JSON line to stdout.
//
// Subprocess sandbox (CHECK 11.3, 11.6):
//   - env is cleared (cmd.Env = []string{}) so no host secrets or $STAIRCASE_DIR leak
//   - cwd is a fresh tempdir, removed after execution
//   - execution is bounded by a configurable timeout (CHECK 11.4)
type PluginGate struct{ def pluginGateDef }

func (p *PluginGate) Name() string     { return p.def.Name }
func (p *PluginGate) Category() string { return p.def.Category }
func (p *PluginGate) Severity() Severity {
	if p.def.Severity == "" {
		return SeverityWarn
	}
	return Severity(p.def.Severity)
}

// Run executes the plugin script and returns the gate result.
// If the script times out, produces malformed output, or exits non-zero,
// Run returns StatusFail — it never panics on unexpected output (CHECK 11.5).
func (p *PluginGate) Run(ctx Context) Result {
	timeout := time.Duration(p.def.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	// Fresh isolated working directory, removed on exit.
	tmpDir, err := os.MkdirTemp("", "staircase-gate-*")
	if err != nil {
		return fail(p.def.Name, p.def.Category, p.Severity(),
			fmt.Sprintf("plugin: cannot create tmpdir: %v", err))
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	cmdCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, p.def.Script) //nolint:gosec
	cmd.Env = []string{}                             // CHECK 11.3, 11.6 — no env inherited
	cmd.Dir = tmpDir                                 // CHECK 11.3 — fresh cwd
	// WaitDelay ensures cmd.Output() returns promptly after context cancellation
	// even when child processes keep I/O pipes open (e.g. a shell spawning sleep).
	cmd.WaitDelay = timeout

	// Write JSON input to stdin.
	input, _ := json.Marshal(map[string]any{
		"case_id": ctx.CaseID,
		"ws_dir":  ctx.WsDir,
	})
	cmd.Stdin = bytes.NewReader(append(input, '\n'))

	// Capture stderr separately so it can be surfaced in the message.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		detail := stderr.String()
		if detail == "" {
			detail = err.Error()
		}
		return fail(p.def.Name, p.def.Category, p.Severity(),
			fmt.Sprintf("plugin error: %s", detail))
	}

	// Decode output — malformed output must not crash (CHECK 11.5).
	var pluginResult struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &pluginResult); err != nil {
		return fail(p.def.Name, p.def.Category, p.Severity(),
			fmt.Sprintf("plugin malformed output: %v (raw: %s)", err, string(out)))
	}

	return Result{
		Name:     p.def.Name,
		Category: p.def.Category,
		Severity: p.Severity(),
		Status:   Status(pluginResult.Status),
		Message:  pluginResult.Message,
	}
}

// loadPluginGates reads $wsDir/gates.json and returns one PluginGate per entry.
// Returns nil (no plugin gates) when the file is absent — not an error.
// Invalid JSON is logged as a warning and treated as zero plugin gates.
func loadPluginGates(wsDir string) []Gate {
	path := filepath.Join(wsDir, "gates.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		obs.Log.Warn("load plugin gates", "path", path, "err", err)
		return nil
	}
	var defs []pluginGateDef
	if err := json.Unmarshal(data, &defs); err != nil {
		obs.Log.Warn("gates.json parse error", "path", path, "err", err)
		return nil
	}
	gates := make([]Gate, len(defs))
	for i, d := range defs {
		gates[i] = &PluginGate{def: d}
	}
	return gates
}
