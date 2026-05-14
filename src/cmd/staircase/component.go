package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var componentCmd = &cobra.Command{
	Use:   "component",
	Short: "Manage project components",
}

var componentAddCmd = &cobra.Command{
	Use:   "add <project-id> <name>",
	Short: "Register a component within a project",
	Args:  cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		projectID, err := parseID("project-id", args[0])
		if err != nil {
			return err
		}
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		p, err := store.GetProject(projectID)
		if err != nil || p == nil {
			return fmt.Errorf("project %d not found", projectID)
		}
		c, err := store.CreateComponent(projectID, args[1])
		if err != nil {
			return err
		}
		fmt.Printf("✅ Component created (id=%d): %s\n", c.ID, c.Name)
		return nil
	},
}

var componentUpdateCmd = &cobra.Command{
	Use:   "update <id> <new-name>",
	Short: "Rename a component",
	Args:  cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		id, err := parseID("id", args[0])
		if err != nil {
			return err
		}
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		if err := store.UpdateComponent(id, args[1]); err != nil {
			return err
		}
		fmt.Printf("✅ Component %d renamed to %q\n", id, args[1])
		return nil
	},
}

var componentListCmd = &cobra.Command{
	Use:   "list <project-id>",
	Short: "List components for a project",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		projectID, err := parseID("project-id", args[0])
		if err != nil {
			return err
		}
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		components, err := store.ListComponentsByProject(projectID)
		if err != nil {
			return err
		}
		if len(components) == 0 {
			fmt.Println("No components registered for this project.")
			return nil
		}
		table(
			[]string{"ID", "NAME"},
			func() [][]string {
				rows := make([][]string, len(components))
				for i, c := range components {
					rows[i] = []string{fmt.Sprintf("%d", c.ID), c.Name}
				}
				return rows
			}(),
		)
		return nil
	},
}

var componentDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Remove a component (cascades agent_node.component_id to NULL)",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		id, err := parseID("id", args[0])
		if err != nil {
			return err
		}
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		if err := store.DeleteComponent(id); err != nil {
			return err
		}
		fmt.Printf("✅ Component %d deleted\n", id)
		return nil
	},
}

func init() {
	componentCmd.AddCommand(componentAddCmd)
	componentCmd.AddCommand(componentUpdateCmd)
	componentCmd.AddCommand(componentListCmd)
	componentCmd.AddCommand(componentDeleteCmd)
	rootCmd.AddCommand(componentCmd)
}
