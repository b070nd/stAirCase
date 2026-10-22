package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/agent"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/plan"
	"github.com/b070nd/stAirCase/src/internal/profile"
	"github.com/b070nd/stAirCase/src/internal/tui"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// explicitFlags are the flags the person gave on this command line: what a project's profile must not override.
var explicitFlags = map[string]bool{}

func recordExplicitFlags(cmd *cobra.Command) {
	explicitFlags = map[string]bool{}
	cmd.Flags().Visit(func(f *pflag.Flag) { explicitFlags[f.Name] = true })
}

// applyProfile resolves a project's profile into l, for a run of that project: what the person put on the command line wins, then the
// profile, then the default. A project without a profile changes nothing. It says which fields it filled, and the profile's digest
// goes into the run's terms, so what the run was told about its project is on the record and a continuation keeps it.
func applyProfile(l *launch, wsDir string, projectID int64) error {
	p, found, err := profile.Load(wsDir, projectID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	var used []string
	fill := func(field string, set bool) {
		if set {
			used = append(used, field)
		}
	}
	if len(p.Checks) > 0 && !explicitFlags["check"] && len(l.terms.Checks) == 0 {
		l.terms.Checks = slices.Clone(p.Checks)
		fill("checks", true)
	}
	if len(p.Protect) > 0 && !explicitFlags["protect"] && len(l.terms.ProtectedPaths) == 0 {
		l.terms.ProtectedPaths = slices.Clone(p.Protect)
		fill("protect", true)
	}
	if p.Sandbox != "" && !explicitFlags["sandbox"] && (l.terms.Sandbox == "" || l.terms.Sandbox == "auto") {
		l.terms.Sandbox = p.Sandbox
		fill("sandbox", true)
	}
	if len(p.Scope) > 0 && !explicitFlags["allow"] && len(l.allow) == 0 {
		l.allow = slices.Clone(p.Scope)
		l.profileScope = true
		fill("scope", true)
	}
	if p.Instructions != "" && l.terms.Instructions == "" { // no flag carries them: the profile is where a project's words live
		l.terms.Instructions = p.Instructions
		fill("instructions", true)
	}
	if len(p.Unreadable) > 0 { // what a project keeps from the agent: no flag takes it away, as with the sensitive paths
		l.terms.Unreadable = slices.Compact(slices.Sorted(slices.Values(append(slices.Clone(l.terms.Unreadable), p.Unreadable...))))
		fill("unreadable", true)
	}
	if len(p.Visible) > 0 && !explicitFlags["visible"] && len(l.terms.Visible) == 0 {
		l.terms.Visible = slices.Clone(p.Visible)
		fill("visible", true)
	}
	if len(p.Sensitive) > 0 { // a profile only adds to what needs a person: no flag takes it away
		l.terms.SensitivePaths = slices.Clone(p.Sensitive)
		fill("sensitive", true)
	}
	l.terms.ProfileSHA256 = p.Digest()
	if len(used) > 0 {
		fmt.Printf("📋 Profile of project #%d: %s (a flag on the command line overrides it)\n", projectID, strings.Join(used, ", "))
	}
	return nil
}

// profileForRoot applies the profile of the project whose source is root, when the workspace has one: a session starts before it makes
// anything, and its terms are judged first.
func profileForRoot(l *launch, wsDir, root string) error {
	if _, err := os.Stat(wsDir + "/workspace.db"); err != nil {
		return nil
	}
	db, err := persistence.InitDB(wsDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	projects, err := persistence.NewStore(db).ListAllProjects()
	if err != nil {
		return err
	}
	for _, p := range projects {
		if samePath(p.SourcePath, root) {
			return applyProfile(l, wsDir, p.ID)
		}
	}
	return nil
}

var projectProfileCmd = &cobra.Command{
	Use:   "profile",
	Short: "What is true of a project for every run of it (kept in the workspace, never in the repository)",
	Long: `A project's profile holds what every run of it should assume: the paths a person must always decide (--protect for the files that
define a check, --sensitive for others, such as a contract file), the scope a change may touch (--scope), the checks and the sandbox.
It is stored in the workspace, so nothing about stAirCase is ever committed to the client's repository, and a run reads it when it
starts. A flag on the command line overrides it for one run; its digest is recorded in the run's terms; a profile can add paths that
need a person and never take any away.`,
}

var (
	profileProtect, profileSensitive, profileScope, profileVisible, profileUnreadable, profileChecks, profileClear []string
	profileUnreadableDefaults                                                                                      bool
	profileSandbox, profileInstructions, profileInstructionsFile                                                   string
)

var projectProfileShowCmd = &cobra.Command{
	Use:   "show <project-id>",
	Short: "Show a project's profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID("project-id", args[0])
		if err != nil {
			return err
		}
		return showProfile(cmd.OutOrStdout(), viper.GetString("STAIRCASE_DIR"), id)
	},
}

var projectProfileSetCmd = &cobra.Command{
	Use:   "set <project-id>",
	Short: "Set fields of a project's profile (a field named replaces it; --clear removes one)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID("project-id", args[0])
		if err != nil {
			return err
		}
		set := map[string]bool{}
		cmd.Flags().Visit(func(f *pflag.Flag) { set[f.Name] = true })
		return setProfile(cmd.OutOrStdout(), viper.GetString("STAIRCASE_DIR"), id, set)
	},
}

