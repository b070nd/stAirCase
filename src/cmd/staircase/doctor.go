package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/b070nd/staircase-core/src/internal/crypto"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/b070nd/staircase-core/src/internal/runtime"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run diagnostics on the stAirCase workspace",
	RunE:  doctorHandler,
}

var doctorFixVenv bool

func init() {
	doctorCmd.Flags().BoolVar(&doctorFixVenv, "fix-venv", false, "Delete and rebuild the Python virtual environment")
	rootCmd.AddCommand(doctorCmd)
}

func doctorHandler(_ *cobra.Command, _ []string) error {
	wsDir := viper.GetString("STAIRCASE_DIR")

	if doctorFixVenv {
		venvPath := filepath.Join(wsDir, "venv")
		fmt.Printf("🔧 --fix-venv: removing %s...\n", venvPath)
		if err := os.RemoveAll(venvPath); err != nil {
			return fmt.Errorf("remove venv: %w", err)
		}
		if err := runtime.BootstrapVenv(wsDir, ""); err != nil {
			return fmt.Errorf("rebuild venv: %w", err)
		}
		fmt.Println("✅ Venv rebuilt successfully.")
		return nil
	}

	fmt.Printf("🩺 stAirCase Doctor — workspace: %s\n\n", wsDir)

	allOK := true

	check := func(label string, ok bool, detail string) {
		if ok {
			fmt.Printf("  ✅ %s\n", label)
		} else {
			fmt.Printf("  ❌ %s — %s\n", label, detail)
			allOK = false
		}
	}

	// ── 1. Workspace directory ────────────────────────────────────────────────
	_, err := os.Stat(wsDir)
	check("Workspace directory exists", err == nil, "run 'staircase init'")

	// ── 2. Encryption key ─────────────────────────────────────────────────────
	_, keyErr := crypto.LoadKey(wsDir)
	check("Workspace encryption key (.key)", keyErr == nil, "run 'staircase init'")

	// ── 3. SQLite database ────────────────────────────────────────────────────
	dbPath := filepath.Join(wsDir, "workspace.db")
	_, dbStatErr := os.Stat(dbPath)
	check("SQLite database (workspace.db)", dbStatErr == nil, "run 'staircase init'")

	if dbStatErr == nil {
		db, err := persistence.InitDB(wsDir)
		if err == nil {
			_ = db.Close()
			check("Database schema verified", true, "")
		} else {
			check("Database schema verified", false, err.Error())
		}
	}

	// ── 4. Python venv ────────────────────────────────────────────────────────
	pythonBin := filepath.Join(wsDir, "venv", "bin", "python")
	_, venvErr := os.Stat(pythonBin)
	check("Python venv exists", venvErr == nil, "run 'staircase init'")

	if venvErr == nil {
		// Check Python version.
		out, err := exec.Command(pythonBin, "--version").Output()
		if err == nil {
			check(fmt.Sprintf("Python (%s)", trimNL(out)), true, "")
		} else {
			check("Python interpreter responsive", false, err.Error())
		}

		// Check critical packages.
		for _, pkg := range []string{"langgraph", "pydantic", "langchain_anthropic", "langchain_openai", "langchain_google_genai", "langchain_xai"} {
			err := exec.Command(pythonBin, "-c", fmt.Sprintf("import %s", pkg)).Run()
			check(fmt.Sprintf("Python package %q", pkg), err == nil,
				fmt.Sprintf("run 'staircase init' or pip install %s", pkg))
		}
	}

	// ── 5. Git ────────────────────────────────────────────────────────────────
	_, gitErr := exec.LookPath("git")
	gitVersion := ""
	if gitErr == nil {
		out, _ := exec.Command("git", "--version").Output()
		gitVersion = trimNL(out)
	}
	label := "git in PATH"
	if gitVersion != "" {
		label = fmt.Sprintf("git in PATH (%s)", gitVersion)
	}
	check(label, gitErr == nil, "install git from https://git-scm.com")

	// ── 6. tmp directory ──────────────────────────────────────────────────────
	tmpDir := filepath.Join(wsDir, "tmp")
	_, tmpErr := os.Stat(tmpDir)
	check("tmp/ directory", tmpErr == nil, "will be created on first run")

	fmt.Println()
	if allOK {
		fmt.Println("✅ All checks passed. Ready to orchestrate.")
	} else {
		fmt.Println("❌ Some checks failed. See above for remediation steps.")
	}
	return nil
}

func trimNL(b []byte) string {
	s := string(b)
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
