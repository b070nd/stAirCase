package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/b070nd/stAirCase/src/internal/governance"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

var (
	governanceRef string
	governanceYes bool
)

var governanceCmd = &cobra.Command{
	Use:   "governance",
	Short: "Take the team's rules and trusted keys from a governance repository",
	Long: `A team keeps its rules and trusted keys in one git repository:

  policy.json      the rules and limits every run uses (required)
  allowed_signers  people trusted for two-person review (git's format)
  keys/*.pub       team members' workspace signing keys (.signing.pub)
  blueprints/<name>/  blueprints the team shares (blueprint.yaml and its files)

staircase governance use installs them in your workspace, pinned to an exact
commit; changes reach you only when you run it again. The blueprints are checked
like 'blueprint import' checks a folder and imported as snapshots whose source commit
is the pinned one, so everyone on the team gets the same hash; bind one to a project
with 'staircase project bind'. verify and report then
trust every team member's certificates, and verify counts the team's reviewers
for CAL 4.`,
}

var governanceUseCmd = &cobra.Command{
	Use:   "use <repository>",
	Short: "Install the rules and keys of a governance repository, pinned to its current commit",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		if !governanceYes {
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return errors.New("this replaces the workspace's policy.json and trusted keys: confirm with --yes")
			}
			fmt.Printf("This replaces the workspace's policy.json, allowed signers and trusted keys with those of\n  %s (%s)\nContinue? [y/N] ", args[0], governanceRef)
			var answer string
			_, _ = fmt.Scanln(&answer)
			if answer != "y" && answer != "Y" && answer != "yes" {
				return errors.New("nothing changed")
			}
		}
		pin, err := governance.Use(viper.GetString("STAIRCASE_DIR"), args[0], governanceRef)
		if err != nil {
			return err
		}
		fmt.Printf("✅ Governance from %s at %.12s: %d file(s) installed\n", pin.Source, pin.Commit, len(pin.Files))
		for _, name := range slices.Sorted(maps.Keys(pin.Files)) {
			fmt.Printf("   %s\n", name)
		}
		if len(pin.Blueprints) > 0 {
			store, db, err := openStore()
			if err != nil {
				return err
			}
			defer func() { _ = db.Close() }()
			imported, err := governance.ImportBlueprints(store, viper.GetString("STAIRCASE_DIR"), pin)
			if err != nil {
				return err
			}
			for _, b := range imported {
				state := "imported"
				if !b.Created {
					state = "already imported"
				}
				fmt.Printf("   blueprint %s (%s) %.12s: bind it with staircase project bind <project-id> %.12s\n", b.Name, state, b.Hash, b.Hash)
			}
		}
		return nil
	},
}

var governanceStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the pinned governance commit, and whether the source or the workspace changed since",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		st, err := governance.Status(viper.GetString("STAIRCASE_DIR"))
		if err != nil {
			return err
		}
		fmt.Printf("Governance: %s (%s) at %.12s\n", st.Pin.Source, st.Pin.Ref, st.Pin.Commit)
		if st.Behind {
			fmt.Printf("   ⚠️  the source is at %.12s now: review it, then run staircase governance use again\n", st.Latest)
		} else {
			fmt.Println("   ✅ up to date with the source")
		}
		for _, name := range slices.Sorted(maps.Keys(st.Pin.Blueprints)) {
			fmt.Printf("   blueprint %s %.12s\n", name, st.Pin.Blueprints[name])
		}
		for _, f := range st.Modified {
			fmt.Printf("   ⚠️  %s was changed in the workspace since it was installed\n", f)
		}
		return nil
	},
}

func init() {
	governanceUseCmd.Flags().StringVar(&governanceRef, "ref", "main", "Branch, tag or commit of the governance repository")
	governanceUseCmd.Flags().BoolVarP(&governanceYes, "yes", "y", false, "Replace without asking (needed without a terminal)")
	governanceCmd.AddCommand(governanceUseCmd, governanceStatusCmd)
	rootCmd.AddCommand(governanceCmd)
}
