// export_test.go — compiled only during `go test`.
// Exposes unexported gate implementations and internal state to the
// black-box test package (package gate_test).
package gate

import "testing"

// ShellSandboxGate helpers — expose metadata and Run for black-box testing.
var shellSandboxGateSingleton Gate = &shellSandboxGate{}

func ExportedShellSandboxRun(ctx Context) Result { return shellSandboxGateSingleton.Run(ctx) }
func ExportedShellSandboxName() string           { return shellSandboxGateSingleton.Name() }
func ExportedShellSandboxCategory() string       { return shellSandboxGateSingleton.Category() }
func ExportedShellSandboxSeverity() Severity     { return shellSandboxGateSingleton.Severity() }

// Individual gate singletons — one per registered gate implementation.
var (
	CaseProjectExistsGate            Gate = &caseProjectExistsGate{}
	CaseHasStoriesGate               Gate = &caseHasStoriesGate{}
	CaseHasPRDGate                   Gate = &caseHasPRDGate{}
	TopologyExistsGate               Gate = &topologyExistsGate{}
	TopologyHasAgentsGate            Gate = &topologyHasAgentsGate{}
	TopologySupervisorRegisteredGate Gate = &topologySupervisorRegisteredGate{}
	TopologyEdgesValidGate           Gate = &topologyEdgesValidGate{}
	TopologyNoOrphanAgentsGate       Gate = &topologyNoOrphanAgentsGate{}
	SecretAnthropicKeyGate           Gate = &secretAnthropicKeyGate{}
	SecretKeyFileGate                Gate = &secretKeyFileGate{}
	SecretNoDuplicatesGate           Gate = &secretNoDuplicatesGate{}
	RuntimeScriptCompiledGate        Gate = &runtimeScriptCompiledGate{}
	RuntimeVenvReadyGate             Gate = &runtimeVenvReadyGate{}
	RuntimeVenvBrokenGate            Gate = &runtimeVenvBrokenGate{}
	RuntimeSourcePathGate            Gate = &runtimeSourcePathGate{}
	RuntimeNoConcurrentRunGate       Gate = &runtimeNoConcurrentRunGate{}
	RuntimeGitAvailableGate          Gate = &runtimeGitAvailableGate{}
	DepNoCycleGate                   Gate = &depNoCycleGate{}
	DepDepsCompletedGate             Gate = &depDepsCompletedGate{}
	TopologyRuntimeValidGate         Gate = &topologyRuntimeValidGate{}
)

// ReplaceRegistry swaps the global gate registry for the duration of a test
// and restores it via t.Cleanup — safe for parallel tests that each call this.
func ReplaceRegistry(t testing.TB, gates []Gate) {
	t.Helper()
	saved := registry
	registry = gates
	t.Cleanup(func() { registry = saved })
}
