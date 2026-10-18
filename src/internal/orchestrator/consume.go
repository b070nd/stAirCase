package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/b070nd/stAirCase/src/internal/barrier"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/history"
)

// Consuming a decision (ADR 0005): an approval is CONSUMED when its decision is on the audit chain. It is kept in the journal
// first, so a run that dies after that can still be recovered with exactly what it approved, and the agent is told only after.
// Whether the run is still live is checked before the journal, after it, and under the chain's append lock immediately before
// the insert; once it is not, nothing more is consumed, however it was decided: the decision is recorded as a rejection, marked
// unconsumed when its journal line was already written. An insert that has begun is written.

// consumeError is a write that failed, which stops the run: the journal line (journal) or the decision on the chain.
type consumeError struct {
	journal bool
	err     error
}

func (e *consumeError) Error() string {
	if e.journal {
		return "journal approval: " + e.err.Error()
	}
	return "audit yield_decided: " + e.err.Error()
}

func (e *consumeError) Unwrap() error { return e.err }

// consume puts proposal seq's decision on the run's chain: rl is the decision, record its audit record. An approval of a change
// is journaled first. When live has ended before the decision is on the chain, rl becomes the rejection that is recorded
// instead. The error is a *consumeError: the run must stop, and the agent is told it was not decided.
func (r *Runner) consume(live context.Context, runID int64, seq int, req domain.YieldRequest, rl *ruling, record func(journaled bool) history.Decided) error {
	expire := func() {
		rl.resp, rl.next, rl.evidence = domain.Decide(false, "the run ended before this was decided"), nil, nil
		rl.source, rl.signed = "orchestrator", history.Signed{}
	}
	if rl.resp.Approved && live.Err() != nil {
		expire()
	}
	journaled := false
	if rl.resp.Approved && rl.next != nil {
		if err := keepApproval(r.wsDir, runID, req, seq, rl.source); err != nil {
			return &consumeError{journal: true, err: err}
		}
		journaled = true
		barrier.Hit(barrier.JournalSynced)
		if live.Err() != nil { // it ended between the journal and the chain: not consumed
			expire()
		}
	}
	var err error
	if rl.resp.Approved {
		expired := false
		err = r.auditIf(runID, "yield_decided", record(journaled), func() error {
			barrier.Hit(barrier.AuditAppend)
			if err := live.Err(); err != nil {
				expired = true
				return err
			}
			return nil
		})
		if expired { // the run ended while this waited for the chain: not consumed, recorded as a rejection
			expire()
			err = r.audit(runID, "yield_decided", record(journaled))
		}
	} else {
		err = r.audit(runID, "yield_decided", record(journaled))
	}
	if err != nil {
		return &consumeError{err: err}
	}
	barrier.Hit(barrier.AuditCommitted)
	return nil
}

// keepApproval writes approval seq's journal line, durably. A line already there for seq with exactly these bytes was written by an
// attempt interrupted before its decision, and stands; one with other bytes is refused, since two lines with one number would
// make the journal unreadable for recovery. A torn last line, which only a crash during a write leaves and which was never
// consumed, is dropped first so the new line does not follow it.
func keepApproval(wsDir string, runID int64, req domain.YieldRequest, seq int, source string) error {
	j, err := readJournalFile(wsDir, runID)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return err
	case j.TornTail:
		if err := rewriteJournal(wsDir, runID, j.Entries); err != nil {
			return err
		}
	}
	want, _ := json.Marshal(req) // the bytes yieldDecided hashes
	for _, e := range j.Entries {
		if e.Seq == seq {
			if bytes.Equal(e.Request, want) {
				return nil
			}
			return errors.New("the approval journal already holds another proposal under this number")
		}
	}
	return appendJournal(wsDir, runID, req, seq, source)
}
