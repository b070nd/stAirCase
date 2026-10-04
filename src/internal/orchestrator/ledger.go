package orchestrator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/domain"
)

// The ledger file's format: 1 is text only; 2 is the same with whole-file changes
// that are not text (an edit's content_b64). A ledger is version 2 only when it
// needs to be, so a reader that knows version 1 refuses what it cannot reproduce.
const (
	ledgerVersion       = 1
	ledgerVersionBinary = 2
)

// Ledger is the record from which a run's commit can be rebuilt: the base
// commit and, in order, every approved proposal exactly as the orchestrator
// derived its files from it. It holds content, so it stays in the workspace
// (audit/run-N.ledger.json, readable by the user only); the change
// certificate carries its SHA-256.
type Ledger struct {
	Version   int              `json:"version"`
	Base      string           `json:"base"`
	Proposals []LedgerProposal `json:"proposals"`
}

// LedgerProposal is one approved proposal.
type LedgerProposal struct {
	Seq    int                   `json:"seq"`    // the run's proposal number
	Source string                `json:"source"` // who decided it
	Edits  []domain.ProposedEdit `json:"edits"`
}

// hasBinary reports whether any edit needs version 2: a file that is not text,
// or an explicit mode.
func (l Ledger) hasBinary() bool {
	for _, p := range l.Proposals {
		for _, e := range p.Edits {
			if e.ContentB64 != "" || e.Mode != "" {
				return true
			}
		}
	}
	return false
}

// add records an approved proposal.
func (l *Ledger) add(seq int, source string, edits []domain.ProposedEdit) {
	l.Proposals = append(l.Proposals, LedgerProposal{Seq: seq, Source: source, Edits: append([]domain.ProposedEdit(nil), edits...)})
}

// writeLedger writes the ledger to the workspace and returns its digest.
func (r *Runner) writeLedger(runID int64, l Ledger) (string, error) {
	l.Version = ledgerVersion
	if l.hasBinary() {
		l.Version = ledgerVersionBinary
	}
	if l.Proposals == nil {
		l.Proposals = []LedgerProposal{}
	}
	b, err := json.Marshal(l)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(r.wsDir, "audit")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(LedgerPath(r.wsDir, runID), b, 0o600); err != nil {
		return "", err
	}
	return sha256Hex(b), nil
}

// LedgerPath is where a run's ledger is kept in the workspace.
func LedgerPath(wsDir string, runID int64) string {
	return filepath.Join(wsDir, "audit", fmt.Sprintf("run-%d.ledger.json", runID))
}

// RebuildTree replays a ledger on its base commit in the repository at
// repoPath and returns the git tree it produces: the same derivation and tree
// construction a run uses, so an honest run's commit has exactly this tree.
func RebuildTree(repoPath string, raw []byte) (string, Ledger, error) {
	var l Ledger
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		return "", l, fmt.Errorf("ledger: %w", err)
	}
	if l.Version != ledgerVersion && l.Version != ledgerVersionBinary {
		return "", l, fmt.Errorf("ledger version %d is not supported (this program reads versions %d and %d)", l.Version, ledgerVersion, ledgerVersionBinary)
	}
	if l.Version == ledgerVersion && l.hasBinary() {
		return "", l, fmt.Errorf("a version %d ledger holds text only with default modes, but it has content_b64 or mode", ledgerVersion)
	}
	repo, err := OpenGitRepo(repoPath)
	if err != nil {
		return "", l, err
	}
	// The files are derived against an empty directory, not this checkout:
	// what is on disk now has no say in what was approved.
	scratch, err := os.MkdirTemp("", "staircase-rebuild-")
	if err != nil {
		return "", l, err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	a, err := newApprovals(&GitRepo{r: repo.r, w: repo.w, path: scratch}, l.Base)
	if err != nil {
		return "", l, err
	}
	for _, p := range l.Proposals {
		next, err := a.derive(p.Edits)
		if err != nil {
			return "", l, fmt.Errorf("proposal %d: %w", p.Seq, err)
		}
		a.record(next)
	}
	env := append(os.Environ(), "GIT_INDEX_FILE="+filepath.Join(scratch, "index"))
	git := func(stdin []byte, args ...string) (string, error) {
		cmd := exec.Command("git", append([]string{"-C", repoPath, "-c", "core.hooksPath=/dev/null"}, args...)...)
		var stderr bytes.Buffer
		cmd.Env, cmd.Stdin, cmd.Stderr = env, bytes.NewReader(stdin), &stderr
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("git %s: %w: %s", args[0], err, bytes.TrimSpace(stderr.Bytes()))
		}
		return strings.TrimSpace(string(out)), nil
	}
	tree, err := applyFiles(git, l.Base, a.files)
	return tree, l, err
}

// applyFiles makes the git tree of the base commit with files applied, in
// the index git points at, and returns its id.
func applyFiles(git func(stdin []byte, args ...string) (string, error), base string, files map[string]*approvedFile) (string, error) {
	if _, err := git(nil, "read-tree", base); err != nil {
		return "", err
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		f := files[p]
		if f.deleted {
			if _, err := git(nil, "update-index", "--force-remove", "--", p); err != nil {
				return "", err
			}
			continue
		}
		blob, err := git(f.content, "hash-object", "-w", "--stdin")
		if err != nil {
			return "", err
		}
		mode := "100644"
		if f.mode&0o111 != 0 {
			mode = "100755"
		}
		if _, err := git(nil, "update-index", "--add", "--cacheinfo", mode, blob, p); err != nil {
			return "", err
		}
	}
	return git(nil, "write-tree")
}