func init() {
	projectProfileSetCmd.Flags().StringArrayVar(&profileProtect, "protect", nil, "Paths or patterns, from the repository's root, where a check is defined: a change to one always needs a person")
	projectProfileSetCmd.Flags().StringArrayVar(&profileSensitive, "sensitive", nil, "Paths or patterns a change to which always needs a person, in addition to the built-in list (a contract file, say)")
	projectProfileSetCmd.Flags().StringArrayVar(&profileScope, "scope", nil, "Paths or patterns a run's changes may touch; a change elsewhere comes to a person as drift")
	projectProfileSetCmd.Flags().StringArrayVar(&profileVisible, "visible", nil, "Ignored directories or files of the checkout that checks need and see read-only (vendor/, generated/): literal paths, from the repository's root")
	projectProfileSetCmd.Flags().StringArrayVar(&profileUnreadable, "unreadable", nil, "Paths or patterns, from the repository's root, that the agent must not read (a tracked auth.json): an agent that cannot be stopped from reading them is not started on the project")
	projectProfileSetCmd.Flags().BoolVar(&profileUnreadableDefaults, "unreadable-defaults", false, "Add the usual list to the unreadable paths: auth.json, .env files, private keys (it is written into the profile, which you can edit)")
	projectProfileSetCmd.Flags().StringVar(&profileInstructions, "instructions", "", "What every agent on this project is told before its task (conventions, rules of the house), as text")
	projectProfileSetCmd.Flags().StringVar(&profileInstructionsFile, "instructions-file", "", "The same, read from a file now (the profile keeps a copy in the workspace; the file stays where it is)")
	projectProfileSetCmd.Flags().StringArrayVar(&profileChecks, "check", nil, "A command (such as the tests) run on the commit a run makes; repeat for more")
	projectProfileSetCmd.Flags().StringVar(&profileSandbox, "sandbox", "", "Where approved commands and checks run: auto, required, off or docker:image=<ref>")
	projectProfileSetCmd.Flags().StringArrayVar(&profileClear, "clear", nil, "Remove a field: "+strings.Join(profile.Fields, ", ")+" (repeatable)")
	projectProfileCmd.AddCommand(projectProfileShowCmd, projectProfileSetCmd)
	projectCmd.AddCommand(projectProfileCmd)
}

func requireProject(wsDir string, id int64) (*domain.Project, error) {
	db, err := persistence.InitDB(wsDir)
	if err != nil {
		return nil, fmt.Errorf("workspace: %w", err)
	}
	defer func() { _ = db.Close() }()
	p, err := persistence.NewStore(db).GetProject(id)
	if err != nil || p == nil {
		return nil, fmt.Errorf("project #%d not found", id)
	}
	return p, nil
}

func showProfile(w io.Writer, wsDir string, id int64) error {
	p, err := requireProject(wsDir, id)
	if err != nil {
		return err
	}
	pr, found, err := profile.Load(wsDir, id)
	if err != nil {
		return err
	}
	if !found {
		fmt.Fprintf(w, "Project #%d (%s) has no profile: every run uses the flags and the defaults.\n", id, p.Name)
		return nil
	}
	fmt.Fprintf(w, "Project #%d (%s), profile %.12s\n", id, p.Name, pr.Digest())
	show := func(name string, v []string) {
		if len(v) > 0 {
			fmt.Fprintf(w, "  %-10s %s\n", name, strings.Join(v, ", "))
		}
	}
	show("protect", pr.Protect)
	show("sensitive", pr.Sensitive)
	show("scope", pr.Scope)
	show("visible", pr.Visible)
	show("unreadable", pr.Unreadable)
	show("checks", pr.Checks)
	if pr.Instructions != "" {
		sum := sha256.Sum256([]byte(pr.Instructions))
		first, _, _ := strings.Cut(pr.Instructions, "\n")
		if len(first) > 70 {
			first = first[:70] + "..."
		}
		fmt.Fprintf(w, "  %-10s %d bytes, sha256 %.12s: %s\n", "instructions", len(pr.Instructions), hex.EncodeToString(sum[:]), tui.Safe(first))
	}
	if pr.Sandbox != "" {
		fmt.Fprintf(w, "  %-10s %s\n", "sandbox", pr.Sandbox)
	}
	return nil
}

