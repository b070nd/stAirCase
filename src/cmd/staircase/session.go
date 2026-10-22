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
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/policy"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
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
	claudeCmd.Flags().StringArrayVar(&cli.allow, "allow", nil, "A path (glob) the task may change; repeat for more")
	claudeCmd.Flags().BoolVar(&cli.terms.AllowShellExec, "allow-shell-exec", false,
		"Let Claude Code propose shell commands (each still needs your approval)")
	claudeCmd.Flags().IntVar(&cli.approvalPort, "approval-port", 0,
		"Decide from another terminal or a script through the local approval API on this port (0 = in this terminal)")
	claudeCmd.Flags().StringVar(&cli.approvalToken, "approval-token", "", "Token for the approval API (default: a new one, printed)")
	claudeCmd.Flags().StringVar(&cli.model, "model", "", "Model for Claude Code (default: its own)")
	claudeCmd.Flags().StringArrayVar(&cli.passEnv, "pass-env", nil,
		"Name of an environment variable Claude Code may inherit, to give it its own credential (for example CLAUDE_CODE_OAUTH_TOKEN); it, and any command it runs, can read the value. Repeat for more")
	checkFlag(claudeCmd)
	checkFlag(codexCmd)
	signFlags(claudeCmd)
	signFlags(codexCmd)
	validatorFlag(claudeCmd)
	validatorFlag(codexCmd)
	inScopeFlag(claudeCmd)
	inScopeFlag(codexCmd)
	awayFlag(claudeCmd)
	awayFlag(codexCmd)
	sandboxFlag(claudeCmd)
	sandboxFlag(codexCmd)
	changeSetFlag(claudeCmd)
	changeSetFlag(codexCmd)
	claudeCmd.Flags().BoolVarP(&cli.yes, "yes", "y", false, "Start without asking to confirm the task (needed without a terminal)")
	rootCmd.AddCommand(claudeCmd)
	codexCmd.Flags().BoolVarP(&cli.yes, "yes", "y", false, "Start without asking to confirm the task (needed without a terminal)")
	codexCmd.Flags().StringVar(&cli.model, "model", "", "Model for Codex (default: its own)")
	codexCmd.Flags().StringArrayVar(&cli.allow, "allow", nil, "A path (glob) the task may change; repeat for more")
	codexCmd.Flags().IntVar(&cli.approvalPort, "approval-port", 0,
		"Decide from another terminal or a script through the local approval API on this port (0 = in this terminal)")
	codexCmd.Flags().StringVar(&cli.approvalToken, "approval-token", "", "Token for the approval API (default: a new one, printed)")
	rootCmd.AddCommand(codexCmd)
}

// claudeSession is `staircase claude <task>`.
func claudeSession(_ *cobra.Command, args []string) error {
	return session(cli, "claude-code", "Claude Code", "claude", args)
}

// codexSession is `staircase codex <task>`.
func codexSession(_ *cobra.Command, args []string) error {
	return session(cli, "codex", "Codex", "codex", args)
}

// geminiSession is `staircase gemini <task>`.
func geminiSession(_ *cobra.Command, args []string) error {
	return session(cli, "gemini", "Gemini CLI", "gemini", args)
}

// opencodeSession is `staircase opencode <task>`.
func opencodeSession(_ *cobra.Command, args []string) error {
	return session(cli, "opencode", "OpenCode", "opencode", args)
}

// sessionFlags are the flags of a session command for an agent: what it may
// change, whether it may ask for commands, how to decide, checks, signing,
// reviewers.
func sessionFlags(cmd *cobra.Command, agentName string, shell bool) {
	cmd.Flags().StringArrayVar(&cli.allow, "allow", nil, "A path (glob) the task may change; repeat for more")
	if shell {
		cmd.Flags().BoolVar(&cli.terms.AllowShellExec, "allow-shell-exec", false,
			"Let "+agentName+" propose shell commands (each still needs your approval)")
	}
	cmd.Flags().IntVar(&cli.approvalPort, "approval-port", 0,
		"Decide from another terminal, a script or your browser through the local approval API on this port (0 = in this terminal)")
	cmd.Flags().StringVar(&cli.approvalToken, "approval-token", "", "Token for the approval API (default: a new one, printed)")
	cmd.Flags().StringVar(&cli.model, "model", "", "Model for "+agentName+" (default: its own)")
	if agentName == "Gemini CLI" {
		cmd.Flags().StringArrayVar(&cli.passEnv, "pass-env", nil,
			"Name of an environment variable Gemini may inherit, to give it its own credential (for example GEMINI_API_KEY); it, and any command it runs, can read the value. Repeat for more")
	}
	cmd.Flags().BoolVarP(&cli.yes, "yes", "y", false, "Start without asking to confirm the task (needed without a terminal)")
	checkFlag(cmd)
	signFlags(cmd)
	validatorFlag(cmd)
	inScopeFlag(cmd)
	awayFlag(cmd)
	sandboxFlag(cmd)
	changeSetFlag(cmd)
	rootCmd.AddCommand(cmd)
}

