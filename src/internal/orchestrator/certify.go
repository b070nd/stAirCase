package orchestrator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/plan"
)

// harnessNames are the harnesses as Assisted-by trailers name them.
var harnessNames = map[string]string{"claude-code": "Claude Code", "codex": "Codex"}

// assistants names who helped make a run's change, for the Assisted-by
// trailer and the certificate: the harness, or the models of the built-in
// agents.
func assistants(pl *plan.Plan) []string {
	switch {
	case pl == nil:
		return []string{"stAirCase"}
	case pl.Harness == "review" && pl.Review != nil:
		return []string{pl.Review.By}
	case pl.Harness != "":
		if n, ok := harnessNames[pl.Harness]; ok {
			return []string{n}
		}
		return []string{pl.Harness}
	}
	var models []string
	for _, a := range pl.Agents {
		if !slices.Contains(models, a.Model) {
			models = append(models, a.Model)
		}
	}
	slices.Sort(models)
	return []string{"stAirCase (" + strings.Join(models, ", ") + ")"}
}

// commitMessage is the run commit's message: what it is, which agents helped
// (the Assisted-by trailer many projects ask for), and the audit chain's last
// hash when the commit was made.
func commitMessage(runID, caseID int64, pl *plan.Plan, chainHead string) string {
	var b strings.Builder
	if pl != nil && pl.Review != nil && pl.Review.Message != "" {
		fmt.Fprintf(&b, "%s\n\nstaircase: run #%d - case #%d\n\n", strings.TrimSpace(pl.Review.Message), runID, caseID)
	} else {
		fmt.Fprintf(&b, "staircase: run #%d - case #%d\n\n", runID, caseID)
	}
	for _, a := range assistants(pl) {
		fmt.Fprintf(&b, "Assisted-by: %s\n", a)
	}
	fmt.Fprintf(&b, "Staircase-Chain: sha256:%s\n", chainHead)
	return b.String()
}

// A quick approval: a change of at least quickLines lines approved by a
// person in under quickMS milliseconds.
const (
	quickLines = 20
	quickMS    = 5000
)

// certify signs a change certificate about commit with the workspace key,
// writes it to audit/run-<id>.certificate.json and attaches it to the commit
// as a git note (refs/notes/staircase). Without a signing key it only says
// so: the certificate is evidence, not a gate.
func (r *Runner) certify(runID int64, commit, baseSHA, chainHead string, pl *plan.Plan, repo *GitRepo, checks []certificate.Check) error {
	priv, err := crypto.LoadSigningKey(r.wsDir)
	if err != nil {
		fmt.Fprintf(os.Stdout, "   ⚠️  No change certificate: %v\n", err)
		return nil
	}
	events, err := r.store.ListEventLogs(runID)
	if err != nil {
		return err
	}
	p := certificate.Predicate{Run: runID, BaseCommit: baseSHA, Agents: assistants(pl), ChainHead: chainHead,
		Decisions: map[string]int{}, CAL: 3, Checks: checks}
	if pl != nil {
		p.PlanDigest, p.Blueprint = pl.Digest, pl.BlueprintHash
	}
	if out, err := exec.Command("git", "-C", repo.path, "config", "user.email").Output(); err == nil {
		p.RequestedBy = strings.TrimSpace(string(out))
	}
	shells, sandboxed, after := 0, 0, false
	var decideMS []int64 // people's decisions
	quick := 0
	for _, e := range events {
		var d struct {
			Source      string `json:"source"`
			ActionType  string `json:"action_type"`
			Approved    bool   `json:"approved"`
			ReviewAfter bool   `json:"review_after"`
			Sandboxed   bool   `json:"sandboxed"`
			DecideMS    *int64 `json:"decide_ms"`
			Lines       int    `json:"lines"`
		}
		if json.Unmarshal([]byte(e.Payload), &d) != nil {
			continue
		}
		switch e.EventType {
		case "shell_ran":
			if d.Sandboxed {
				sandboxed++
			}
		case "yield_decided":
			p.Decisions[d.Source]++
			if d.Approved && d.ActionType == domain.ActionShellExec {
				shells++
			}
			if d.Source == "operator" && d.DecideMS != nil {
				decideMS = append(decideMS, *d.DecideMS)
				// ponytail: a fixed bar for "approved without reading"; tune once teams report numbers.
				if d.Approved && d.Lines >= quickLines && *d.DecideMS < quickMS {
					quick++
				}
			}
			// a sandboxed command was decided before it ran; what it wrote is decided too
			after = after || (d.Approved && d.ReviewAfter && !d.Sandboxed)
		}
	}
	if len(decideMS) > 0 {
		slices.Sort(decideMS)
		p.Attention = &certificate.Attention{HumanDecisions: len(decideMS), QuickApprovals: quick,
			MedianSeconds: float64(decideMS[len(decideMS)/2]) / 1000}
	}
	if shells > sandboxed { // ADR 0001: CAL 3 runs no approved command outside a sandbox
		p.CAL = 2
		p.Notes = append(p.Notes, "shell commands were approved and ran without a sandbox")
	}
	if after { // ADR 0001: CAL 3 decides every action before it runs
		p.CAL = 2
		p.Notes = append(p.Notes, "commands changed files that were reviewed after the fact")
	}

	env, err := certificate.Sign(certificate.New(commit, p), priv)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Join(r.wsDir, "audit")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, fmt.Sprintf("run-%d.certificate.json", runID))
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return err
	}
	name, email := repo.identity()
	cmd := exec.Command("git", "-C", repo.path, "-c", "core.hooksPath=/dev/null",
		"notes", "--ref=staircase", "add", "-f", "-F", path, commit)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME="+name, "GIT_AUTHOR_EMAIL="+email,
		"GIT_COMMITTER_NAME="+name, "GIT_COMMITTER_EMAIL="+email)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("attach the change certificate to %s: %w: %s", commit, err, bytes.TrimSpace(stderr.Bytes()))
	}
	fmt.Fprintf(os.Stdout, "   📜 Change certificate (CAL %d): %s, also in git notes (refs/notes/staircase)\n", p.CAL, path)
	return r.audit(runID, "certificate_issued", map[string]any{"commit": commit, "cal": p.CAL,
		"envelope_sha256": sha256Hex(b)})
}
