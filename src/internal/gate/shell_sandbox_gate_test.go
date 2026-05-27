package gate_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/b070nd/staircase-core/src/internal/gate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeScript writes a fake graph_exec script to wsDir for the given caseID.
func writeScript(t *testing.T, wsDir string, caseID int64, content string) {
	t.Helper()
	p := filepath.Join(wsDir, fmt.Sprintf("graph_exec_case%d.py", caseID))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
}

// TestShellSandboxGate_skip_no_script returns SKIP when the graph_exec file
// has not been compiled yet (before staircase compile).
func TestShellSandboxGate_skip_no_script(t *testing.T) {
	ctx, _ := newGateEnv(t)
	_, ctx.CaseID = makeCase(t, ctx.Store)
	res := gate.ExportedShellSandboxRun(ctx)
	assert.Equal(t, gate.StatusSkip, res.Status)
}

// TestShellSandboxGate_pass_no_run_shell returns PASS when the compiled script
// does not contain run_shell (agent topology did not request shell access).
func TestShellSandboxGate_pass_no_run_shell(t *testing.T) {
	ctx, wsDir := newGateEnv(t)
	_, ctx.CaseID = makeCase(t, ctx.Store)
	writeScript(t, wsDir, ctx.CaseID, `# graph_exec — no shell tool`)
	res := gate.ExportedShellSandboxRun(ctx)
	assert.Equal(t, gate.StatusPass, res.Status)
}

// TestShellSandboxGate_warn_run_shell_no_sandbox returns WARN when run_shell
// is present in the compiled script but no OS sandbox tool is in PATH.
// We rely on the fact that the test environment is unlikely to have bwrap,
// docker, and firejail all absent — if any is present the test is skipped.
func TestShellSandboxGate_warn_run_shell_no_sandbox(t *testing.T) {
	for _, tool := range []string{"bwrap", "docker", "firejail"} {
		if _, err := exec.LookPath(tool); err == nil {
			t.Skipf("sandbox tool %q present in PATH — skip unsandboxed-warn test", tool)
		}
	}
	ctx, wsDir := newGateEnv(t)
	_, ctx.CaseID = makeCase(t, ctx.Store)
	writeScript(t, wsDir, ctx.CaseID, `def run_shell(): pass`)
	res := gate.ExportedShellSandboxRun(ctx)
	assert.Equal(t, gate.StatusWarn, res.Status)
	assert.Contains(t, res.Message, "OS-unsandboxed")
}

// TestShellSandboxGate_metadata verifies Name, Category, and Severity.
func TestShellSandboxGate_metadata(t *testing.T) {
	assert.Equal(t, "runtime.shell_sandbox", gate.ExportedShellSandboxName())
	assert.Equal(t, "security", gate.ExportedShellSandboxCategory())
	assert.Equal(t, gate.SeverityWarn, gate.ExportedShellSandboxSeverity())
}
