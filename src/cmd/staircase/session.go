package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/plan"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

var (
	sessionAllow      []string
	sessionYes        bool
	sessionInScope    bool         // --approve-in-scope
	sessionOnEvidence bool         // --approve-on-evidence
	sessionReview     *plan.Review // set by staircase review
)

var claudeCmd = &cobra.Command{
	Use:   "claude <task>",
	Short: "Run Claude Code on a task in this repository, with every change decided by you",
	Long: `Runs Claude Code on the task in a separate worktree of the git repository you
are in. Every file change and command it wants to make comes to you first. At
the end, exactly the approved changes are committed on a new branch,
staircase/run-N; your checkout is not touched.

No setup is needed: the workspace, a project for this repository and a case for
the task are created when missing. Claude Code must be installed and logged in.

--allow limits the paths the task may change; changes elsewhere come to you as
drift (see docs/drift.md).`,
	Args: cobra.ArbitraryArgs,
	RunE: claudeSession,
}

var codexCmd = &cobra.Command{
	Use:   "codex <task>",
	Short: "Run Codex on a task in this repository, with every change decided by you",
	Long: `Runs OpenAI's Codex CLI on the task in a separate worktree of the git
repository you are in. Every file edit it wants to make comes to you first.
Its shell commands run in Codex's sandbox (no network, writes only in the
worktree), and the files a command changes come to you afterwards: kept if
you approve, reverted if not. At the end, exactly the approved changes are
committed on a new branch, staircase/run-N; your checkout is not touched.

No setup is needed. Codex must be installed (the ChatGPT app for macOS
includes it) and logged in.`,
	Args: cobra.ArbitraryArgs,
	RunE: codexSession,
}

var geminiCmd = &cobra.Command{
	Use:   "gemini <task>",
	Short: "Run Gemini CLI on a task in this repository, with every change decided by you (experimental)",
	Long: `Runs Google's Gemini CLI on the task in a separate worktree of the git repository
you are in. Every file change and command it wants to make comes to you first
(commands need --allow-shell-exec); every other tool is refused. At the end,
exactly the approved changes are committed on a new branch, staircase/run-N;
your checkout is not touched.

Experimental: run for real on Gemini CLI 0.46.0 (a governed run, a held approval, a
rejection with its retry, a resume of its own session, and the control for a change
written around the hooks), otherwise tested against a stand-in. Gemini CLI must be
installed and logged in. Its commands run without a sandbox, so a run with commands reaches CAL 2.
Hooks in your own or the repository's .gemini settings still run beside
stAirCase's (Gemini cannot be told to ignore them); a run fails if Gemini never
calls stAirCase's hooks.`,
	Args: cobra.ArbitraryArgs,
	RunE: geminiSession,
}

var opencodeCmd = &cobra.Command{
	Use:   "opencode <task>",
	Short: "Run OpenCode on a task in this repository, with every change decided by you (experimental)",
	Long: `Runs OpenCode on the task in a separate worktree of the git repository you are
in. Every file edit, patch and command it wants to make comes to you first
(commands need --allow-shell-exec); every other tool is refused. At the end,
exactly the approved changes are committed on a new branch, staircase/run-N;
your checkout is not touched.

Experimental: run once against the real OpenCode 1.18.35 (one governed run, and a
rejection with its retry, verified in a fresh clone); otherwise tested against a stand-in
that runs the generated plugin, and no control for an ungated change has been run. OpenCode
must be installed and have a provider logged in (opencode auth login). Its
commands run without a sandbox, so a run with commands reaches CAL 2. Plugins in
the repository's .opencode folder still load beside stAirCase's (OpenCode cannot
be told to ignore them); a run fails if OpenCode never calls stAirCase's plugin.`,
	Args: cobra.ArbitraryArgs,
	RunE: opencodeSession,
}

