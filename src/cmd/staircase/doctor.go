package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/b070nd/stAirCase/src/internal/agent"
	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/sandbox"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run diagnostics on the stAirCase workspace",
	Long: `Checks that the workspace is healthy (directory, keys, database, git), and reports
what stAirCase works without but gains from: ssh-keygen for signed decisions and
reviews (OpenSSH 8.0 or newer), which OS sandbox works on this machine and why another
does not, and which of Claude Code, Codex, Gemini CLI and OpenCode are installed, with
their versions and, where it can be asked without a model call, whether they are logged
in. It also lists runs that were interrupted before they committed and hold approved
changes (staircase recover), and says when a legal hold is in effect. A missing optional
tool is information, not a failed check.`,
	RunE: doctorHandler,
}

var doctorFixVenv bool

func init() {
	doctorCmd.Flags().BoolVar(&doctorFixVenv, "fix-venv", false, "No effect: there is no Python environment")
	_ = doctorCmd.Flags().MarkDeprecated("fix-venv", "the agent runtime is built into staircase; there is no venv to fix")
	rootCmd.AddCommand(doctorCmd)
}

func doctorHandler(_ *cobra.Command, _ []string) error {
	wsDir := viper.GetString("STAIRCASE_DIR")

	fmt.Printf("🩺 stAirCase Doctor - workspace: %s\n\n", wsDir)

	allOK := true

	check := func(label string, ok bool, detail string) {
		if ok {
			fmt.Printf("  ✅ %s\n", label)
		} else {
			fmt.Printf("  ❌ %s - %s\n", label, detail)
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
		fmt.Println("  ℹ️  venv/ is from an older staircase and no longer used - you can delete it")
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
		fmt.Println("  ℹ️  tmp/ directory - created on first run")
	}

	if dbStatErr == nil {
		interruptedRuns(wsDir)
	}
	if _, err := os.Stat(filepath.Join(wsDir, "legal-hold")); err == nil {
		fmt.Println("  ℹ️  legal hold in effect: clean --aggressive deletes no audit rows, cases or run branches")
	}
	optionalTools()

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

// interruptedRuns lists runs that stopped before they committed and hold
// approved changes in their journal: `staircase recover` commits those.
func interruptedRuns(wsDir string) {
	db, err := persistence.InitDB(wsDir)
	if err != nil {
		return
	}
	defer func() { _ = db.Close() }()
	store := persistence.NewStore(db)
	for _, status := range []string{persistence.RunStatusRunning, persistence.RunStatusKilled, persistence.RunStatusFailed} {
		runs, err := store.ListRunsByStatus(status)
		if err != nil {
			return
		}
		for _, r := range runs {
			n := orchestrator.JournaledApprovals(wsDir, r.ID)
			if r.GitCommitHash != "" || n == 0 {
				continue
			}
			hint := fmt.Sprintf("staircase recover %d (or continue it: staircase resume %d)", r.ID, r.ID)
			if status == persistence.RunStatusRunning {
				switch held, known := orchestrator.RunOwner(wsDir, r.ID); {
				case known && held:
					continue // alive: not interrupted
				case !known:
					hint += " --force (only if its process is gone)"
				}
			}
			fmt.Printf("  ⚠️  run #%d was interrupted with %d approved change(s): %s\n", r.ID, n, hint)
		}
	}
}

// optionalTools reports what stAirCase works without but gains from: SSH
// signing, an OS sandbox, and the agents. A missing one is information.
func optionalTools() {
	fmt.Println("\n  Optional tools (stAirCase works without them; each adds something)")
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		fmt.Println("  ℹ️  ssh-keygen: not found - needed to sign decisions and reviews (--sign-approvals, staircase sign)")
	} else if major, minor, ok := opensshVersion(); !ok {
		fmt.Println("  ✅ ssh-keygen found (version not checked) - signed decisions and reviews")
	} else if major < 8 {
		fmt.Printf("  ⚠️  ssh-keygen (OpenSSH %d.%d) - signing needs 8.0 or newer\n", major, minor)
	} else {
		fmt.Printf("  ✅ ssh-keygen (OpenSSH %d.%d) - signed decisions and reviews\n", major, minor)
	}

	engines := sandbox.Status()
	var working, why []string
	for _, e := range engines {
		if e.Err == nil {
			working = append(working, e.Name)
		} else {
			why = append(why, e.Name+": "+e.Err.Error())
		}
	}
	switch {
	case len(working) > 0:
		fmt.Printf("  ✅ sandbox: %s - commands and checks run confined\n", strings.Join(working, ", "))
	case len(engines) > 0:
		fmt.Printf("  ⚠️  sandbox: none works here (%s) - with --sandbox auto commands run unconfined and say so; --sandbox required refuses them\n", strings.Join(why, "; "))
	default:
		fmt.Println("  ⚠️  sandbox: none for this operating system - commands run unconfined unless --sandbox required refuses them")
	}

	agentLine("Claude Code", firstPath("claude"), "--version", func(bin string) string {
		out, err := runQuiet(bin, "auth", "status")
		if err != nil && out == "" {
			return "login not checked"
		}
		if strings.Contains(out, `"loggedIn": true`) || strings.Contains(out, `"loggedIn":true`) {
			return "logged in"
		}
		return "not logged in: run claude auth login"
	})
	agentLine("Codex", agent.FindCodex(), "--version", func(bin string) string {
		if _, err := runQuiet(bin, "login", "status"); err != nil {
			return "not logged in: run codex login"
		}
		return "logged in"
	})
	agentLine("Gemini CLI", firstPath("gemini"), "--version", func(string) string { return "login not checked" })
	agentLine("OpenCode", firstPath("opencode"), "--version", func(string) string { return "login not checked" })
}

func firstPath(name string) string {
	p, _ := exec.LookPath(name)
	return p
}

// agentLine reports one agent: not installed, or its version and login state.
func agentLine(name, bin, versionFlag string, login func(bin string) string) {
	if bin == "" {
		fmt.Printf("  ℹ️  %s: not installed\n", name)
		return
	}
	version, _ := runQuiet(bin, versionFlag)
	if i := strings.IndexByte(version, '\n'); i >= 0 {
		version = version[:i]
	}
	version = strings.TrimSuffix(strings.TrimSpace(version), " ("+name+")") // "2.1.236 (Claude Code)" repeats the name
	if len(version) > 60 {
		version = version[:60]
	}
	state, mark := login(bin), "✅"
	if strings.HasPrefix(state, "not logged in") {
		mark = "⚠️ " // installed, but a session would stop at its login
	}
	fmt.Printf("  %s %s %s (%s)\n", mark, name, version, state)
}

// runQuiet runs bin with args for at most five seconds and returns what it
// printed (stdout, then stderr); it never shows it.
func runQuiet(bin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

var opensshRE = regexp.MustCompile(`OpenSSH_(\d+)\.(\d+)`)

// opensshVersion is the version of the installed OpenSSH, from ssh -V.
func opensshVersion() (major, minor int, ok bool) {
	out, _ := runQuiet("ssh", "-V")
	m := opensshRE.FindStringSubmatch(out)
	if m == nil {
		return 0, 0, false
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	return major, minor, true
}
