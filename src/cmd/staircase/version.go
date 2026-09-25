package main

import (
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

// version, commit, and date are injected at build time by GoReleaser via
// -ldflags "-X main.version=… -X main.commit=… -X main.date=…". Without them
// (go install, go build) the version comes from the module version Go records
// in the binary.
var (
	version = ""
	commit  = "none"
	date    = "unknown"
)

// buildVersion is the injected version, else the recorded module version
// ("v0.3.0" → "0.3.0"), else "dev" for a build from a local checkout.
func buildVersion(injected, module string) string {
	switch {
	case injected != "":
		return injected
	case module != "" && module != "(devel)":
		return strings.TrimPrefix(module, "v")
	}
	return "dev"
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version of stAirCase",
	Run: func(cmd *cobra.Command, args []string) {
		module := ""
		if info, ok := debug.ReadBuildInfo(); ok {
			module = info.Main.Version
		}
		v := buildVersion(version, module)
		if v != "dev" {
			v = "v" + v
		}
		fmt.Printf("stAirCase %s (commit %s, built %s)\n", v, commit, date)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