func init() {
	sessionFlags(opencodeCmd, "OpenCode", true)
	sessionFlags(geminiCmd, "Gemini CLI", true)
	claudeCmd.Flags().StringArrayVar(&sessionAllow, "allow", nil, "A path (glob) the task may change; repeat for more")
	claudeCmd.Flags().BoolVar(&runAllowShellExec, "allow-shell-exec", false,
		"Let Claude Code propose shell commands (each still needs your approval)")
	claudeCmd.Flags().IntVar(&runApprovalPort, "approval-port", 0,
		"Decide from another terminal or a script through the local approval API on this port (0 = in this terminal)")
	claudeCmd.Flags().StringVar(&runApprovalToken, "approval-token", "", "Token for the approval API (default: a new one, printed)")
	claudeCmd.Flags().StringVar(&runModel, "model", "", "Model for Claude Code (default: its own)")
	claudeCmd.Flags().StringArrayVar(&runPassEnv, "pass-env", nil,
		"Name of an environment variable Claude Code may inherit, to give it its own credential (for example CLAUDE_CODE_OAUTH_TOKEN); it, and any command it runs, can read the value. Repeat for more")
	checkFlag(claudeCmd)
	checkFlag(codexCmd)
	signFlags(claudeCmd)
	signFlags(codexCmd)
	validatorFlag(claudeCmd)
	validatorFlag(codexCmd)
	inScopeFlag(claudeCmd)
	inScopeFlag(codexCmd)
	claudeCmd.Flags().BoolVarP(&sessionYes, "yes", "y", false, "Start without asking to confirm the task (needed without a terminal)")
	rootCmd.AddCommand(claudeCmd)
	codexCmd.Flags().BoolVarP(&sessionYes, "yes", "y", false, "Start without asking to confirm the task (needed without a terminal)")
	codexCmd.Flags().StringVar(&runModel, "model", "", "Model for Codex (default: its own)")
	codexCmd.Flags().StringArrayVar(&sessionAllow, "allow", nil, "A path (glob) the task may change; repeat for more")
	codexCmd.Flags().IntVar(&runApprovalPort, "approval-port", 0,
		"Decide from another terminal or a script through the local approval API on this port (0 = in this terminal)")
	codexCmd.Flags().StringVar(&runApprovalToken, "approval-token", "", "Token for the approval API (default: a new one, printed)")
	rootCmd.AddCommand(codexCmd)
}

// claudeSession is `staircase claude <task>`.
func claudeSession(_ *cobra.Command, args []string) error {
	return session("claude-code", "Claude Code", "claude", args)
}

// codexSession is `staircase codex <task>`.
func codexSession(_ *cobra.Command, args []string) error {
	return session("codex", "Codex", "codex", args)
}

// geminiSession is `staircase gemini <task>`.
func geminiSession(_ *cobra.Command, args []string) error {
	return session("gemini", "Gemini CLI", "gemini", args)
}

// opencodeSession is `staircase opencode <task>`.
func opencodeSession(_ *cobra.Command, args []string) error {
	return session("opencode", "OpenCode", "opencode", args)
}

// sessionFlags are the flags of a session command for an agent: what it may
// change, whether it may ask for commands, how to decide, checks, signing,
// reviewers.
func sessionFlags(cmd *cobra.Command, agentName string, shell bool) {
	cmd.Flags().StringArrayVar(&sessionAllow, "allow", nil, "A path (glob) the task may change; repeat for more")
	if shell {
		cmd.Flags().BoolVar(&runAllowShellExec, "allow-shell-exec", false,
			"Let "+agentName+" propose shell commands (each still needs your approval)")
	}
	cmd.Flags().IntVar(&runApprovalPort, "approval-port", 0,
		"Decide from another terminal, a script or your browser through the local approval API on this port (0 = in this terminal)")
	cmd.Flags().StringVar(&runApprovalToken, "approval-token", "", "Token for the approval API (default: a new one, printed)")
	cmd.Flags().StringVar(&runModel, "model", "", "Model for "+agentName+" (default: its own)")
	if agentName == "Gemini CLI" {
		cmd.Flags().StringArrayVar(&runPassEnv, "pass-env", nil,
			"Name of an environment variable Gemini may inherit, to give it its own credential (for example GEMINI_API_KEY); it, and any command it runs, can read the value. Repeat for more")
	}
	cmd.Flags().BoolVarP(&sessionYes, "yes", "y", false, "Start without asking to confirm the task (needed without a terminal)")
	checkFlag(cmd)
	signFlags(cmd)
	validatorFlag(cmd)
	inScopeFlag(cmd)
	rootCmd.AddCommand(cmd)
}

