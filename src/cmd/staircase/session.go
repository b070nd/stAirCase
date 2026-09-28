package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

var (
	sessionAllow []string
	sessionYes   bool
)

var claudeCmd = &cobra.Command{
	Use:   "claude <task>",
	Short: "Run Claude Code on a task in this repository, with every change decided by you",
	Long: `Runs Claude Code on the task in a separate worktree of the git repository you
are in. Every file change and command it wants to make comes to you first. At
the end, exactly the approved changes are committed on a new branch,
staircase/run-N; your checkout is not touched.

No setup is needed: the workspace, a project for this repository and a case for
the task are created when missing. Claude Code must be installed and logged in.

--allow limits the paths the task may change; changes elsewhere come to you as
drift (see docs/drift.md).`,
	Args: cobra.ArbitraryArgs,
	RunE: claudeSession,
}

var codexCmd = &cobra.Command{
	Use:   "codex <task>",
	Short: "Run Codex on a task in this repository, with every change decided by you",
	Long: `Runs OpenAI's Codex CLI on the task in a separate worktree of the git
repository you are in. Every file edit it wants to make comes to you first.
Its shell commands run in Codex's sandbox (no network, writes only in the
worktree), and the files a command changes come to you afterwards: kept if
you approve, reverted if not. At the end, exactly the approved changes are
committed on a new branch, staircase/run-N; your checkout is not touched.

No setup is needed. Codex must be installed (the ChatGPT app for macOS
includes it) and logged in.`,
	Args: cobra.ArbitraryArgs,
	RunE: codexSession,
}

func init() {
	claudeCmd.Flags().StringArrayVar(&sessionAllow, "allow", nil, "A path (glob) the task may change; repeat for more")
	claudeCmd.Flags().BoolVar(&runAllowShellExec, "allow-shell-exec", false,
		"Let Claude Code propose shell commands (each still needs your approval)")
	claudeCmd.Flags().IntVar(&runApprovalPort, "approval-port", 0,
		"Decide from another terminal or a script through the local approval API on this port (0 = in this terminal)")
	claudeCmd.Flags().StringVar(&runApprovalToken, "approval-token", "", "Token for the approval API (default: a new one, printed)")
	claudeCmd.Flags().StringVar(&runModel, "model", "", "Model for Claude Code (default: its own)")
	claudeCmd.Flags().BoolVarP(&sessionYes, "yes", "y", false, "Start without asking to confirm the task (needed without a terminal)")
	rootCmd.AddCommand(claudeCmd)
	codexCmd.Flags().BoolVarP(&sessionYes, "yes", "y", false, "Start without asking to confirm the task (needed without a terminal)")
	codexCmd.Flags().StringVar(&runModel, "model", "", "Model for Codex (default: its own)")
	codexCmd.Flags().StringArrayVar(&sessionAllow, "allow", nil, "A path (glob) the task may change; repeat for more")
	codexCmd.Flags().IntVar(&runApprovalPort, "approval-port", 0,
		"Decide from another terminal or a script through the local approval API on this port (0 = in this terminal)")
	codexCmd.Flags().StringVar(&runApprovalToken, "approval-token", "", "Token for the approval API (default: a new one, printed)")
	rootCmd.AddCommand(codexCmd)
}

// claudeSession is `staircase claude <task>`.
func claudeSession(_ *cobra.Command, args []string) error {
	return session("claude-code", "Claude Code", "claude", args)
}

// codexSession is `staircase codex <task>`.
func codexSession(_ *cobra.Command, args []string) error {
	return session("codex", "Codex", "codex", args)
}

// session is a governed run of harness in the current repository, with
// nothing to set up first.
func session(harness, name, command string, args []string) error {
	task := strings.TrimSpace(strings.Join(args, " "))
	if task == "" {
		return fmt.Errorf(`what should %s do? For example: staircase %s "add a /health endpoint"`, name, command)
	}
	root, err := gitTopLevel()
	if err != nil {
		return err
	}

	wsDir := viper.GetString("STAIRCASE_DIR")
	if _, err := os.Stat(filepath.Join(wsDir, "workspace.db")); errors.Is(err, os.ErrNotExist) {
		fmt.Printf("🧰 Creating the workspace in %s\n", wsDir)
	}
	db, err := persistence.InitDB(wsDir)
	if err != nil {
		return fmt.Errorf("workspace: %w", err)
	}
	store := persistence.NewStore(db)
	var storyID int64
	caseID, err := func() (int64, error) {
		defer func() { _ = db.Close() }() // the run opens the workspace itself
		if err := crypto.GenerateKey(wsDir); err != nil {
			return 0, err
		}
		if err := crypto.GenerateSigningKey(wsDir); err != nil {
			return 0, err
		}
		project, err := sessionProject(store, root)
		if err != nil {
			return 0, err
		}
		_, budget, _ := store.GetProjectConfig(project.ID)
		base, _ := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
		if runAgreedBy, err = agree(sessionSetup{harness: harness, name: name, task: task, root: root,
			base: strings.TrimSpace(string(base)), allow: sessionAllow, model: runModel,
			shell: runAllowShellExec, budget: budget, projectID: project.ID}); err != nil {
			return 0, err
		}
		c, err := store.CreateCase(project.ID)
		if err != nil {
			return 0, err
		}
		if err := store.SetCasePRD(c.ID, task); err != nil {
			return 0, err
		}
		story, err := store.CreateUserStory(c.ID, task)
		if err != nil {
			return 0, err
		}
		storyID = story.ID
		if len(sessionAllow) > 0 {
			scope, _ := json.Marshal(map[string]any{"allow": sessionAllow})
			if err := store.SetUserStoryScope(story.ID, string(scope)); err != nil {
				return 0, err
			}
		}
		fmt.Printf("🧭 Project %q (#%d), case #%d, story #%d\n", project.Name, project.ID, c.ID, story.ID)
		if _, err := compileCase(store, wsDir, c.ID, harness, true); err != nil {
			return 0, err
		}
		return c.ID, nil
	}()
	if err != nil {
		return err
	}
	if err := runCase(caseID); err != nil {
		return err
	}
	branch := "staircase/run-<id>"
	if db, err := persistence.InitDB(wsDir); err == nil {
		if runs, err := persistence.NewStore(db).ListRunsByCase(caseID); err == nil && len(runs) > 0 {
			branch = fmt.Sprintf("staircase/run-%d", runs[len(runs)-1].ID)
		}
		_ = db.Close()
	}
	fmt.Printf("\nThe approved changes are on %s. Review them, then accept the story when the task is done:\n"+
		"  git -C %s diff HEAD...%s\n  staircase story accept %d\n", branch, root, branch, storyID)
	return nil
}

