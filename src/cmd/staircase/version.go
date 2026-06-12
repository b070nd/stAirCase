package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// version, commit, and date are injected at build time by GoReleaser via
// -ldflags "-X main.version=… -X main.commit=… -X main.date=…". The defaults
// apply to plain `go build` / source builds. Pre-1.0 per the README roadmap.
var (
	version = "0.1.0-dev"
	commit  = "none"
	date    = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version of stAirCase",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("stAirCase v%s (commit %s, built %s)\n", version, commit, date)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
