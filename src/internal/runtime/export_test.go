//go:build !windows

package runtime

import "os/exec"

// ExportedVenvPythonBin exposes venvPythonBin for whitebox testing.
func ExportedVenvPythonBin(venvPath string) string {
	return venvPythonBin(venvPath)
}

// NewPythonProcessForTest constructs a PythonProcess directly so Kill() can be
// tested without going through LaunchPython (which requires a real Python venv).
// done must be a buffered channel (cap >= 1) that the caller closes or sends on
// when the underlying process exits.
func NewPythonProcessForTest(cmd *exec.Cmd, done chan error) *PythonProcess {
	// A no-op cancel func — the real cancel comes from context.WithCancel inside
	// LaunchPython, but for unit tests we don't need context cancellation.
	noop := func() {}
	return &PythonProcess{
		cmd:    cmd,
		cancel: noop,
		Done:   done,
	}
}

// ExportedPipInstallArgs exposes pipInstallArgs for whitebox testing.
func ExportedPipInstallArgs(reqPath, offlineWheelsDir string, requirements []byte) []string {
	return pipInstallArgs(reqPath, offlineWheelsDir, requirements)
}
