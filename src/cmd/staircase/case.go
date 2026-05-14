package main

import (
	"fmt"
	"os"
	"time"

	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/spf13/cobra"
)

var caseCmd = &cobra.Command{
	Use:   "case",
	Short: "Manage Cases (units of work)",
}

var caseNewCmd = &cobra.Command{
	Use:   "new <project-id>",
	Short: "Create a new Case for a project",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		projectID, err := parseID("project-id", args[0])
		if err != nil {
			return err
		}
		if p, _ := store.GetProject(projectID); p == nil {
			return fmt.Errorf("project #%d not found", projectID)
		}

		c, err := store.CreateCase(projectID)
		if err != nil {
			return fmt.Errorf("create case: %w", err)
		}
		fmt.Printf("✅ Case #%d created (project #%d) — status: %s\n", c.ID, projectID, c.Status)
		fmt.Printf("   Set a PRD with: staircase case set-prd %d <prd.json>\n", c.ID)
		return nil
	},
}

var caseSetPRDCmd = &cobra.Command{
	Use:   "set-prd <case-id> <prd-file>",
	Short: "Load a PRD JSON file into a Case",
	Args:  cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		caseID, err := parseID("case-id", args[0])
		if err != nil {
			return err
		}

		prdBytes, err := os.ReadFile(args[1])
		if err != nil {
			return fmt.Errorf("read PRD file: %w", err)
		}

		if err := store.SetCasePRD(caseID, string(prdBytes)); err != nil {
			return fmt.Errorf("set PRD: %w", err)
		}
		fmt.Printf("✅ PRD loaded into Case #%d (%d bytes).\n", caseID, len(prdBytes))
		return nil
	},
}

var caseListCmd = &cobra.Command{
	Use:   "list <project-id>",
	Short: "List all Cases for a project",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		projectID, err := parseID("project-id", args[0])
		if err != nil {
			return err
		}

		cases, err := store.ListCasesByProject(projectID)
		if err != nil {
			return err
		}
		if len(cases) == 0 {
			fmt.Printf("No cases for project #%d.\n", projectID)
			return nil
		}

		rows := make([][]string, len(cases))
		for i, c := range cases {
			prd := "—"
			if c.PrdJSON != "" {
				prd = fmt.Sprintf("%d bytes", len(c.PrdJSON))
			}
			rows[i] = []string{
				fmt.Sprint(c.ID),
				c.Status,
				c.LastModified.Format("2006-01-02 15:04"),
				prd,
			}
		}
		table([]string{"ID", "Status", "Modified", "PRD"}, rows)
		return nil
	},
}

var caseStatusCmd = &cobra.Command{
	Use:   "status <case-id>",
	Short: "Show detailed status of a Case",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		caseID, err := parseID("case-id", args[0])
		if err != nil {
			return err
		}

		c, err := store.GetCase(caseID)
		if err != nil || c == nil {
			return fmt.Errorf("case #%d not found", caseID)
		}

		fmt.Printf("Case #%d\n", c.ID)
		fmt.Printf("  Project:  #%d\n", c.ProjectID)
		fmt.Printf("  Status:   %s\n", c.Status)
		fmt.Printf("  Modified: %s\n", c.LastModified.Format("2006-01-02 15:04:05"))

		stories, _ := store.ListUserStoriesByCase(caseID)
		if len(stories) > 0 {
			fmt.Printf("\n  User Stories (%d):\n", len(stories))
			for _, s := range stories {
				fmt.Printf("    #%d [%s] %s\n", s.ID, s.Status, s.Description)
			}
		}
		return nil
	},
}

var caseDeleteCmd = &cobra.Command{
	Use:   "delete <case-id>",
	Short: "Flag a Case for deletion (removed on next 'staircase clean --aggressive')",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		caseID, err := parseID("case-id", args[0])
		if err != nil {
			return err
		}
		if c, _ := store.GetCase(caseID); c == nil {
			return fmt.Errorf("case #%d not found", caseID)
		}
		if err := store.FlagCaseDeleted(caseID); err != nil {
			return fmt.Errorf("flag case: %w", err)
		}
		fmt.Printf("🗑  Case #%d flagged for deletion. Run 'staircase clean --aggressive' to purge.\n", caseID)
		return nil
	},
}

