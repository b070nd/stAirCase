package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/plan"
	"github.com/spf13/cobra"
)

var (
	sealBy      string
	sealMessage string
	attachOff   bool
)

var sealCmd = &cobra.Command{
	Use:   "seal",
	Short: "Commit the staged changes an agent made in your checkout, file by file decided and certified",
	Long: `For agents that edit your checkout directly (Cursor, an IDE assistant, any
tool): stage their changes with git add, then run staircase seal instead of git
commit. Each staged file comes to you (or your rules) to approve or reject, as
in staircase review. Your current branch then moves to one new commit that holds
exactly the approved files, with your message, an Assisted-by trailer and a
change certificate (CAL 2: the files were changed before they were decided).

Rejected changes stay in your working files, uncommitted; edits you did not
stage are left alone.`,
	Args: cobra.NoArgs,
	RunE: sealHandler,
}

var attachCmd = &cobra.Command{
	Use:   "attach",
	Short: "Stop plain git commits in this checkout while an agent works in it (use staircase seal)",
	Long: `Installs a pre-commit hook in this repository that refuses git commit with the
way to seal the changes instead. It guards against committing an agent's work
by accident; it is not a wall (git commit --no-verify gets through, and your CI
check stays the second line). staircase attach --off removes it. An existing
pre-commit hook of your own is never overwritten.`,
	Args: cobra.NoArgs,
	RunE: attachHandler,
}

func init() {
	sealCmd.Flags().StringVar(&sealBy, "by", "", "Which agent made the changes, for the Assisted-by trailer and the certificate (default: an agent)")
	sealCmd.Flags().StringVarP(&sealMessage, "message", "m", "", "The commit message (default: Changes by <agent>)")
	sealCmd.Flags().StringArrayVar(&sessionAllow, "allow", nil, "A path (glob) the changes may touch; a file elsewhere comes to you as drift")
	checkFlag(sealCmd)
	signFlags(sealCmd)
	validatorFlag(sealCmd)
	inScopeFlag(sealCmd)
	sealCmd.Flags().BoolVarP(&sessionYes, "yes", "y", false, "Start without asking to confirm (needed without a terminal)")
	sealCmd.Flags().IntVar(&runApprovalPort, "approval-port", 0,
		"Decide from another terminal or a script through the local approval API on this port (0 = in this terminal)")
	sealCmd.Flags().StringVar(&runApprovalToken, "approval-token", "", "Token for the approval API (default: a new one, printed)")
	attachCmd.Flags().BoolVar(&attachOff, "off", false, "Remove the hook")
	rootCmd.AddCommand(sealCmd, attachCmd)
}

func sealHandler(_ *cobra.Command, _ []string) error {
	root, err := gitTopLevel()
	if err != nil {
		return err
	}
	git := func(args ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
		return strings.TrimSpace(string(out)), err
	}
	branch, err := git("symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		return errors.New("seal needs a branch checked out, not a detached HEAD")
	}
	if exec.Command("git", "-C", root, "diff", "--cached", "--quiet").Run() == nil {
		return errors.New("nothing staged: stage the agent's changes with git add, then run staircase seal")
	}
	head, err := git("rev-parse", "HEAD")
	if err != nil {
		return err
	}
	staged, _ := git("diff", "--cached", "--name-only")
	tree, err := git("write-tree")
	if err != nil {
		return fmt.Errorf("git write-tree: %w", err)
	}
	by := sealBy
	if by == "" {
		by = "an agent"
	}
	msg := sealMessage
	if msg == "" {
		msg = "Changes by " + by
	}
	// The staged changes as a commit no branch points to: review brings it in
	// file by file, as it does a pull request.
	commit, err := git("commit-tree", tree, "-p", head, "-m", msg)
	if err != nil {
		return fmt.Errorf("git commit-tree: %w", err)
	}

	sessionReview = &plan.Review{Ref: "staged changes", Commit: commit, By: by, Message: msg}
	finish := sessionFinish
	defer func() { sessionReview, sessionFinish = nil, finish }()
	sessionFinish = func(_, runBranch string, _ int64) error {
		run, err := git("rev-parse", runBranch)
		if err != nil {
			return err
		}
		if run == head {
			fmt.Println("\nNothing was approved: your branch and your staged changes are as they were.")
			_, _ = git("branch", "-D", runBranch)
			return nil
		}
		if parent, _ := git("rev-parse", run+"^"); parent != head {
			return fmt.Errorf("the certified commit %.12s does not follow %.12s; it stays on %s", run, head, runBranch)
		}
		// Move the branch only if it is still where it was: a commit made
		// meanwhile is never overwritten.
		if _, err := git("update-ref", "refs/heads/"+branch, run, head); err != nil {
			return fmt.Errorf("%s moved while sealing; the certified commit stays on %s", branch, runBranch)
		}
		if _, err := git("reset", "-q"); err != nil { // the index follows; your working files stay as they are
			return err
		}
		_, _ = git("branch", "-D", runBranch)
		kept, _ := git("diff-tree", "--no-commit-id", "--name-only", "-r", run)
		fmt.Printf("\n✅ Sealed %.12s on %s: %s (CAL 2, change certificate in refs/notes/staircase)\n", run, branch, msg)
		for _, f := range strings.Fields(staged) {
			if !strings.Contains("\n"+kept+"\n", "\n"+f+"\n") {
				fmt.Printf("   left out, still in your working files: %s\n", f)
			}
		}
		return nil
	}
	return session("review", by, "seal", []string{fmt.Sprintf("staged changes by %s: %s", by, msg)})
}

// attachMarker names the hook staircase attach installs.
const attachMarker = "# staircase attach"

func attachHandler(_ *cobra.Command, _ []string) error {
	root, err := gitTopLevel()
	if err != nil {
		return err
	}
	out, err := exec.Command("git", "-C", root, "rev-parse", "--git-path", "hooks").Output()
	if err != nil {
		return err
	}
	hooks := strings.TrimSpace(string(out))
	if !filepath.IsAbs(hooks) {
		hooks = filepath.Join(root, hooks)
	}
	hook := filepath.Join(hooks, "pre-commit")
	cur, err := os.ReadFile(hook)
	ours := err == nil && strings.Contains(string(cur), attachMarker)
	if attachOff {
		if ours {
			if err := os.Remove(hook); err != nil {
				return err
			}
		}
		fmt.Println("✅ Detached: git commit works as usual in this checkout")
		return nil
	}
	if err == nil && !ours {
		return fmt.Errorf("%s already has a pre-commit hook of its own; staircase attach does not replace it", root)
	}
	script := "#!/bin/sh\n" + attachMarker + ": an agent works in this checkout (staircase attach --off to undo)\n" +
		"echo 'staircase: this checkout is attached. Commit the agent'\\''s changes with:' >&2\n" +
		"echo '  staircase seal -m \"<message>\" --by \"<agent>\"' >&2\n" +
		"echo '(git commit --no-verify commits without a certificate)' >&2\n" +
		"exit 1\n"
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		return err
	}
	fmt.Println("✅ Attached: git commit is refused here; seal the agent's changes with staircase seal")
	return nil
}
