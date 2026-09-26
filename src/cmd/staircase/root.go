package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var rootCmd = &cobra.Command{
	Use:   "staircase",
	Short: "stAirCase - AI Workspace Orchestrator",
	Long:  `Deterministic, version-controlled infrastructure for multi-agent AI swarms.`,
	// Execute prints each error exactly once. Usage is shown only for invalid
	// invocations: cobra validates flags/args before PersistentPreRun runs.
	SilenceErrors:    true,
	PersistentPreRun: func(cmd *cobra.Command, _ []string) { cmd.SilenceUsage = true },
}

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.PersistentFlags().String("dir", "", "Workspace directory (default: ~/.staircase-workspace)")
	_ = viper.BindPFlag("STAIRCASE_DIR", rootCmd.PersistentFlags().Lookup("dir"))
}

func initConfig() {
	viper.AutomaticEnv() // Read STAIRCASE_DIR from env if present

	if viper.GetString("STAIRCASE_DIR") == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		viper.Set("STAIRCASE_DIR", filepath.Join(home, ".staircase-workspace"))
	}
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