func setProfile(w io.Writer, wsDir string, id int64, named map[string]bool) error {
	if _, err := requireProject(wsDir, id); err != nil {
		return err
	}
	pr, _, err := profile.Load(wsDir, id)
	if err != nil {
		return err
	}
	for _, field := range profileClear {
		switch field {
		case "protect":
			pr.Protect = nil
		case "sensitive":
			pr.Sensitive = nil
		case "scope":
			pr.Scope = nil
		case "visible":
			pr.Visible = nil
		case "unreadable":
			pr.Unreadable = nil
		case "instructions":
			pr.Instructions = ""
		case "checks":
			pr.Checks = nil
		case "sandbox":
			pr.Sandbox = ""
		default:
			return fmt.Errorf("--clear %q: one of %s", field, strings.Join(profile.Fields, ", "))
		}
	}
	if named["protect"] {
		pr.Protect = profileProtect
	}
	if named["sensitive"] {
		pr.Sensitive = profileSensitive
	}
	if named["scope"] {
		pr.Scope = profileScope
	}
	if named["visible"] {
		pr.Visible = profileVisible
	}
	if named["unreadable"] {
		pr.Unreadable = profileUnreadable
	}
	if named["unreadable-defaults"] {
		pr.Unreadable = append(slices.Clone(pr.Unreadable), profile.DefaultUnreadable...)
	}
	if named["instructions"] && named["instructions-file"] {
		return errors.New("give the instructions once: --instructions (text) or --instructions-file")
	}
	if named["instructions"] {
		pr.Instructions = profileInstructions
	}
	if named["instructions-file"] {
		b, err := os.ReadFile(profileInstructionsFile)
		if err != nil {
			return fmt.Errorf("--instructions-file: %w", err)
		}
		pr.Instructions = string(b)
	}
	if named["check"] {
		pr.Checks = profileChecks
	}
	if named["sandbox"] {
		pr.Sandbox = profileSandbox
	}
	if len(named) == 0 {
		return errors.New("nothing to set: name a field (--protect, --sensitive, --scope, --visible, --unreadable, --instructions, --check, --sandbox) or --clear one")
	}
	if err := profile.Save(wsDir, id, pr); err != nil {
		return err
	}
	return showProfile(w, wsDir, id)
}

var caseBriefingCmd = &cobra.Command{
	Use:   "briefing <case-id>",
	Short: "Show the exact text an agent is told for a compiled case",
	Long: `Prints what the agent of a compiled case is told: the project's instructions (from its profile), then the task (the PRD, the stories with
their scope, and the reviewers' lessons), then what the agent's own harness adds. It is the text the agents are given, composed by the same
function. An away run puts its own brief first and a continuation adds the task as it stood: those depend on the run and are not shown.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := parseID("case-id", args[0])
		if err != nil {
			return err
		}
		return showBriefing(cmd.OutOrStdout(), viper.GetString("STAIRCASE_DIR"), id)
	},
}

func init() { caseCmd.AddCommand(caseBriefingCmd) }

func showBriefing(w io.Writer, wsDir string, caseID int64) error {
	pl, err := plan.Load(filepath.Join(wsDir, "tmp", fmt.Sprintf("plan_case%d.json", caseID)))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("case #%d is not compiled - run 'staircase compile %d' first", caseID, caseID)
	case err != nil:
		return fmt.Errorf("plan for case #%d: %w", caseID, err)
	}
	db, err := persistence.InitDB(wsDir)
	if err != nil {
		return fmt.Errorf("workspace: %w", err)
	}
	defer func() { _ = db.Close() }()
	c, err := persistence.NewStore(db).GetCase(caseID)
	if err != nil || c == nil {
		return fmt.Errorf("case #%d not found", caseID)
	}
	p, _, err := profile.Load(wsDir, c.ProjectID)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, agent.BriefingFor(pl.Harness, pl.Brief(), &orchestrator.AgentEnv{Instructions: p.Instructions}, pl.RepoContext))
	return err
}
