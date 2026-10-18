package orchestrator

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

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
}

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
	return t, t.checkDeclared(harness)
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