// session is a governed run of harness in the current repository, with
// nothing to set up first.
func session(harness, name, command string, args []string) error {
	task := strings.TrimSpace(strings.Join(args, " "))
	if task == "" {
		return fmt.Errorf(`what should %s do? For example: staircase %s "add a /health endpoint"`, name, command)
	}
	if sessionOnEvidence {
		sessionInScope = true // approving on evidence is approving the task, with checks and reviewers instead of samples
		if len(runChecks) == 0 && len(runValidator) == 0 {
			return fmt.Errorf("--approve-on-evidence needs evidence to decide on: name a --check (your tests) and/or a --validator")
		}
	}
	if sessionInScope && len(sessionAllow) == 0 {
		return fmt.Errorf("--approve-in-scope and --approve-on-evidence need the task's scope: name the paths it may change with --allow")
	}
	root, err := gitTopLevel()
	if err != nil {
		return err
	}

	wsDir := viper.GetString("STAIRCASE_DIR")
	if _, err := os.Stat(filepath.Join(wsDir, "workspace.db")); errors.Is(err, os.ErrNotExist) {
		fmt.Printf("🧰 Creating the workspace in %s\n", wsDir)
	}
	db, err := persistence.InitDB(wsDir)
	if err != nil {
		return fmt.Errorf("workspace: %w", err)
	}
	store := persistence.NewStore(db)
	var storyID int64
	caseID, err := func() (int64, error) {
		defer func() { _ = db.Close() }() // the run opens the workspace itself
		if err := crypto.GenerateKey(wsDir); err != nil {
			return 0, err
		}
		if err := crypto.GenerateSigningKey(wsDir); err != nil {
			return 0, err
		}
		project, err := sessionProject(store, root)
		if err != nil {
			return 0, err
		}
		_, budget, _ := store.GetProjectConfig(project.ID)
		base, _ := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
		if runAgreedBy, err = agree(sessionSetup{harness: harness, name: name, task: task, root: root,
			base: strings.TrimSpace(string(base)), allow: sessionAllow, checks: runChecks, inScope: sessionInScope, onEvidence: sessionOnEvidence, validators: len(runValidator), signed: runSignKey != "", requireSigned: runRequireSigned, model: runModel,
			shell: runAllowShellExec, budget: budget, projectID: project.ID}); err != nil {
			return 0, err
		}
		c, err := store.CreateCase(project.ID)
		if err != nil {
			return 0, err
		}
		if err := store.SetCasePRD(c.ID, task); err != nil {
			return 0, err
		}
		story, err := store.CreateUserStory(c.ID, task)
		if err != nil {
			return 0, err
		}
		storyID = story.ID
		if len(sessionAllow) > 0 {
			scope, _ := json.Marshal(map[string]any{"allow": sessionAllow})
			if err := store.SetUserStoryScope(story.ID, string(scope)); err != nil {
				return 0, err
			}
		}
		fmt.Printf("🧭 Project %q (#%d), case #%d, story #%d\n", project.Name, project.ID, c.ID, story.ID)
		if _, err := compileCase(store, wsDir, c.ID, harness, sessionReview, true); err != nil {
			return 0, err
		}
		return c.ID, nil
	}()
	if err != nil {
		return err
	}
	if err := runCase(caseID); err != nil {
		return err
	}
	branch := "staircase/run-<id>"
	if db, err := persistence.InitDB(wsDir); err == nil {
		if runs, err := persistence.NewStore(db).ListRunsByCase(caseID); err == nil && len(runs) > 0 {
			branch = fmt.Sprintf("staircase/run-%d", runs[len(runs)-1].ID)
		}
		_ = db.Close()
	}
	return sessionFinish(root, branch, storyID)
}

