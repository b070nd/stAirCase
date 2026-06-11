package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Manage projects within a vendor namespace",
}

var projectSourcePath string

var projectAddCmd = &cobra.Command{
	Use:   "add <vendor-name> <project-name>",
	Short: "Register a new project under a vendor",
	Args:  cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		vendorName, projectName := args[0], args[1]

		if projectSourcePath != "" {
			if !filepath.IsAbs(projectSourcePath) {
				return fmt.Errorf("--source must be an absolute path, got %q", projectSourcePath)
			}
			if _, err := os.Stat(projectSourcePath); err != nil {
				return fmt.Errorf("source path %q: %w", projectSourcePath, err)
			}
		}

		vendor, err := store.GetVendorByName(vendorName)
		if err != nil {
			return fmt.Errorf("lookup vendor: %w", err)
		}
		if vendor == nil {
			return fmt.Errorf("vendor %q not found — register it first with 'staircase vendor add %s'", vendorName, vendorName)
		}

		p, err := store.CreateProject(vendor.ID, projectName, projectSourcePath)
		if err != nil {
			return fmt.Errorf("create project: %w", err)
		}
		fmt.Printf("✅ Project #%d %q created under vendor %q.\n", p.ID, p.Name, vendorName)
		if p.SourcePath != "" {
			fmt.Printf("   Source path: %s\n", p.SourcePath)
		}
		return nil
	},
}

var projectListCmd = &cobra.Command{
	Use:   "list <vendor-name>",
	Short: "List projects for a vendor",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		vendor, err := store.GetVendorByName(args[0])
		if err != nil {
			return err
		}
		if vendor == nil {
			return fmt.Errorf("vendor %q not found", args[0])
		}

		projects, err := store.ListProjectsByVendor(vendor.ID)
		if err != nil {
			return err
		}
		if len(projects) == 0 {
			fmt.Printf("No projects under %q.\n", args[0])
			return nil
		}

		rows := make([][]string, len(projects))
		for i, p := range projects {
			src := p.SourcePath
			if src == "" {
				src = "(none)"
			}
			rows[i] = []string{fmt.Sprint(p.ID), p.Name, src}
		}
		table([]string{"ID", "Name", "Source Path"}, rows)
		return nil
	},
}

var projectDepCmd = &cobra.Command{
	Use:   "dep",
	Short: "Manage project dependency edges",
}

var projectDepAddCmd = &cobra.Command{
	Use:   "add <source-project-id> <target-project-id>",
	Short: "Declare that source-project depends on target-project",
	Args:  cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		srcID, err := parseID("source-project-id", args[0])
		if err != nil {
			return err
		}
		tgtID, err := parseID("target-project-id", args[1])
		if err != nil {
			return err
		}

		dep, err := store.CreateProjectDependency(srcID, tgtID)
		if err != nil {
			return fmt.Errorf("create dependency: %w", err)
		}
		fmt.Printf("✅ Dependency #%d: project #%d → project #%d\n", dep.ID, srcID, tgtID)
		return nil
	},
}

var projectSetWebhookCmd = &cobra.Command{
	Use:   "set-webhook <project-id> <url>",
	Short: "Set (or clear) the HITL webhook URL for a project",
	Long: `Set (or clear) the HITL webhook URL for a project.

To authenticate the webhook channel (strongly recommended — otherwise a network
attacker can forge approvals), store a shared HMAC secret under the reserved key:

    staircase secret set __webhook_hmac_secret__ <secret> --project <project-id>

When that secret is present, stAirCase HMAC-signs each outbound yield and rejects
any response that is not validly signed with the same secret. The approver must
verify the X-Staircase-Signature request header and sign its response the same way.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(_ *cobra.Command, args []string) error {
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		id, err := parseID("project-id", args[0])
		if err != nil {
			return err
		}
		url := ""
		if len(args) == 2 {
			url = args[1]
		}
		if err := store.UpdateProjectWebhook(id, url); err != nil {
			return fmt.Errorf("set webhook: %w", err)
		}
		if url == "" {
			fmt.Printf("✅ Webhook cleared for project #%d.\n", id)
		} else {
			fmt.Printf("✅ Webhook set for project #%d: %s\n", id, url)
		}
		return nil
	},
}

var projectConfigCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage per-project LLM configuration",
}

var (
	configDefaultModel string
	configBudgetCap    float64
)

var projectConfigSetCmd = &cobra.Command{
	Use:   "set <project-id>",
	Short: "Set default model or budget cap for a project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		id, err := parseID("project-id", args[0])
		if err != nil {
			return err
		}

		if configDefaultModel != "" {
			if err := store.SetProjectDefaultModel(id, configDefaultModel); err != nil {
				return fmt.Errorf("set default model: %w", err)
			}
			fmt.Printf("✅ Default model for project #%d set to %q.\n", id, configDefaultModel)
		}
		if cmd.Flags().Changed("budget-cap") {
			if err := store.SetProjectBudgetCap(id, configBudgetCap); err != nil {
				return fmt.Errorf("set budget cap: %w", err)
			}
			if configBudgetCap == 0 {
				fmt.Printf("✅ Budget cap for project #%d cleared (no cap).\n", id)
			} else {
				fmt.Printf("✅ Budget cap for project #%d set to $%.2f/run.\n", id, configBudgetCap)
			}
		}
		return nil
	},
}

var projectConfigShowCmd = &cobra.Command{
	Use:   "show <project-id>",
	Short: "Show LLM configuration for a project",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		id, err := parseID("project-id", args[0])
		if err != nil {
			return err
		}

		model, budget, err := store.GetProjectConfig(id)
		if err != nil {
			return fmt.Errorf("get config: %w", err)
		}

		if model == "" {
			model = "(unset — falls back to CLI default)"
		}
		budgetStr := "(no cap)"
		if budget > 0 {
			budgetStr = fmt.Sprintf("$%.2f per run", budget)
		}

		fmt.Printf("Project #%d LLM Configuration\n", id)
		fmt.Printf("  Default model:  %s\n", model)
		fmt.Printf("  Budget cap:     %s\n", budgetStr)
		return nil
	},
}

func init() {
	projectAddCmd.Flags().StringVar(&projectSourcePath, "source", "", "Absolute path to the project's source repository")
	projectDepCmd.AddCommand(projectDepAddCmd)
	projectConfigSetCmd.Flags().StringVar(&configDefaultModel, "default-model", "", "Default LLM model for agents in this project")
	projectConfigSetCmd.Flags().Float64Var(&configBudgetCap, "budget-cap", 0, "Max USD spend per run (0 = no cap)")
	projectConfigCmd.AddCommand(projectConfigSetCmd, projectConfigShowCmd)
	projectCmd.AddCommand(projectAddCmd, projectListCmd, projectDepCmd)
	projectCmd.AddCommand(projectSetWebhookCmd)
	projectCmd.AddCommand(projectConfigCmd)
	rootCmd.AddCommand(projectCmd)
}
