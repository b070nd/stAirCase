package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version of stAirCase",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("stAirCase v2.0.0-dev\n")
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
