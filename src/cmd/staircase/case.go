package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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

var caseRollbackCmd = &cobra.Command{
	Use:   "rollback <case-id>",
	Short: "Discard the latest run of a case: remove its worktree and delete its branch",
	Long: `Discards the most recent run of a case: removes its worktree (kept after a
failed run) and deletes its staircase/run-N branch. Your checkout is not
touched — runs never modify it. The run record and its audit chain are kept;
a rolled_back event records who discarded it, and the case returns to PENDING
so it can be run again. A RUNNING run must be stopped first.`,
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
		lastRun := runs[0] // ListRunsByCase orders by id DESC
		if lastRun.Status == persistence.RunStatusRunning {
			return fmt.Errorf("run #%d is still RUNNING — stop it first (Ctrl-C in its terminal); "+
				"stale records are reaped by 'staircase run --reconcile'", lastRun.ID)
		}
		project, err := store.GetProject(caseRec.ProjectID)
		if err != nil || project == nil {
			return fmt.Errorf("project #%d not found", caseRec.ProjectID)
		}
		if project.SourcePath == "" {
			return fmt.Errorf("project #%d has no source_path configured — nothing to roll back", project.ID)
		}

		wt := filepath.Join(viper.GetString("STAIRCASE_DIR"), "worktrees", fmt.Sprintf("run-%d", lastRun.ID))
		if _, err := os.Stat(wt); err == nil {
			// The worktree holds the run branch; it must go before the branch can.
			if out, err := gitOutput(project.SourcePath, "worktree", "remove", "--force", wt); err != nil {
				return fmt.Errorf("remove run worktree %s: %w: %s", wt, err, out)
			}
			fmt.Printf("   🗑  Removed worktree: %s\n", wt)
		}
		branch := fmt.Sprintf("staircase/run-%d", lastRun.ID)
		if _, err := gitOutput(project.SourcePath, "rev-parse", "--verify", branch); err == nil {
			if out, err := gitOutput(project.SourcePath, "branch", "-D", branch); err != nil {
				return fmt.Errorf("delete run branch %q: %w: %s", branch, err, out)
			}
			fmt.Printf("   🗑  Deleted branch: %s\n", branch)
		}

		actor := "unknown"
		if u, err := user.Current(); err == nil {
			actor = u.Username
		}
		payload, _ := json.Marshal(map[string]any{"type": "rolled_back", "actor": actor, "branch": branch})
		if _, err := store.AppendEventLogChained(lastRun.ID, "rolled_back", string(payload), ""); err != nil {
			return fmt.Errorf("audit rollback: %w", err)
		}
		if err := store.UpdateCaseStatus(caseID, persistence.CaseStatusPending); err != nil {
			return fmt.Errorf("update case status: %w", err)
		}
		fmt.Printf("✅ Case #%d rolled back: run #%d (%s) discarded by %s; case is PENDING again.\n",
			caseID, lastRun.ID, lastRun.Status, actor)
		return nil
	},
}

func init() {
	caseCmd.AddCommand(caseNewCmd, caseSetPRDCmd, caseListCmd, caseStatusCmd, caseDeleteCmd)
	caseCmd.AddCommand(caseRollbackCmd)
	rootCmd.AddCommand(caseCmd)
}
