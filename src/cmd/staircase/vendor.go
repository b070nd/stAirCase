package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var vendorCmd = &cobra.Command{
	Use:   "vendor",
	Short: "Manage vendor namespaces",
}

var vendorAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Register a new vendor namespace",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		v, err := store.CreateVendor(args[0])
		if err != nil {
			return fmt.Errorf("create vendor: %w", err)
		}
		fmt.Printf("✅ Vendor #%d %q created.\n", v.ID, v.Name)
		return nil
	},
}

var vendorListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all registered vendors",
	RunE: func(_ *cobra.Command, _ []string) error {
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		vendors, err := store.ListVendors()
		if err != nil {
			return err
		}
		if len(vendors) == 0 {
			fmt.Println("No vendors registered. Use 'staircase vendor add <name>'.")
			return nil
		}

		rows := make([][]string, len(vendors))
		for i, v := range vendors {
			rows[i] = []string{fmt.Sprint(v.ID), v.Name, v.CreatedAt.Format("2006-01-02")}
		}
		table([]string{"ID", "Name", "Created"}, rows)
		return nil
	},
}

func init() {
	vendorCmd.AddCommand(vendorAddCmd, vendorListCmd)
	rootCmd.AddCommand(vendorCmd)
}
