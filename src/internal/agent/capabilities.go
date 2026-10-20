package agent

import (
	"errors"
	"io/fs"
	"os/exec"

	"github.com/b070nd/stAirCase/src/internal/orchestrator"
)

// Capabilities is what an adapter supports (V11-C01 scope 8, after Cloudroom's capability envelope): a description for the
// owner and for `staircase doctor`, never a grant. A run is governed by its terms, not by what its agent could do; an agent that
// supports less is governed the same and says so. An adapter not in the table declares nothing and is assumed to support nothing.
type Capabilities struct {
	// NativeResume: a continued run can resume the agent's own session. Without it a continuation is a fresh session, grounded by
	// the audit chain, and only when a person asked for that (--fresh-context).
	NativeResume bool
	// Sandbox: the agent confines the commands it runs itself (its own sandbox, apart from stAirCase's for approved commands).
	Sandbox bool
	// MCPGate: a call of an MCP server's tool is decided like a command (ADR 0008). Elsewhere every tool but edits and commands
	// is refused.
	MCPGate bool
	// Hooks: the agent's own hook mechanism calls stAirCase for every tool call (ADR 0003).
	Hooks bool
	// HostMode: the adapter runs the agent for another program that started it (ADR 0008), its protocol passing through.
	HostMode bool
	// Note says in a sentence what the flags leave out.
	Note string
}

// capabilities is every adapter's declaration, in one place. Each flag is what the adapter's code does today (see its tests and
// docs/compatibility.md); change it with the adapter.
var capabilities = map[string]Capabilities{
	"claude-code": {NativeResume: true, Sandbox: true, MCPGate: true, Hooks: true, HostMode: true,
		Note: "its own sandbox confines its commands and the chain counts them as sandboxed; MCP tool calls need --away-tool in an away run"},
	"codex": {NativeResume: true, Sandbox: true, Hooks: true,
		Note: "Codex's workspace-write sandbox confines its commands' writes and network (not their reads), which the chain does not count as sandboxed; every tool but patches and commands is refused"},
	"gemini": {NativeResume: true, Hooks: true,
		Note: "its sandbox is off unless asked for and stAirCase does not ask, so its commands run without one; every tool but edits and commands is refused"},
	"opencode": {
		Note: "governed by a plugin generated for the run, not by hooks; its commands run without a sandbox; a continuation needs --fresh-context"},
}

// adapterNames are the adapters in the order they are listed.
var adapterNames = []string{"claude-code", "codex", "gemini", "opencode"}

// AdapterNames are the adapters that declare capabilities, in the order they are shown.
func AdapterNames() []string { return append([]string(nil), adapterNames...) }

// CapabilitiesOf is what the named adapter declares, and false for one that declares nothing.
func CapabilitiesOf(name string) (Capabilities, bool) {
	c, ok := capabilities[name]
	return c, ok
}

// Supports splits the capabilities into those the adapter supports and those it does not, in words.
func (c Capabilities) Supports() (yes, no []string) {
	for _, f := range []struct {
		name string
		on   bool
	}{{"native resume", c.NativeResume}, {"agent sandbox", c.Sandbox}, {"MCP gate", c.MCPGate}, {"hooks", c.Hooks}, {"host mode", c.HostMode}} {
		if f.on {
			yes = append(yes, f.name)
		} else {
			no = append(no, f.name)
		}
	}
	return yes, no
}

// nativeResume is the declared resume support of an adapter, which its NativeResume answers with.
func nativeResume(name string) bool { return capabilities[name].NativeResume }

// ungoverned is the failure of a session whose hooks (or plugin) never reached the run: the agent was not governed, so nothing it
// did is kept. It is a rejected launch.
func ungoverned(label string) error {
	return orchestrator.Rejected(errors.New(label + " never ran staircase's hooks, so the session was not governed; nothing it did is kept"))
}

// classifyRun classifies an error from running the agent's program. A program that cannot be started (missing, not executable) was
// refused, so the launch is rejected; one that started and ended with an error is a host exit, which is what a plain error is.
func classifyRun(runErr, err error) error {
	var notStarted *exec.Error
	var pathErr *fs.PathError
	if errors.As(runErr, &notStarted) || errors.As(runErr, &pathErr) {
		return orchestrator.Rejected(err)
	}
	return err
}
