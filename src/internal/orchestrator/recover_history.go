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

// history is what recovery reads from a run's audit events.
type history struct {
	Bound       runBinding
	Approvals   []auditedApproval // in the order the chain recorded them
	LastDecided int               // the highest proposal number the chain decided, approved or not
	// What the run loaded and was started with, as it recorded it: the policy it
	// ran under (not whatever policy.json holds now) and the signed request of the
	// person who started it.
	PolicyDigest string
	Initiator    *certificate.Initiator
}

// auditedHistory reads a run's audit events, oldest first, and refuses a history
// that no run could have written even though its hashes are right (a decision
// before the run began, proposal numbers that do not increase, a decision after
// the run's evidence was issued). It is pure: it reads events and nothing else.
func auditedHistory(events []domain.RunEventLog) (history, error) {
	var h history
	bounds, lastSeq, sealed := 0, 0, ""
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

// parseJournal reads JSON Lines. A last line that does not parse is a torn tail
// (the crash cut the append, so it was never audited either); a line that does
// not parse and is followed by others is corruption, and so is a repeated number.
func parseJournal(raw []byte) (journalRead, error) {
	var j journalRead
	lines := bytes.Split(raw, []byte("\n"))
	for len(lines) > 0 && len(bytes.TrimSpace(lines[len(lines)-1])) == 0 {
		lines = lines[:len(lines)-1]
	}
	seen := map[int]bool{}
	for i, line := range lines {
		var e journalEntry
		if err := json.Unmarshal(line, &e); err != nil || e.Seq <= 0 {
			if i == len(lines)-1 {
				j.TornTail = true
				continue
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
// audit chain recorded for it. A record with no digest (a history from a version
// that did not write one) is not compared.
func matchesDigest(recorded map[string]string, derived map[string]*approvedFile) bool {
	return len(recorded) == 0 || maps.Equal(recorded, digest(derived))
}