// sessionFinish tells what to do with a session's result; staircase seal
// replaces it.
var sessionFinish = func(root, branch string, storyID int64) error {
	fmt.Printf("\nThe approved changes are on %s. Review them, then accept the story when the task is done:\n"+
		"  git -C %s diff HEAD...%s\n  staircase story accept %d\n", branch, root, branch, storyID)
	return nil
}

// gitTopLevel is the top of the git repository the current directory is in.
// It must have a commit: the agents start from it.
func gitTopLevel() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", errors.New("not inside a git repository: run staircase from the repository the task is about")
	}
	root := strings.TrimSpace(string(out))
	if err := exec.Command("git", "-C", root, "rev-parse", "--verify", "-q", "HEAD").Run(); err != nil {
		return "", fmt.Errorf("%s has no commit yet: commit something first; the agent starts from your last commit", root)
	}
	return root, nil
}

// sessionProject is the workspace project for the repository at root: the
// one whose source is root, else a new one under vendor "local", named after
// the folder.
func sessionProject(store *persistence.Store, root string) (*domain.Project, error) {
	projects, err := store.ListAllProjects()
	if err != nil {
		return nil, err
	}
	for _, p := range projects {
		if samePath(p.SourcePath, root) {
			return &p, nil
		}
	}
	v, err := store.GetVendorByName("local")
	if err != nil {
		return nil, err
	}
	if v == nil {
		if v, err = store.CreateVendor("local"); err != nil {
			return nil, err
		}
	}
	name := filepath.Base(root)
	for i := 2; ; i++ {
		if p, err := store.GetProjectByVendorAndName(v.ID, name); err != nil {
			return nil, err
		} else if p == nil {
			break
		}
		name = fmt.Sprintf("%s-%d", filepath.Base(root), i)
	}
	fmt.Printf("📁 New project %q for %s\n", name, root)
	return store.CreateProject(v.ID, name, root)
}

// samePath compares two directories after resolving symlinks (/tmp and
// /private/tmp on macOS are the same folder).
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// sessionSetup is what a session is about to do, for the person to agree to.
type sessionSetup struct {
	harness, name, task, root, base, model string
	allow, checks                          []string
	inScope, onEvidence                    bool
	validators                             int
	signed, requireSigned                  bool
	shell                                  bool
	budget                                 float64
	projectID                              int64
}

