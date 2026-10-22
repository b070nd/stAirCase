package orchestrator

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stAirCase/src/internal/engine"
	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/sandbox"
)

// Terms are what a run decides under, set when it starts and kept by every continuation: who agreed to the task, how changes
// are approved, where commands and checks run, the checks, what signs, and what an away run may do without asking. They are
// plain data, saved as the run's options.json and bound on run_bound by digest (ADR 0010, Q22). Where the run listens, how it
// is shown and the agent that carries it out are not terms (RunOptions).
type Terms struct {
	// Agreed says who agreed to the task before the run (a session's confirmation).
	Agreed string `json:"agreed,omitempty"`
	// ApproveInScope approves changes inside the agreed task's scope as part of the task (source "task"), with a checkpoint
	// every few changes and a person's final review of the whole change.
	ApproveInScope bool `json:"approve_in_scope,omitempty"`
	// ApproveOnEvidence approves in-scope changes on evidence instead of on the agreed task alone: the Checks must pass on the
	// state each change would produce, and the reviewer models, if any, must agree; otherwise a person decides.
	ApproveOnEvidence bool `json:"approve_on_evidence,omitempty"`
	// AllowShellExec lets the agent propose shell commands; off unless asked for.
	AllowShellExec bool `json:"allow_shell_exec,omitempty"`
	// Sandbox is where approved commands and checks run: auto (also when empty), required, off or docker:...
	Sandbox                string `json:"sandbox,omitempty"`
	RequireSignedApprovals bool   `json:"require_signed_approvals,omitempty"`
	// SignKey, when set, signs each human decision with this SSH key (a path, never a key), as SignAs.
	SignKey string `json:"sign_key,omitempty"`
	SignAs  string `json:"sign_as,omitempty"`
	// RequireEvidence makes a commit without its ledger and signed certificate a failure.
	RequireEvidence bool `json:"require_evidence,omitempty"`
	// Checks are commands, such as the tests, run as evidence; CheckTimeout bounds each (0: 15 minutes).
	Checks       []string      `json:"checks,omitempty"`
	CheckTimeout time.Duration `json:"check_timeout_ns,omitempty"`
	// SignalModel (at SignalURL, when set) is a decision model asked about every change approved without a person.
	SignalModel string `json:"signal_model,omitempty"`
	SignalURL   string `json:"signal_url,omitempty"`
	// ValidatorModels decide in-scope file edits the policy leaves open; a person approves the whole change once.
	ValidatorModels []string `json:"validator_models,omitempty"`
	// Away (ADR 0006): nobody is asked; changes are approved on evidence or by policy, the rest is parked.
	Away bool `json:"away,omitempty"`
	// AwayCommands (ADR 0009) and AwayTools (ADR 0008) are what an away run may run or call without asking.
	AwayCommands []string `json:"away_commands,omitempty"`
	AwayTools    []string `json:"away_tools,omitempty"`
	// ProtectedPaths hold the checks' definitions (paths or ** patterns, from the repository's root): a change to one always
	// needs a person, whatever the policy says, and is parked while away. Those the checks name are added (checkDefinitions).
	ProtectedPaths []string `json:"protected_paths,omitempty"`
	// Scope narrows the plan's scope for this run: a change outside these paths (or ** patterns, from the repository's root) comes to a
	// person as drift, whatever the stories allow (--allow on a run, or the project's profile).
	Scope []string `json:"scope,omitempty"`
	// SensitivePaths are the project's own sensitive paths, added to the built-in list: a change to one always needs a person,
	// whatever the policy says. They can only add to the list (the project's profile).
	SensitivePaths []string `json:"sensitive_paths,omitempty"`
	// ProfileSHA256 is the digest of the project's profile the run started under, "" when the project had none: what the run was told
	// about its project is on the record.
	ProfileSHA256 string `json:"profile_sha256,omitempty"`
	// Instructions are the project's, given to the agent before its task, whichever agent it is (the project's profile). Text: bound
	// by the terms' digest, saved with them, so a continuation tells its agent the same words.
	Instructions string `json:"instructions,omitempty"`
	// Visible is the run's environment: directories (or files) of the user's checkout, relative to its root, that the repository
	// ignores and the checks need (composer's vendor/, generated code). Checks see them, read-only, in the checkout of the commit they
	// run in. They are never in the agent's worktree and never part of what is approved.
	Visible []string `json:"visible,omitempty"`
	// Deliver is how the run's approved change reaches its branch (ADR 0012): DeliverReview (also when empty, and then absent
	// from the saved form, so every run saved before the field existed reads as review) offers it to a person; DeliverPolicy
	// delivers it, while nobody is there, when only rules of the owner's signed policy approved it.
	Deliver string `json:"deliver,omitempty"`
	// DeliverPaths are the delivery scope: the paths (or ** patterns, from the repository's root) a policy delivery may deliver.
	DeliverPaths []string `json:"deliver_paths,omitempty"`
}

