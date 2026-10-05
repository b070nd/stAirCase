package orchestrator

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/domain"
)

// What a run approved is known from two records that must agree: the audit chain
// (what was decided, in order, tamper-evident) and the approval journal (the
// requests themselves, which the chain only holds the hash of). Recovery commits
// only what both say, and refuses when they do not fit together, because
// certifying a prefix of what was approved would pass an incomplete change off as
// the run's own.

// runBinding is what a run recorded about where it started.
type runBinding struct{ Base, Branch, Harness, PlanDigest, Blueprint string }

// auditedApproval is one approved file change as the audit chain records it.
type auditedApproval struct {
	Seq    int
	Source string
	Hash   string            // the request's SHA-256
	Files  map[string]string // the digest of exactly the approved state
}

// auditedFinalReview is a final review as the audit chain records it.
type auditedFinalReview struct {
	Approved bool
	Files    map[string]string
}

// history is what recovery reads from a run's audit events.
type history struct {
	Bound       runBinding
	Approvals   []auditedApproval // in the order the chain recorded them
	LastDecided int               // the highest proposal number the chain decided, approved or not
	// The final reviews a person decided (a decision of its own, with no request before it):
	// each is the digest of the whole approved state that was put to them.
	FinalReviews []auditedFinalReview
	// What the run loaded and was started with, as it recorded it: the policy it
	// ran under (not whatever policy.json holds now) and the signed request of the
	// person who started it.
	PolicyDigest string
	Initiator    *certificate.Initiator
}

// auditedHistory reads a run's audit events, oldest first, and refuses a history
// that no run could have written even though its hashes are right (a decision
// before the run began, proposal numbers that do not increase, a decision after
// the run's evidence was issued, a decision no request led to). It is pure: it reads
// events and nothing else.
//
// The legal shape, from what a run writes (v0.6.0, the first version with a journal,
// and later): run_bound first and once; a yield_request audited before every
// decision it leads to (each decision uses up one request of its kind; the requests
// of different proposals may be audited in another order than they are decided, and
// a request may stay undecided when the run dies); proposal numbers that increase;
// a final review is a yield_decided of its own and uses up no request; nothing is
// decided after the certificate or a recovery.
func auditedHistory(events []domain.RunEventLog) (history, error) {
	var h history
	bounds, lastSeq, sealed := 0, 0, ""
	pending := map[string]int{} // requests not yet decided, by action type
	for i, e := range events {
		var p struct {
			BaseSHA       string            `json:"base_sha"`
			Branch        string            `json:"branch"`
			Harness       string            `json:"harness"`
			PlanDigest    string            `json:"plan_digest"`
			BlueprintHash string            `json:"blueprint_hash"`
			Seq           int               `json:"seq"`
			Source        string            `json:"source"`
			Approved      bool              `json:"approved"`
			ActionType    string            `json:"action_type"`
			RequestSHA256 string            `json:"request_sha256"`
			Files         map[string]string `json:"files"`
			Digest        string            `json:"digest"`
			Principal     string            `json:"principal"`
			Signature     string            `json:"signature"`
		}
		if i == 0 && e.EventType != "run_bound" {
			return h, fmt.Errorf("the audit history begins with %q, not run_bound", e.EventType)
		}
		switch e.EventType {
		case "run_bound":
			bounds++
			if bounds > 1 {
				return h, errors.New("the audit history binds the run to a commit more than once")
			}
			if json.Unmarshal([]byte(e.Payload), &p) != nil {
				return h, errors.New("the run_bound record is unreadable")
			}
			h.Bound = runBinding{p.BaseSHA, p.Branch, p.Harness, p.PlanDigest, p.BlueprintHash}
		case "policy_snapshot":
			if json.Unmarshal([]byte(e.Payload), &p) == nil {
				h.PolicyDigest = p.Digest
			}
		case "initiator_signed":
			if json.Unmarshal([]byte(e.Payload), &p) == nil && p.Signature != "" {
				h.Initiator = &certificate.Initiator{Principal: p.Principal, Signature: p.Signature}
			}
		case "yield_request":
			var q struct {
				ActionType string `json:"action_type"`
			}
			if json.Unmarshal([]byte(e.Payload), &q) != nil {
				return h, fmt.Errorf("the request at entry %d is unreadable", i+1)
			}
			pending[q.ActionType]++
		case "certificate_issued", "run_recovered":
			sealed = e.EventType
		case "yield_decided":
			if json.Unmarshal([]byte(e.Payload), &p) != nil {
				return h, fmt.Errorf("the decision at entry %d is unreadable", i+1)
			}
			if sealed != "" {
				return h, fmt.Errorf("a decision (proposal %d) follows %s", p.Seq, sealed)
			}
			if p.Seq <= lastSeq {
				return h, fmt.Errorf("proposal numbers do not increase: %d follows %d", p.Seq, lastSeq)
			}
			lastSeq = p.Seq
			if p.ActionType == domain.ActionFinalReview {
				h.FinalReviews = append(h.FinalReviews, auditedFinalReview{p.Approved, p.Files})
				continue
			}
			if pending[p.ActionType] == 0 {
				return h, fmt.Errorf("the decision of proposal %d (%s) has no request before it", p.Seq, p.ActionType)
			}
			pending[p.ActionType]--
			if p.Approved && p.ActionType == domain.ActionFileEdit {
				h.Approvals = append(h.Approvals, auditedApproval{p.Seq, p.Source, p.RequestSHA256, p.Files})
			}
		}
	}
	h.LastDecided = lastSeq
	return h, nil
}