// agreement describes the session before its agent starts: the task, where
// it may change files, how edits and commands are governed for this agent,
// the model and the budget.
func agreement(s sessionSetup) string {
	var b strings.Builder
	if s.harness == "review" {
		fmt.Fprintf(&b, "stAirCase is about to review changes made elsewhere:\n  %s\n", s.task)
	} else {
		fmt.Fprintf(&b, "stAirCase is about to run %s on:\n  %s\n", s.name, s.task)
	}
	fmt.Fprintf(&b, "in %s, from commit %.12s (your checkout is not touched)\n\n", s.root, s.base)
	scope := "the whole repository"
	if len(s.allow) > 0 {
		scope = strings.Join(s.allow, ", ") + " (a change anywhere else comes to you as drift)"
	}
	fmt.Fprintf(&b, "  may change:  %s\n", scope)
	switch {
	case s.onEvidence:
		fmt.Fprintf(&b, "  edits:       inside the scope approved when the evidence is there: %s;\n"+
			"               otherwise the change comes to you with what was missing; nothing is sampled for you,\n"+
			"               sensitive files and anything outside come to you, and you approve the whole change at the end\n", evidenceSources(s))
	case s.inScope:
		fmt.Fprintf(&b, "  edits:       inside the scope approved as part of this task, 1 in 5 shown to you;\n"+
			"               sensitive files and anything outside come to you; you approve the whole change at the end\n")
	case s.harness == "review":
		fmt.Fprintf(&b, "  files:       each changed file comes to you, one at a time; rejected ones are left out\n")
	default:
		fmt.Fprintf(&b, "  edits:       each one comes to you before it happens\n")
	}
	switch {
	case s.harness == "review":
		fmt.Fprintf(&b, "  commands:    none (the changes were made elsewhere)\n")
	case s.harness == "codex":
		fmt.Fprintf(&b, "  commands:    run in Codex's sandbox (no network); files they change come to you afterwards\n")
	case s.shell && (s.harness == "gemini" || s.harness == "opencode"):
		fmt.Fprintf(&b, "  commands:    each one comes to you before it runs, without a sandbox (the change reaches CAL 2)\n")
	case s.shell && s.harness == "claude-code":
		fmt.Fprintf(&b, "  commands:    each one comes to you before it runs in Claude Code's sandbox (no network)\n")
	case s.shell:
		fmt.Fprintf(&b, "  commands:    each one comes to you before it runs\n")
	default:
		fmt.Fprintf(&b, "  commands:    not allowed (--allow-shell-exec lets it ask)\n")
	}
	if len(s.checks) > 0 {
		fmt.Fprintf(&b, "  checks:      %s (must pass before the agent may finish; results in the certificate)\n", strings.Join(s.checks, "; "))
	}
	switch {
	case s.requireSigned:
		fmt.Fprintf(&b, "  decisions:   must be signed with an SSH key of a trusted signer\n")
	case s.signed:
		fmt.Fprintf(&b, "  decisions:   signed with your SSH key as you decide\n")
	}
	model := s.model
	if model == "" {
		model = s.name + "'s default"
	}
	fmt.Fprintf(&b, "  model:       %s\n", model)
	if s.budget > 0 {
		fmt.Fprintf(&b, "  budget:      at most $%.2f\n", s.budget)
	} else {
		fmt.Fprintf(&b, "  budget:      no budget cap (staircase project config set %d --budget-cap <dollars>)\n", s.projectID)
	}
	return b.String()
}

// agree shows the agreement and asks for it; with --yes it only shows it. It
// returns who agreed, for the audit chain.
func agree(s sessionSetup) (string, error) {
	fmt.Print(agreement(s))
	if sessionYes {
		return "--yes", nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("no terminal to confirm the task: add --yes to start without asking")
	}
	fmt.Print("\nStart? [y/N] ")
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
		return "", errors.New("not started")
	}
	return "operator", nil
}

// evidenceSources says what an evidence-based approval rests on.
func evidenceSources(s sessionSetup) string {
	var parts []string
	if len(s.checks) > 0 {
		parts = append(parts, fmt.Sprintf("your %d check(s) pass on the change", len(s.checks)))
	}
	if s.validators > 0 {
		parts = append(parts, "the reviewer model(s) agree")
	}
	return strings.Join(parts, " and ")
}

// inScopeFlag adds --approve-in-scope and --approve-on-evidence to cmd.
func inScopeFlag(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&sessionOnEvidence, "approve-on-evidence", false,
		"Approve changes inside the --allow scope only when the evidence is there: your --check commands pass on the state the change produces, "+
			"and the --validator models agree. Otherwise the change comes to you with what was missing. Replaces the 1-in-5 sampling; needs --check and/or --validator")
	cmd.Flags().BoolVar(&sessionInScope, "approve-in-scope", false,
		"Approve changes inside the --allow scope as part of the agreed task instead of one by one: "+
			"1 in 5, sensitive files and anything outside still come to you, and you approve the whole change at the end")
}
