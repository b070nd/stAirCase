package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/b070nd/staircase-core/src/internal/crypto"
	"github.com/b070nd/staircase-core/src/internal/persistence"
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
	doctorCmd.Flags().BoolVar(&doctorFixVenv, "fix-venv", false, "No effect: there is no Python environment")
	_ = doctorCmd.Flags().MarkDeprecated("fix-venv", "the agent runtime is built into staircase; there is no venv to fix")
	rootCmd.AddCommand(doctorCmd)
}

func doctorHandler(_ *cobra.Command, _ []string) error {
	wsDir := viper.GetString("STAIRCASE_DIR")

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

	// ── 4. Leftovers of the Python runtime ────────────────────────────────────
	if _, err := os.Stat(filepath.Join(wsDir, "venv")); err == nil {
		fmt.Println("  ℹ️  venv/ is from an older staircase and no longer used — you can delete it")
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
	// tmp/ is created on first run, so its absence is informational only.
	if _, err := os.Stat(filepath.Join(wsDir, "tmp")); err == nil {
		fmt.Println("  ✅ tmp/ directory")
	} else {
		fmt.Println("  ℹ️  tmp/ directory — created on first run")
	}

	fmt.Println()
	if !allOK {
		fmt.Println("❌ Some checks failed. See above for remediation steps.")
		return fmt.Errorf("doctor: some checks failed")
	}
	fmt.Println("✅ All checks passed. Ready to orchestrate.")
	return nil
}

func trimNL(b []byte) string {
	s := string(b)
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
