package main

import (
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
)

var sessionAllow []string

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

func init() {
	claudeCmd.Flags().StringArrayVar(&sessionAllow, "allow", nil, "A path (glob) the task may change; repeat for more")
	claudeCmd.Flags().BoolVar(&runAllowShellExec, "allow-shell-exec", false,
		"Let Claude Code propose shell commands (each still needs your approval)")
	claudeCmd.Flags().IntVar(&runApprovalPort, "approval-port", 0,
		"Decide from another terminal or a script through the local approval API on this port (0 = in this terminal)")
	rootCmd.AddCommand(claudeCmd)
}

// claudeSession is `staircase claude <task>`: a governed Claude Code run in
// the current repository, with nothing to set up first.
func claudeSession(_ *cobra.Command, args []string) error {
	task := strings.TrimSpace(strings.Join(args, " "))
	if task == "" {
		return errors.New(`what should Claude Code do? For example: staircase claude "add a /health endpoint"`)
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
		if _, err := compileCase(store, wsDir, c.ID, "claude-code", true); err != nil {
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
		return "", errors.New("not inside a git repository: run staircase claude from the repository the task is about")
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