// journalRead is the approval journal as it can be trusted.
type journalRead struct {
	Entries  []journalEntry
	TornTail bool // the last line was cut short, as a crash does
}

// parseJournal reads JSON Lines. appendJournal writes a line and its newline in one
// write, so what a crash cuts is the end of the last line: a last line without its
// newline that is not complete JSON is a torn tail (never audited either, so never
// released). Anything else that is not an entry is corruption, whatever it looks like:
// a line that ends in a newline, complete JSON that is not an entry (`{}`, `null`, a
// number that is not positive, no request), or an invalid line with others after it.
// A repeated number is corruption too.
func parseJournal(raw []byte) (journalRead, error) {
	var j journalRead
	unterminated := len(raw) > 0 && raw[len(raw)-1] != '\n'
	lines := bytes.Split(raw, []byte("\n"))
	for len(lines) > 0 && len(bytes.TrimSpace(lines[len(lines)-1])) == 0 {
		lines = lines[:len(lines)-1]
	}
	seen := map[int]bool{}
	for i, line := range lines {
		var e journalEntry
		if err := json.Unmarshal(line, &e); err != nil || e.Seq <= 0 || len(e.Request) == 0 {
			cut := i == len(lines)-1 && unterminated && !json.Valid(line)
			if cut {
				j.TornTail = true
				continue
			}
			if i == len(lines)-1 {
				return j, fmt.Errorf("the last journal line (%d) is complete but is not an entry: it is corrupt, not a cut append", i+1)
			}
			return j, fmt.Errorf("journal line %d is corrupt and entries follow it", i+1)
		}
		if seen[e.Seq] {
			return j, fmt.Errorf("journal line %d repeats proposal %d", i+1, e.Seq)
		}
		seen[e.Seq] = true
		j.Entries = append(j.Entries, e)
	}
	return j, nil
}

// reconcile pairs what the chain approved with the journal's requests and returns
// the proposals to commit, in the chain's order. It refuses unless every approval
// the chain recorded has its request in the journal, unchanged: an approval that
// was acknowledged to the agent but cannot be recovered means the recovered change
// would be less than what the run approved. Journal entries the chain has no
// approval for are what a crash leaves (written, not yet audited, so never
// released) and are ignored only after the last decision the chain holds; anywhere
// else they are corruption.
func reconcile(h history, j journalRead) ([]LedgerProposal, error) {
	bySeq := map[int]journalEntry{}
	for _, e := range j.Entries {
		bySeq[e.Seq] = e
	}
	audited := map[int]bool{}
	var kept []LedgerProposal
	for _, a := range h.Approvals {
		audited[a.Seq] = true
		e, ok := bySeq[a.Seq]
		if !ok {
			return nil, fmt.Errorf("proposal %d was approved and is on the audit chain, but the journal does not have it: the change cannot be recovered whole", a.Seq)
		}
		if sha256Hex(e.Request) != a.Hash {
			return nil, fmt.Errorf("the journal's request for proposal %d is not the one the audit chain recorded", a.Seq)
		}
		var req domain.YieldRequest
		if err := json.Unmarshal(e.Request, &req); err != nil {
			return nil, fmt.Errorf("the journal's request for proposal %d is unreadable: %w", a.Seq, err)
		}
		kept = append(kept, LedgerProposal{Seq: a.Seq, Source: a.Source, Edits: req.ProposedEdits}) // who decided is the chain's word, not the journal's
	}
	for _, e := range j.Entries {
		if !audited[e.Seq] && e.Seq <= h.LastDecided {
			return nil, fmt.Errorf("the journal holds proposal %d, which the audit chain did not approve although it decided later ones", e.Seq)
		}
	}
	return kept, nil
}

// readJournalFile reads a run's journal for reconcile; a missing file is an error.
func readJournalFile(wsDir string, runID int64) (journalRead, error) {
	f, err := os.Open(journalPath(wsDir, runID))
	if err != nil {
		return journalRead{}, err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(f)
	if err != nil {
		return journalRead{}, fmt.Errorf("read the journal: %w", err)
	}
	return parseJournal(raw)
}

// matchesDigest reports whether the state a proposal derives to is the digest the
// audit chain recorded for it. Every version that keeps a journal (v0.6.0 on)
// records the digest of exactly the approved state with an approval, so a record
// without one, or with another, is not accepted: there is no older schema to be
// lenient to.
func matchesDigest(recorded map[string]string, derived map[string]*approvedFile) bool {
	return maps.Equal(recorded, digest(derived))
}

// finalReviewed reports whether a person's final review of exactly this approved
// state is on the chain, and an error when the chain holds an approved final review
// of other bytes: what was reviewed is not what the approvals derive.
func (h history) finalReviewed(files map[string]*approvedFile) (bool, error) {
	reviewed := false
	for _, f := range h.FinalReviews {
		if !f.Approved {
			continue
		}
		if !maps.Equal(f.Files, digest(files)) {
			return false, errors.New("the audit chain records a final review that approved other bytes than the approvals derive")
		}
		reviewed = true
	}
	return reviewed, nil
}
