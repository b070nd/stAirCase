package runtime

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

//go:embed requirements.txt
var embeddedRequirements []byte

// embeddedRecording is the source of runner/staircase_runner/recording.py, copied
// here so it can be embedded in the binary and injected into the workspace venv.
// Keep in sync with runner/staircase_runner/recording.py.
//
//go:embed recording_embed.py
var embeddedRecording []byte

// BootstrapVenv ensures a strictly isolated Python environment exists in $STAIRCASE_DIR.
// On each call it compares a SHA-256 hash of the embedded requirements.txt against a
// stored hash in the venv directory. If the hash differs (requirements were updated),
// pip install is re-run without recreating the venv — no manual rm -rf needed.
// venvPythonBin returns the platform-correct Python executable path inside a venv.
func venvPythonBin(venvPath string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venvPath, "Scripts", "python.exe")
	}
	return filepath.Join(venvPath, "bin", "python")
}

func BootstrapVenv(workspaceDir string, offlineWheelsDir string) error {
	venvPath := filepath.Join(workspaceDir, "venv")
	pythonExec := venvPythonBin(venvPath)
	hashFile := filepath.Join(venvPath, ".requirements_hash")

	reqSum := sha256.Sum256(embeddedRequirements)
	reqHash := fmt.Sprintf("%x", reqSum)

	// Fast path: venv exists and requirements are unchanged.
	if _, err := os.Stat(pythonExec); err == nil {
		if stored, err := os.ReadFile(hashFile); err == nil && strings.TrimSpace(string(stored)) == reqHash {
			return nil
		}
		// Requirements changed: re-run pip install in-place (no venv recreation needed).
		fmt.Fprintln(os.Stdout, "📦 Requirements changed — updating packages...")
		if err := runPipInstall(pythonExec, workspaceDir, offlineWheelsDir); err != nil {
			// pip failed — mark the venv as broken so the next init recreates it.
			_ = os.WriteFile(hashFile+".broken", []byte(err.Error()), 0o644)
			_ = os.Remove(hashFile)
			return err
		}
		if err := writeRunnerInject(venvPath); err != nil {
			return fmt.Errorf("write runner_inject: %w", err)
		}
		_ = os.WriteFile(hashFile, []byte(reqHash), 0o644)
		fmt.Fprintln(os.Stdout, "✅ Packages updated.")
		return nil
	}

	// If a .broken sentinel exists from a previous failed install, remove the
	// entire venv so it is recreated from scratch below.
	brokenSentinel := hashFile + ".broken"
	if _, err := os.Stat(brokenSentinel); err == nil {
		fmt.Fprintln(os.Stdout, "⚠️  Broken venv detected — recreating from scratch...")
		_ = os.RemoveAll(venvPath)
	}

	fmt.Fprintln(os.Stdout, "🚀 Bootstrapping isolated Python environment (Zero-Trace)...")

	// 1. Create the virtual environment
	cmd := exec.Command("python3", "-m", "venv", venvPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create venv: %v\nOutput: %s\nHint: Ensure python3 is installed", err, string(out))
	}

	// 2. Rosetta / Architecture verification (macOS Edge Case)
	if runtime.GOOS == "darwin" {
		archCmd := exec.Command(pythonExec, "-c", "import platform; print(platform.machine())")
		out, _ := archCmd.Output()
		if runtime.GOARCH == "arm64" && strings.Contains(string(out), "x86_64") {
			return fmt.Errorf("architecture mismatch: Go binary is native arm64, but python3 is running via Rosetta (x86_64). This will cause wheel compilation failures")
		}
	}

	// 3. Install dependencies.
	if err := runPipInstall(pythonExec, workspaceDir, offlineWheelsDir); err != nil {
		// Mark the venv as broken. The next `staircase init` will detect this
		// sentinel and remove the entire venv before attempting recreation.
		_ = os.WriteFile(brokenSentinel, []byte(err.Error()), 0o644)
		return err
	}

	// 4. Inject the staircase_runner package so --record-llm / --replay-llm work.
	if err := writeRunnerInject(venvPath); err != nil {
		return fmt.Errorf("write runner_inject: %w", err)
	}

	_ = os.Remove(brokenSentinel) // clear any previous broken sentinel
	_ = os.WriteFile(hashFile, []byte(reqHash), 0o644)
	fmt.Fprintln(os.Stdout, "✅ Environment isolated successfully.")
	return nil
}

// RunnerInjectDir returns the directory that must be on sys.path for
// `import staircase_runner` to resolve inside the workspace venv.
// Python sees: <RunnerInjectDir>/staircase_runner/__init__.py
//              <RunnerInjectDir>/staircase_runner/recording.py
func RunnerInjectDir(venvPath string) string {
	return filepath.Join(venvPath, "runner_inject")
}

// writeRunnerInject writes the embedded staircase_runner package under
// <venvPath>/runner_inject/staircase_runner/.  The directory is recreated on
// every BootstrapVenv call so the embedded content stays in sync with the binary.
func writeRunnerInject(venvPath string) error {
	pkgDir := filepath.Join(RunnerInjectDir(venvPath), "staircase_runner")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "__init__.py"), []byte(""), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(pkgDir, "recording.py"), embeddedRecording, 0o644)
}

// runPipInstall writes the embedded requirements.txt to tmp/ and runs pip install.
func runPipInstall(pythonExec, workspaceDir, offlineWheelsDir string) error {
	reqPath := filepath.Join(workspaceDir, "tmp", "requirements.txt")
	_ = os.MkdirAll(filepath.Dir(reqPath), 0o755)
	if err := os.WriteFile(reqPath, embeddedRequirements, 0o644); err != nil {
		return err
	}

	// --require-hashes: requirements.txt is fully hash-pinned; pip fails closed
	// on any package whose hash is missing or mismatched (supply-chain drift).
	pipCmdArgs := []string{"-m", "pip", "install", "--require-hashes", "-r", reqPath}
	if offlineWheelsDir != "" {
		pipCmdArgs = append(pipCmdArgs, "--no-index", "--find-links", offlineWheelsDir)
	}

	pipCmd := exec.Command(pythonExec, pipCmdArgs...)
	var stderr bytes.Buffer
	pipCmd.Stderr = &stderr

	if err := pipCmd.Run(); err != nil {
		errOutput := stderr.String()
		if strings.Contains(errOutput, "CERTIFICATE_VERIFY_FAILED") || strings.Contains(errOutput, "ReadTimeoutError") {
			return fmt.Errorf("network/proxy failure during pip install. \nDetails: %s\n\nSolution: Use offline wheels via 'staircase init --offline-wheels <dir>'", errOutput)
		}
		return fmt.Errorf("failed to install dependencies: %v\nStderr: %s", err, errOutput)
	}
	return nil
}
