package main

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/plan"
	"github.com/spf13/cobra"
)

var reviewBy string

var reviewCmd = &cobra.Command{
	Use:   "review <branch | commit>",
	Short: "Review changes made elsewhere (a cloud agent's pull request) and certify what you approve",
	Long: `Brings the changes of a branch made elsewhere, for example a pull request
opened by a cloud agent, into a separate worktree of your current branch, one
file at a time. Each changed file comes to you (or your rules) to approve or
reject; rejected files are left out. Exactly the approved files are committed
on a new branch, staircase/run-N, with a change certificate that names who made
the changes (--by) and the exact commit reviewed. The files were changed before
they were decided, so the change reaches CAL 2.

Fetch a pull request first, for example:
  git fetch origin pull/42/head:pr-42
  staircase review pr-42 --by "Copilot coding agent"`,
	Args: cobra.ExactArgs(1),
	RunE: reviewHandler,
}

func init() {
	reviewCmd.Flags().StringVar(&reviewBy, "by", "", "Who made the changes, for the Assisted-by trailer and the certificate (default: an external agent)")
	reviewCmd.Flags().StringArrayVar(&sessionAllow, "allow", nil, "A path (glob) the changes may touch; a file elsewhere comes to you as drift")
	checkFlag(reviewCmd)
	reviewCmd.Flags().BoolVarP(&sessionYes, "yes", "y", false, "Start without asking to confirm (needed without a terminal)")
	reviewCmd.Flags().IntVar(&runApprovalPort, "approval-port", 0,
		"Decide from another terminal or a script through the local approval API on this port (0 = in this terminal)")
	reviewCmd.Flags().StringVar(&runApprovalToken, "approval-token", "", "Token for the approval API (default: a new one, printed)")
	rootCmd.AddCommand(reviewCmd)
}

func reviewHandler(_ *cobra.Command, args []string) error {
	out, err := exec.Command("git", "rev-parse", "--verify", "-q", args[0]+"^{commit}").Output()
	if err != nil {
		return fmt.Errorf("%q is not a branch or commit here - fetch it first, for example: git fetch origin pull/42/head:pr-42", args[0])
	}
	commit := strings.TrimSpace(string(out))
	if exec.Command("git", "merge-base", "--is-ancestor", commit, "HEAD").Run() == nil {
		return fmt.Errorf("%s (%.12s) is already part of the current branch: nothing to review", args[0], commit)
	}
	by := reviewBy
	if by == "" {
		by = "an external agent"
	}
	sessionReview = &plan.Review{Ref: args[0], Commit: commit, By: by}
	defer func() { sessionReview = nil }()
	return session("review", by, "review", []string{fmt.Sprintf("%s (commit %.12s), made by %s", args[0], commit, by)})
}