var caseRollbackForce bool

var caseRollbackCmd = &cobra.Command{
	Use:   "rollback <case-id>",
	Short: "Roll back the last run for a case — deletes the staircase branch and restores the working tree",
	Long: `Deletes the staircase/run-N blast-radius branch for the most recent run of a case
and restores the repository to the branch it was on before the run.

Use --force to roll back a currently RUNNING case (kills the run first).`,
	Args: cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		caseID, err := parseID("case-id", args[0])
		if err != nil {
			return err
		}

		caseRec, err := store.GetCase(caseID)
		if err != nil || caseRec == nil {
			return fmt.Errorf("case #%d not found", caseID)
		}

		runs, err := store.ListRunsByCase(caseID)
		if err != nil {
			return fmt.Errorf("list runs: %w", err)
		}
		if len(runs) == 0 {
			return fmt.Errorf("case #%d has no runs to roll back", caseID)
		}

		// Most recent run is first (ListRunsByCase orders by id DESC).
		lastRun := runs[0]

		if lastRun.Status == persistence.RunStatusRunning && !caseRollbackForce {
			return fmt.Errorf(
				"run #%d is still RUNNING — use --force to kill it and roll back",
				lastRun.ID,
			)
		}

		project, err := store.GetProject(caseRec.ProjectID)
		if err != nil || project == nil {
			return fmt.Errorf("project #%d not found", caseRec.ProjectID)
		}

		if project.SourcePath == "" {
			return fmt.Errorf("project #%d has no source_path configured — nothing to roll back", project.ID)
		}

		stairBranch := fmt.Sprintf("staircase/run-%d", lastRun.ID)
		originalBranch := lastRun.GitBranch

		// Check if the branch exists at all.
		if _, err := gitOutput(project.SourcePath, "rev-parse", "--verify", stairBranch); err != nil {
			return fmt.Errorf("blast-radius branch %q does not exist — already rolled back?", stairBranch)
		}

		// If currently on the staircase branch, restore the original.
		currentBranch, _ := gitOutput(project.SourcePath, "rev-parse", "--abbrev-ref", "HEAD")
		if currentBranch == stairBranch {
			if _, err := gitOutput(project.SourcePath, "checkout", originalBranch); err != nil {
				return fmt.Errorf("restore branch %q: %w", originalBranch, err)
			}
			fmt.Printf("   🔀 Restored branch: %s\n", originalBranch)
		}

		// Delete the staircase branch.
		if _, err := gitOutput(project.SourcePath, "branch", "-D", stairBranch); err != nil {
			return fmt.Errorf("delete blast-radius branch %q: %w", stairBranch, err)
		}
		fmt.Printf("   🗑  Deleted branch: %s\n", stairBranch)

		// Mark the run as KILLED in the DB.
		now := time.Now()
		if err := store.UpdateRunStatus(lastRun.ID, persistence.RunStatusKilled, &now, ""); err != nil {
			return fmt.Errorf("update run status: %w", err)
		}
		if err := store.UpdateCaseStatus(caseID, persistence.CaseStatusFailed); err != nil {
			return fmt.Errorf("update case status: %w", err)
		}

		fmt.Printf("✅ Case #%d rolled back — run #%d marked KILLED.\n", caseID, lastRun.ID)
		return nil
	},
}

func init() {
	caseCmd.AddCommand(caseNewCmd, caseSetPRDCmd, caseListCmd, caseStatusCmd, caseDeleteCmd)
	caseRollbackCmd.Flags().BoolVar(&caseRollbackForce, "force", false, "Kill a RUNNING run and roll back")
	caseCmd.AddCommand(caseRollbackCmd)
	rootCmd.AddCommand(caseCmd)
}
