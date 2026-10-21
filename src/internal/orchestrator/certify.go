package orchestrator

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/plan"
	"github.com/b070nd/stAirCase/src/internal/wslock"
)

// harnessNames are the harnesses as Assisted-by trailers name them.
var harnessNames = map[string]string{"claude-code": "Claude Code", "codex": "Codex", "gemini": "Gemini CLI", "opencode": "OpenCode"}

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

// evidence is what a run adds to its certificate besides its decisions.
type evidence struct {
	checks         []certificate.Check
	ledger, policy string // SHA-256 digests
	initiator      *certificate.Initiator
	recovered      bool // the run was interrupted and its commit made by `recover`
	awayReviewed   bool // an away run: approved on evidence while nobody watched, its whole change accepted afterwards (ADR 0006)
	byPolicy       bool // an away run that delivered by policy: only rules of the signed policy approved, and no person reviewed the whole change (ADR 0012)
}

// certify signs a change certificate about commit with the workspace key,
// writes it to audit/run-<id>.certificate.json and attaches it to the commit
// as a git note (refs/notes/staircase). Without a signing key it only says
// so: the certificate is evidence, not a gate.
func (r *Runner) certify(runID int64, commit, baseSHA, chainHead string, pl *plan.Plan, repo *GitRepo, ev evidence) error {
	priv, err := crypto.LoadSigningKey(r.wsDir)
	if err != nil {
		fmt.Fprintf(os.Stdout, "   ⚠️  No change certificate: %v\n", err)
		return fmt.Errorf("no signing key: %w", err)
	}
	// The decisions are counted from the chain, so it is read only as verified: a changed row gets no certificate.
	hist, err := history.Read(r.store, runID)
	if err != nil {
		return fmt.Errorf("no certificate is signed over it: %w", err)
	}
	p := certificate.Predicate{Run: runID, BaseCommit: baseSHA, Agents: assistants(pl), ChainHead: chainHead,
		Decisions: map[string]int{}, CAL: 3, Checks: ev.checks, Ledger: ev.ledger, Policy: ev.policy, Initiator: ev.initiator}
	if pl != nil {
		p.PlanDigest, p.Blueprint = pl.Digest, pl.BlueprintHash
	}
	if out, err := exec.Command("git", "-C", repo.path, "config", "user.email").Output(); err == nil {
		p.RequestedBy = strings.TrimSpace(string(out))
	}
	shells, after, tools := 0, false, 0
	var decideMS []int64 // people's decisions
	quick := 0
	signed := certificate.SignedApprovals{}
	for _, d := range hist.Decisions {
		p.Decisions[d.Source]++
		if d.Approved && d.ActionType == domain.ActionShellExec {
			shells++
		}
		if d.Approved && d.ActionType == domain.ActionToolCall {
			tools++
		}
		if d.Source == "operator" && d.Signature != "" {
			signed.Decisions++
			if d.Trusted != nil && *d.Trusted && !slices.Contains(signed.Signers, d.SignedBy) {
				signed.Signers = append(signed.Signers, d.SignedBy)
			}
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
	sandboxed := hist.Shells.Sandboxed
	if signed.Decisions > 0 {
		slices.Sort(signed.Signers)
		p.Signed = &signed
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
	if tools > 0 { // an MCP tool was decided before it ran, but it ran outside stAirCase's sandbox (ADR 0008)
		p.CAL = min(p.CAL, 2)
		p.Notes = append(p.Notes, fmt.Sprintf("%d call(s) of MCP tools were approved; they ran outside stAirCase's sandbox", tools))
	}
	if after { // ADR 0001: CAL 3 decides every action before it runs
		p.CAL = 2
		p.Notes = append(p.Notes, "commands changed files that were reviewed after the fact")
	}

	if ev.awayReviewed {
		p.Notes = append(p.Notes, "made while the owner was away: changes were approved on evidence or by policy, what needed a person was parked, and the owner accepted the whole change after the run ended (staircase decide)")
	}
	if ev.byPolicy { // the level stays what it computes: a later person's signature can still lift it
		p.Delivery = "policy"
		p.Notes = append(p.Notes, fmt.Sprintf("made while the owner was away and delivered by policy: every approved change was approved by a rule of the owner's signed policy (%s), none by a person, by the agreed task, by a reviewer model or on evidence alone; the checks passed on the exact tree; no person reviewed the whole change; only the run branch moved and nothing was pushed", ev.policy))
	}
	if ev.recovered { // the end of the run (worktree check, final checks) did not happen
		p.CAL = min(p.CAL, 2)
		p.Notes = append(p.Notes, "the run was interrupted and its approved changes recovered afterwards: the agent's end, the check of the worktree and the definition-of-done checks did not happen")
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
	if err := attachNote(repo, NotesRef, path, commit); err != nil {
		return fmt.Errorf("attach the change certificate to %s: %w", commit, err)
	}
	fmt.Fprintf(os.Stdout, "   📜 Change certificate (CAL %d): %s, also in git notes (refs/notes/staircase)\n", p.CAL, path)
	for _, d := range hist.Delivered { // signing again gives the same certificate: it is on the chain once
		if d.Event == "certificate_issued" && d.EnvelopeSHA256 == sha256Hex(b) {
			return nil
		}
	}
	return r.audit(runID, "certificate_issued", map[string]any{"commit": commit, "cal": p.CAL,
		"envelope_sha256": sha256Hex(b)})
}

// NotesRef and LedgerNotesRef are the git notes a run attaches to its commit:
// the signed certificate, and the ledger it names.
const (
	NotesRef       = "staircase"
	LedgerNotesRef = "staircase-ledger"
)

// noteAttempts bounds how often a note is written again when reading it back
// does not find it.
const noteAttempts = 5

// AttachNote is attachNote for a caller outside a run (staircase sign), given the path of the repository: a signature is
// attached as safely as the certificate it signs.
func AttachNote(repoPath, ref, file, commit string) error {
	repo, err := OpenGitRepo(repoPath)
	if err != nil {
		return err
	}
	return attachNote(repo, ref, file, commit)
}

// attachNote adds the file's content, byte for byte, as a note under
// refs/notes/<ref> on commit. Git loses notes that are added at the same moment
// (without a word: six parallel adds lost notes in 8 of 8 trials), so the write
// holds a lock on the repository, covering every notes ref, reads the note back
// and compares it with the file, and writes it again up to noteAttempts times.
// A note that cannot be confirmed is an error, never a success: a certificate
// that is not there fails a verifier later.
func attachNote(repo *GitRepo, ref, file, commit string) error {
	unlock, err := lockNotes(repo)
	if err != nil {
		return err
	}
	defer unlock()
	// The note is the blob of the file itself: git's own message cleanup (trailing
	// blanks, line ends) would change bytes that a verifier compares.
	want, err := repo.gitNotes("hash-object", "-w", file)
	if err != nil {
		return fmt.Errorf("store the note: %w", err)
	}
	var last error
	for attempt := 1; attempt <= noteAttempts; attempt++ {
		last = errors.New("it was written but is not there")
		if _, err := repo.gitNotes("notes", "--ref="+ref, "add", "-f", "-C", want, commit); err != nil {
			last = err
		} else if got, err := repo.gitNotes("notes", "--ref="+ref, "list", commit); err == nil && got == want {
			return nil
		}
		time.Sleep(time.Duration(attempt) * 20 * time.Millisecond)
	}
	return fmt.Errorf("the note in refs/notes/%s on %.12s could not be confirmed after %d tries: %w", ref, commit, noteAttempts, last)
}

// gitNotes runs one git command in the repository for the note writer and
// returns its trimmed output.
func (g *GitRepo) gitNotes(args ...string) (string, error) {
	name, email := g.identity()
	cmd := exec.Command("git", append([]string{"-C", g.path, "-c", "core.hooksPath=/dev/null"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME="+name, "GIT_AUTHOR_EMAIL="+email,
		"GIT_COMMITTER_NAME="+name, "GIT_COMMITTER_EMAIL="+email)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// lockNotes takes the repository's notes lock: a flock on a file in the git
// directory shared by every worktree, so another process and another goroutine
// wait their turn. It gives up after the time a note write can reasonably take.
func lockNotes(repo *GitRepo) (unlock func(), err error) {
	dir, err := repo.gitNotes("rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, fmt.Errorf("lock the notes: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "staircase-notes.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("lock the notes: %w", err)
	}
	for deadline := time.Now().Add(30 * time.Second); wslock.LockExclusive(f.Fd()) != nil; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, errors.New("lock the notes: another delivery in this repository holds the lock")
		}
	}
	return func() { _ = wslock.Unlock(f.Fd()); _ = f.Close() }, nil
}