// session is a governed run of harness in the current repository, with
// nothing to set up first.
func session(l launch, harness, name, command string, args []string) error {
	task := strings.TrimSpace(strings.Join(args, " "))
	if task == "" {
		return fmt.Errorf(`what should %s do? For example: staircase %s "add a /health endpoint"`, name, command)
	}
	if err := checkChangeSet(&l, viper.GetString("STAIRCASE_DIR")); err != nil {
		return err
	}
	// What is true of this repository's project for every run of it fills what the command line left out (when the workspace has the
	// project already: nothing is made yet).
	if root, err := gitTopLevel(); err == nil {
		if err := profileForRoot(&l, viper.GetString("STAIRCASE_DIR"), root); err != nil {
			return err
		}
	}
	// The terms are judged before anything is asked or made. Who agrees to the task is asked below, so it stands in here.
	t := l.runTerms()
	t.Agreed = "the person asked below"
	t, err := orchestrator.NewTerms(t, harness)
	if err != nil {
		return err
	}
	l.terms.ApproveInScope, l.terms.ApproveOnEvidence = t.ApproveInScope, t.ApproveOnEvidence
	if l.terms.ApproveInScope && len(l.allow) == 0 {
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
		var snap *policy.Snapshot // the rules a policy delivery would rest on, shown to the person who arms it
		if t.Deliver == orchestrator.DeliverPolicy {
			if snap, err = policy.LoadSnapshot(wsDir); err != nil {
				return 0, fmt.Errorf("load policy: %w", err)
			}
		} else {
			snap = &policy.Snapshot{}
		}
		if l.terms.Agreed, err = agree(sessionSetup{deliver: t.Deliver, deliverPaths: t.DeliverPaths, policyDigest: snap.Digest, policySigned: snap.Signed,
			harness: harness, name: name, task: task, root: root,
			base: strings.TrimSpace(string(base)), allow: l.allow, checks: l.terms.Checks, protected: t.ProtectedPaths, inScope: l.terms.ApproveInScope, onEvidence: l.terms.ApproveOnEvidence, away: l.terms.Away, validators: len(l.terms.ValidatorModels), signed: l.terms.SignKey != "", requireSigned: l.terms.RequireSignedApprovals, model: l.model,
			shell: l.terms.AllowShellExec, yes: l.yes, budget: budget, projectID: project.ID}); err != nil {
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
		if len(l.allow) > 0 {
			scope, _ := json.Marshal(map[string]any{"allow": l.allow})
			if err := store.SetUserStoryScope(story.ID, string(scope)); err != nil {
				return 0, err
			}
		}
		fmt.Printf("🧭 Project %q (#%d), case #%d, story #%d\n", project.Name, project.ID, c.ID, story.ID)
		if _, err := compileCase(store, wsDir, c.ID, harness, l.review, true); err != nil {
			return 0, err
		}
		return c.ID, nil
	}()
	if err != nil {
		return err
	}
	if err := runCase(l, caseID); err != nil {
		return err
	}
	branch := "staircase/run-<id>"
	if db, err := persistence.InitDB(wsDir); err == nil {
		if last := lastRunOfCase(persistence.NewStore(db), caseID); last != nil {
			branch = fmt.Sprintf("staircase/run-%d", last.ID)
			if last.Status == persistence.RunStatusAwaitingReview {
				_ = db.Close()
				fmt.Printf("\nNothing is committed yet: the change waits for your review. Look at it and decide with:\n  staircase decide %d\n", last.ID)
				return nil
			}
		}
		_ = db.Close()
	}
	if l.finish == nil {
		return defaultFinish(root, branch, storyID)
	}
	return l.finish(root, branch, storyID)
}

// lastRunOfCase is the case's most recent run, or nil when it has none or they cannot be read. ListRunsByCase is newest first.
func lastRunOfCase(store *persistence.Store, caseID int64) *domain.Run {
	runs, err := store.ListRunsByCase(caseID)
	if err != nil || len(runs) == 0 {
		return nil
	}
	return &runs[0]
}

// defaultFinish tells what to do with a session's result; staircase seal
// gives its own.
func defaultFinish(root, branch string, storyID int64) error {
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
	allow, checks, protected               []string
	inScope, onEvidence, away              bool
	validators                             int
	signed, requireSigned                  bool
	shell                                  bool
	yes                                    bool // start without asking
	budget                                 float64
	projectID                              int64
	// delivery by policy (ADR 0012): the mode ("" or "review" is the default), the delivery scope, and the digest of the policy whose
	// rules would decide, with whether it is signed
	deliver      string
	deliverPaths []string
	policyDigest string
	policySigned bool
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
	case s.away:
		// what both away runs say; they differ in what follows the checks
		fmt.Fprintf(&b, "  away:        nobody is asked. Inside the scope a change is approved only when %s;\n"+
			"               anything that needs you (no evidence, outside the scope, a command, a sensitive file) is parked\n"+
			"               for you and the agent goes on. When it stops, the checks run on the whole change", evidenceSources(s))
		if s.deliver != orchestrator.DeliverPolicy {
			fmt.Fprintf(&b, ", and it waits\n               for your review: staircase decide. Nothing is committed before you accept it\n")
			break
		}
		fmt.Fprintf(&b, "\n  delivery:    BY POLICY: no person reviews the whole change. When the checks pass on exactly what rules of your signed\n"+
			"               policy approved, inside the delivery scope, it is committed to the run branch without your review;\n"+
			"               nothing is pushed. One parked proposal, a change approved on evidence alone, or a sensitive or\n"+
			"               protected path sends the whole run to your review instead (staircase decide)\n")
		fmt.Fprintf(&b, "  delivers:    only under %s (the delivery scope)\n", strings.Join(s.deliverPaths, ", "))
		switch {
		case s.policySigned:
			fmt.Fprintf(&b, "  policy:      %s (signed): its rules decide\n", s.policyDigest)
		case s.policyDigest != "":
			fmt.Fprintf(&b, "  policy:      %s (NOT signed: the run will be refused until you sign it: staircase policy sign)\n", s.policyDigest)
		default:
			fmt.Fprintf(&b, "  policy:      none (NOT signed: the run will be refused until you write one and sign it: staircase policy sign)\n")
		}
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
	if len(s.protected) > 0 {
		fmt.Fprintf(&b, "  protected:   %s (where your checks are defined: a change to them always comes to you, parked while away)\n",
			strings.Join(s.protected, ", "))
	} else if len(s.checks) > 0 {
		fmt.Fprintf(&b, "  protected:   nothing: your checks name no file of their own, so no file of your checks is protected and the agent may edit\n"+
			"               the tests they run (name them with --protect <path or pattern>)\n")
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
	if s.yes {
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

// awayFlag adds --away to cmd (ADR 0006).
func awayFlag(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&cli.terms.Away, "away", false,
		"Work while you are away: nobody is asked; a change is approved only when your --check commands pass on it, anything that "+
			"needs you is parked, and the run ends waiting for your review (staircase decide). Needs --check and --allow")
	cmd.Flags().StringArrayVar(&cli.terms.AwayTools, "away-tool", nil,
		"An exact MCP tool (mcp__<server>__<tool>) an away run may call without asking (repeat for more); any other tool call is parked")
	cmd.Flags().StringArrayVar(&cli.terms.AwayCommands, "away-command", nil,
		"An exact command an away run may run without asking (repeat for more): your decision made in advance. It needs --away, "+
			"--allow-shell-exec and a sandbox without network (--sandbox docker:image=... or required); it runs only as written, in the worktree's root")
	cmd.Flags().StringVar(&cli.terms.Deliver, "deliver", orchestrator.DeliverReview,
		"How an away run's approved change reaches its branch: review (it waits for your review: staircase decide) or policy "+
			"(it is committed to the run branch, with no review, when only rules of your signed policy approved it and your checks pass; "+
			"no person reviews the whole change). Policy needs --away, --check, --deliver-path, --sign-approvals, a signed policy "+
			"(staircase policy sign), and a --protect path or checks that name a file of their own; it never pushes")
	cmd.Flags().StringArrayVar(&cli.terms.DeliverPaths, "deliver-path", nil,
		"A path or pattern (from the repository's root) where --deliver policy may deliver, such as docs/**: a change anywhere else waits "+
			"for your review. Repeat for more")
}

// inScopeFlag adds --approve-in-scope and --approve-on-evidence to cmd.
func inScopeFlag(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&cli.terms.ApproveOnEvidence, "approve-on-evidence", false,
		"Approve changes inside the --allow scope only when the evidence is there: your --check commands pass on the state the change produces, "+
			"and the --validator models agree. Otherwise the change comes to you with what was missing. Replaces the 1-in-5 sampling; needs --check and/or --validator")
	cmd.Flags().BoolVar(&cli.terms.ApproveInScope, "approve-in-scope", false,
		"Approve changes inside the --allow scope as part of the agreed task instead of one by one: "+
			"1 in 5, sensitive files and anything outside still come to you, and you approve the whole change at the end")
}