// The modes of Terms.Deliver.
const (
	DeliverReview = "review"
	DeliverPolicy = "policy"
)

// termsVersion is the version of the saved form of Terms (options.json).
const termsVersion = 1

// savedTerms is the saved form: the terms and their version.
type savedTerms struct {
	Version int `json:"version"`
	Terms
}

// loadTerms reads the terms run runID saved when it started. It refuses bytes the chain did not bind (a run bound before the
// digest was recorded is read as before), another version, and terms no run can decide under. No saved terms is os.ErrNotExist.
func loadTerms(wsDir string, runID int64, h history.History, harness string) (Terms, error) {
	b, err := os.ReadFile(savedOptionsPath(wsDir, runID))
	if err != nil {
		return Terms{}, err
	}
	if !termsBound(h, b) {
		return Terms{}, errors.New("the options it saved are not the ones the audit chain names")
	}
	var s savedTerms
	if json.Unmarshal(b, &s) != nil || s.Version != termsVersion {
		return Terms{}, errors.New("the options it saved are unreadable")
	}
	return NewTerms(s.Terms, harness)
}

// NewTerms normalizes t and refuses what no run can decide under, judged from the terms alone and the harness that carries
// the run out ("" for the built-in agent). Away is approving on evidence, which is approving the agreed task.
func NewTerms(t Terms, harness string) (Terms, error) {
	if t.Away {
		if len(t.Checks) == 0 {
			return t, errors.New("--away needs at least one --check: while you are away, a change is approved only when your checks pass on it")
		}
		if t.RequireSignedApprovals { // the review in staircase decide is not signed yet: refused rather than weaker
			return t, errors.New("--away cannot be combined with signed decisions yet: the review of an away run is not signed")
		}
		t.ApproveOnEvidence = true
	}
	if t.ApproveOnEvidence {
		if len(t.Checks) == 0 && len(t.ValidatorModels) == 0 {
			return t, errors.New("--approve-on-evidence needs evidence to decide on: name a --check (your tests) and/or a --validator")
		}
		t.ApproveInScope = true
	}
	if t.ApproveInScope && t.Agreed == "" {
		return t, errors.New("--approve-in-scope and --approve-on-evidence approve an agreed task: nobody agreed to one")
	}
	if err := sandbox.ParseMode(orAuto(t.Sandbox)); err != nil {
		return t, fmt.Errorf("--sandbox: %w", err)
	}
	if err := t.protect(); err != nil {
		return t, err
	}
	var err error
	if t.Scope, err = cleanPaths("--allow", t.Scope); err != nil {
		return t, err
	}
	if t.SensitivePaths, err = cleanPaths("the profile's sensitive paths", t.SensitivePaths); err != nil {
		return t, err
	}
	if t.Visible, err = visiblePaths(t.Visible); err != nil {
		return t, err
	}
	if len(t.Instructions) > maxInstructions {
		return t, fmt.Errorf("the project's instructions are %d bytes and an agent is told at most %d", len(t.Instructions), maxInstructions)
	}
	if err := t.deliver(); err != nil {
		return t, err
	}
	return t, t.checkDeclared(harness)
}

