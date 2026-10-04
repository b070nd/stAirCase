package orchestrator

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/b070nd/stAirCase/src/internal/wslock"
)

// A recovery is an operation with an identity. Before it touches git it writes what
// it is about to do (which run, from which base, the tree the approvals produce, the
// approvals in order, the chain head, whether a person had to review it); the commit
// it makes names the operation in a trailer. A later recover knows a commit is its
// own because that name, the base as sole parent and the expected tree all agree, not
// because it looks about right: a commit anyone else made with the same tree and a
// copied trailer is a conflict, and never skips the review.

type opApproval struct {
	Seq           int    `json:"seq"`
	Source        string `json:"source"`
	RequestSHA256 string `json:"request_sha256"`
}

type recoveryOp struct {
	Op          string       `json:"op"`
	RunID       int64        `json:"run_id"`
	Base        string       `json:"base"`
	Branch      string       `json:"branch"`
	Tree        string       `json:"tree"`
	Approvals   []opApproval `json:"approvals"`
	ChainHead   string       `json:"chain_head"`
	NeedsReview bool         `json:"needs_review"`
	Reviewed    bool         `json:"reviewed,omitempty"`
	Commit      string       `json:"commit,omitempty"` // known once delivered
}

// recoveryTrailer names the operation in the commit it made.
const recoveryTrailer = "Staircase-Recovery"

func opPath(wsDir string, runID int64) string {
	return filepath.Join(wsDir, "journal", fmt.Sprintf("run-%d.recovery.json", runID))
}

// loadOp reads a run's recovery operation; nil when there is none.
func loadOp(wsDir string, runID int64) (*recoveryOp, error) {
	b, err := os.ReadFile(opPath(wsDir, runID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the recovery record: %w", err)
	}
	var o recoveryOp
	if err := json.Unmarshal(b, &o); err != nil || o.Op == "" || o.RunID != runID {
		return nil, errors.New("the recovery record of this run is damaged: not guessing what it meant")
	}
	return &o, nil
}

// save writes the record durably: it must be on disk before git is touched.
func (o *recoveryOp) save(wsDir string) error {
	b, err := json.Marshal(o)
	if err != nil {
		return err
	}
	dir := filepath.Dir(opPath(wsDir, o.RunID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".recovery-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), opPath(wsDir, o.RunID))
}

// matches reports whether this operation is about the same change as the one
// recovery has just worked out.
func (o *recoveryOp) matches(base, branch, tree string, approvals []opApproval) bool {
	return o.Base == base && o.Branch == branch && o.Tree == tree && slices.Equal(o.Approvals, approvals)
}

// owns reports whether tip is the commit this operation made: the base as its
// only parent, the expected tree, and this operation's name in its trailer.
func (o *recoveryOp) owns(repoPath, tip string) bool {
	if gitOut(repoPath, "rev-list", "--parents", "-n", "1", tip) != tip+" "+o.Base {
		return false
	}
	if gitOut(repoPath, "rev-parse", tip+"^{tree}") != o.Tree {
		return false
	}
	return gitOut(repoPath, "log", "-1", "--format=%(trailers:key="+recoveryTrailer+",valueonly)", tip) == o.Op
}

func newOpID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// lockRecovery takes the run's recovery lock without waiting: two recoveries of
// one run would race to write the same evidence.
func lockRecovery(wsDir string, runID int64) (unlock func(), err error) {
	if err := os.MkdirAll(filepath.Join(wsDir, "journal"), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(wsDir, "journal", fmt.Sprintf("run-%d.recovery.lock", runID)), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := wslock.LockExclusive(f.Fd()); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another recovery of run #%d is in progress", runID)
	}
	return func() { _ = wslock.Unlock(f.Fd()); _ = f.Close() }, nil
}

// approvalsOf is the fingerprint of the approvals a recovery is about: the
// numbers, who decided and the hash of each request, as the chain recorded them.
func approvalsOf(approvals []auditedApproval) []opApproval {
	out := make([]opApproval, len(approvals))
	for i, a := range approvals {
		out[i] = opApproval{Seq: a.Seq, Source: a.Source, RequestSHA256: a.Hash}
	}
	return out
}