// gitTopLevel is the top of the git repository the current directory is in.
// It must have a commit: the agents start from it.
func gitTopLevel() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", errors.New("not inside a git repository: run staircase from the repository the task is about")
	}
	root := strings.TrimSpace(string(out))
	if err := exec.Command("git", "-C", root, "rev-parse", "--verify", "-q", "HEAD").Run(); err != nil {
		return "", fmt.Errorf("%s has no commit yet: commit something first; the agent starts from your last commit", root)
	}
	return root, nil
}

// sessionProject is the workspace project for the repository at root: the
// one whose source is root, else a new one under vendor "local", named after
// the folder.
func sessionProject(store *persistence.Store, root string) (*domain.Project, error) {
	projects, err := store.ListAllProjects()
	if err != nil {
		return nil, err
	}
	for _, p := range projects {
		if samePath(p.SourcePath, root) {
			return &p, nil
		}
	}
	v, err := store.GetVendorByName("local")
	if err != nil {
		return nil, err
	}
	if v == nil {
		if v, err = store.CreateVendor("local"); err != nil {
			return nil, err
		}
	}
	name := filepath.Base(root)
	for i := 2; ; i++ {
		if p, err := store.GetProjectByVendorAndName(v.ID, name); err != nil {
			return nil, err
		} else if p == nil {
			break
		}
		name = fmt.Sprintf("%s-%d", filepath.Base(root), i)
	}
	fmt.Printf("📁 New project %q for %s\n", name, root)
	return store.CreateProject(v.ID, name, root)
}

// samePath compares two directories after resolving symlinks (/tmp and
// /private/tmp on macOS are the same folder).
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// sessionSetup is what a session is about to do, for the person to agree to.
type sessionSetup struct {
	harness, name, task, root, base, model string
	allow                                  []string
	shell                                  bool
	budget                                 float64
	projectID                              int64
}

// agreement describes the session before its agent starts: the task, where
// it may change files, how edits and commands are governed for this agent,
// the model and the budget.
func agreement(s sessionSetup) string {
	var b strings.Builder
	fmt.Fprintf(&b, "stAirCase is about to run %s on:\n  %s\n", s.name, s.task)
	fmt.Fprintf(&b, "in %s, from commit %.12s (your checkout is not touched)\n\n", s.root, s.base)
	scope := "the whole repository"
	if len(s.allow) > 0 {
		scope = strings.Join(s.allow, ", ") + " (a change anywhere else comes to you as drift)"
	}
	fmt.Fprintf(&b, "  may change:  %s\n", scope)
	fmt.Fprintf(&b, "  edits:       each one comes to you before it happens\n")
	switch {
	case s.harness == "codex":
		fmt.Fprintf(&b, "  commands:    run in Codex's sandbox (no network); files they change come to you afterwards\n")
	case s.shell:
		fmt.Fprintf(&b, "  commands:    each one comes to you before it runs\n")
	default:
		fmt.Fprintf(&b, "  commands:    not allowed (--allow-shell-exec lets it ask)\n")
	}
	model := s.model
	if model == "" {
		model = s.name + "'s default"
	}
	fmt.Fprintf(&b, "  model:       %s\n", model)
	if s.budget > 0 {
		fmt.Fprintf(&b, "  budget:      at most $%.2f\n", s.budget)
	} else {
		fmt.Fprintf(&b, "  budget:      no budget cap (staircase project config set %d --budget-cap <dollars>)\n", s.projectID)
	}
	return b.String()
}

// agree shows the agreement and asks for it; with --yes it only shows it. It
// returns who agreed, for the audit chain.
func agree(s sessionSetup) (string, error) {
	fmt.Print(agreement(s))
	if sessionYes {
		return "--yes", nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("no terminal to confirm the task: add --yes to start without asking")
	}
	fmt.Print("\nStart? [y/N] ")
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
		return "", errors.New("not started")
	}
	return "operator", nil
}