// deliver judges the delivery mode (ADR 0012). Review is the default and is never saved. Policy delivery is unattended, so it
// is refused unless everything it rests on is in the terms: an away run (nobody to ask), no reviewer model (a model's approval
// is never a rule's), a delivery scope, the initiator's signature (the certificate names who armed it), and a complete
// declaration of the protected paths (after protect: the checks name a file of their own, or --protect named some). Signed
// decisions are refused with --away already. Whether the policy was signed is known only when the run starts (firstSegment.policy).
func (t *Terms) deliver() error {
	switch t.Deliver {
	case "", DeliverReview:
		t.Deliver = ""
		if len(t.DeliverPaths) > 0 {
			return errors.New("--deliver-path names where delivery by policy may deliver: add --deliver policy")
		}
		return nil
	case DeliverPolicy:
	default:
		return fmt.Errorf("--deliver %q: review or policy", t.Deliver)
	}
	switch {
	case !t.Away:
		return errors.New("--deliver policy needs --away: it delivers while nobody is there to ask")
	case len(t.ValidatorModels) > 0:
		return errors.New("--deliver policy cannot be combined with --validator: only a rule of your policy may approve what is delivered, never a model")
	case len(t.DeliverPaths) == 0:
		return errors.New("--deliver policy needs --deliver-path: name where it may deliver (a path or pattern, from the repository's root)")
	case t.SignKey == "":
		return errors.New("--deliver policy needs --sign-approvals <key>: the certificate names who armed delivery without review")
	case len(t.ProtectedPaths) == 0:
		return errors.New("--deliver policy needs protected paths: your checks name no file of their own, so name where they are defined with --protect <path or pattern> (for go test ./... : --protect \"**/*_test.go\")")
	}
	var paths []string
	for _, p := range t.DeliverPaths {
		clean, ok := repoPath(p)
		if !ok {
			return fmt.Errorf("--deliver-path %q: name a path or pattern inside the repository, from its root", p)
		}
		paths = append(paths, clean)
	}
	slices.Sort(paths)
	t.DeliverPaths = slices.Compact(paths)
	return nil
}

// protect makes ProtectedPaths the declared paths, cleaned, and every path the checks name as their definition, sorted once.
func (t *Terms) protect() error {
	var paths []string
	for _, p := range t.ProtectedPaths {
		clean, ok := repoPath(p)
		if !ok {
			return fmt.Errorf("--protect %q: name a path or pattern inside the repository, from its root", p)
		}
		paths = append(paths, clean)
	}
	for _, c := range t.Checks {
		paths = append(paths, checkDefinitions(c)...)
	}
	slices.Sort(paths)
	t.ProtectedPaths = slices.Compact(paths)
	return nil
}

// maxInstructions is the most a project may tell its agents (as the profile's limit).
const maxInstructions = 32 << 10

