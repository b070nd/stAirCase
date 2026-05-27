package gate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func init() {
	Register(&shellSandboxGate{})
}

// shellSandboxGate warns when the compiled graph_exec script uses run_shell
// but no OS sandbox tool (bwrap, docker) is present in PATH.
//
// run_shell executes arbitrary shell commands as the same OS user as the
// orchestrator with full host filesystem access.  An OS sandbox (bubblewrap,
// docker) limits blast radius; without one, the trust boundary is HITL
// approval alone.
//
// The gate is advisory (WARN) so it never blocks a run — operators on
// developer workstations are expected to see and acknowledge this limitation.
// Use `staircase run --no-shell-exec` to eliminate the risk entirely.
type shellSandboxGate struct{}

func (*shellSandboxGate) Name() string       { return "runtime.shell_sandbox" }
func (*shellSandboxGate) Category() string   { return "security" }
func (*shellSandboxGate) Severity() Severity { return SeverityWarn }

func (*shellSandboxGate) Run(ctx Context) Result {
	const name = "runtime.shell_sandbox"

	// Locate the compiled graph_exec script for this case.
	scriptPath := filepath.Join(ctx.WsDir, fmt.Sprintf("graph_exec_case%d.py", ctx.CaseID))
	src, err := os.ReadFile(scriptPath)
	if err != nil {
		if os.IsNotExist(err) {
			return skip(name, "security", SeverityWarn,
				"graph_exec script not found — run 'staircase compile' first")
		}
		return skip(name, "security", SeverityWarn, "cannot read graph_exec script: "+err.Error())
	}

	// Only warn when run_shell is actually present in the compiled script.
	if !strings.Contains(string(src), "run_shell") {
		return pass(name, "security", SeverityWarn, "run_shell not present in compiled script")
	}

	// Check for an OS sandbox tool in PATH.
	for _, tool := range []string{"bwrap", "docker", "firejail"} {
		if p, err := exec.LookPath(tool); err == nil {
			return pass(name, "security", SeverityWarn,
				fmt.Sprintf("run_shell present; OS sandbox available: %s", p))
		}
	}

	return warn(name, "security",
		"run_shell is OS-unsandboxed — no bwrap/docker/firejail in PATH. "+
			"Shell commands run as the orchestrator user with full host access. "+
			"Use --no-shell-exec to disable, or install bubblewrap/docker for isolation.")
}