// visiblePaths is the declared environment cleaned, sorted and without duplicates: literal paths inside the repository, never a pattern
// and never the git directory.
func visiblePaths(paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		clean, ok := repoPath(p)
		if !ok {
			return nil, fmt.Errorf("--visible %q: name a path inside the repository, from its root", p)
		}
		if strings.ContainsAny(clean, "*?[") {
			return nil, fmt.Errorf("--visible %q names a directory or a file, not a pattern", p)
		}
		if clean == ".git" || strings.HasPrefix(clean, ".git/") {
			return nil, fmt.Errorf("--visible %q: the git directory is not an environment", p)
		}
		out = append(out, clean)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// cleanPaths is paths cleaned, sorted and without duplicates, refusing one that is outside the repository or not a well formed pattern.
func cleanPaths(what string, paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		clean, ok := repoPath(p)
		if !ok {
			return nil, fmt.Errorf("%s %q: name a path or pattern inside the repository, from its root", what, p)
		}
		if !engine.ValidGlob(clean) {
			return nil, fmt.Errorf("%s %q is not a well formed pattern", what, p)
		}
		out = append(out, clean)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// repoPath is p cleaned, if it names something inside the repository from its root.
func repoPath(p string) (string, bool) { return engine.CleanRepoPath(p) }

// checkDefinitions are the paths a check names as its definition, where that can be known: the script it runs, the files a
// test runner is given, a build tool's file. What it only looks at (test -f x) or finds by itself (go test ./...) is not known.
// After a cd nothing more is known: the paths are no longer from the root.
func checkDefinitions(command string) []string {
	var out []string
	add := func(tok string, dir bool) {
		if p, ok := repoPath(strings.Trim(tok, `'"`)); ok && !strings.ContainsAny(p, "*?[{$`") {
			out = append(out, p)
			if dir && path.Ext(p) == "" {
				out = append(out, p+"/**")
			}
		}
	}
	for _, segment := range strings.FieldsFunc(command, func(r rune) bool { return strings.ContainsRune(";&|\n", r) }) {
		words := strings.Fields(segment)
		for len(words) > 0 && strings.Contains(words[0], "=") { // FOO=1 before the command
			words = words[1:]
		}
		if len(words) == 0 {
			continue
		}
		prog, args := strings.Trim(words[0], `'"`), words[1:]
		if strings.Contains(prog, "/") {
			add(prog, false)
		}
		switch path.Base(prog) {
		case "cd", "pushd":
			return out
		case "make", "gmake":
			out = append(out, "Makefile", "GNUmakefile")
		case "just":
			out = append(out, "justfile")
		case "npm", "yarn", "pnpm":
			out = append(out, "package.json")
		case "sh", "bash", "zsh", "dash", "python", "python3", "node", "ruby", "perl": // the first argument not a flag is the script
			for _, a := range args {
				if a == "-c" || a == "-e" || a == "-m" { // code given inline, or a module found by name
					break
				}
				if !strings.HasPrefix(a, "-") {
					add(a, false)
					break
				}
			}
		case "bats", "pytest": // every argument not a flag is a test file or directory
			for _, a := range args {
				if !strings.HasPrefix(a, "-") {
					add(a, true)
				}
			}
		}
	}
	return out
}

func orAuto(mode string) string {
	if mode == "" {
		return sandbox.Auto
	}
	return mode
}

// checkDeclared refuses a declaration of tools or commands (ADR 0008, 0009) that is not an away run's, of a command that cannot
// run in a sandbox without network, or of a command that is not a whole command: no composition, redirection, substitution or
// second line.
func (t Terms) checkDeclared(harness string) error {
	for _, tool := range t.AwayTools {
		if !t.Away {
			return errors.New("--away-tool declares tools for an away run: add --away")
		}
		if !strings.HasPrefix(tool, "mcp__") || strings.ContainsAny(tool, " *?") {
			return fmt.Errorf("--away-tool %q: name one MCP tool exactly, as mcp__<server>__<tool>", tool)
		}
	}
	if len(t.AwayCommands) == 0 {
		return nil
	}
	switch {
	case !t.Away:
		return errors.New("--away-command declares commands for an away run: add --away")
	case !t.AllowShellExec:
		return errors.New("--away-command needs commands to be allowed at all: add --allow-shell-exec")
	case !noNetworkMode(t.Sandbox):
		return errors.New("--away-command needs a sandbox without network: --sandbox docker:image=<image>, or --sandbox required where sandbox-exec or bubblewrap runs")
	case harness == "gemini" || harness == "opencode":
		return fmt.Errorf("--away-command: %s runs its commands without a sandbox", harness)
	}
	for _, c := range t.AwayCommands {
		if strings.TrimSpace(c) != c || c == "" || strings.ContainsAny(c, ";&|<>`$\n\r") {
			return fmt.Errorf("--away-command %q: a declared command must be the whole command, without ; & | < > ` $ or a second line", c)
		}
	}
	return nil
}
