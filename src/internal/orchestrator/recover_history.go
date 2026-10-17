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
	"github.com/b070nd/stAirCase/src/internal/history"
)

// What a run approved is known from two records that must agree: the audit chain
// (what was decided, in order, tamper-evident) and the approval journal (the
// requests themselves, which the chain only holds the hash of). Recovery commits
// only what both say, and refuses when they do not fit together, because
// certifying a prefix of what was approved would pass an incomplete change off as
// the run's own.

// policyDigest is the digest of the policy the run recorded it loaded; "" when it recorded none.
func policyDigest(h history.History) string {
	if h.Policy == nil {
		return ""
	}
	return h.Policy.Digest
}

// initiatorOf is the signed request of the person who started the run, as a certificate names it.
func initiatorOf(h history.History) *certificate.Initiator {
	if h.Initiator == nil {
		return nil
	}
	return &certificate.Initiator{Principal: h.Initiator.Principal, Signature: h.Initiator.Signature}
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
// A repeated number is corruption too. Nor is every unterminated invalid line a cut one: only a line
// that is a prefix of what appendJournal writes (see isCutJournalLine).
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
			cut := i == len(lines)-1 && unterminated && isCutJournalLine(line)
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

// isCutJournalLine reports whether line could be the beginning of a line appendJournal writes, which is
// always {"seq":<digits>,"source":"<text>","request":<json>}: what a crash that cut the write leaves. A line
// that no completion could turn into one (garbage, a stray bracket, other keys, extra content) is not a
// cut write but corruption.
func isCutJournalLine(line []byte) bool {
	rest := line
	// literal parts of the format, then the digits of seq, then the source text, then the request
	eat := func(lit string) (ok, ran bool) { // ok: rest starts with lit, or rest is a prefix of lit; ran: rest was used up
		if len(rest) <= len(lit) {
			return bytes.HasPrefix([]byte(lit), rest), true
		}
		if !bytes.HasPrefix(rest, []byte(lit)) {
			return false, false
		}
		rest = rest[len(lit):]
		return true, false
	}
	if ok, ran := eat(`{"seq":`); !ok || ran {
		return ok
	}
	n := 0
	for n < len(rest) && rest[n] >= '0' && rest[n] <= '9' {
		n++
	}
	if n == 0 {
		return len(rest) == 0
	}
	rest = rest[n:]
	if ok, ran := eat(`,"source":"`); !ok || ran {
		return ok
	}
	q := bytes.IndexByte(rest, '"') // the source is a plain word: a closing quote ends it
	if q < 0 {
		return bytes.IndexFunc(rest, func(r rune) bool { return r < ' ' }) < 0
	}
	rest = rest[q+1:]
	if ok, ran := eat(`,"request":`); !ok || ran {
		return ok
	}
	// the request is one JSON value, then the entry's closing brace
	dec := json.NewDecoder(bytes.NewReader(rest))
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			var syn *json.SyntaxError
			return !errors.As(err, &syn) && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF))
		}
		if d, ok := tok.(json.Delim); ok {
			if d == '{' || d == '[' {
				depth++
			} else {
				depth--
			}
		}
		if depth == 0 { // the request is complete: nothing but the closing brace may follow
			tail := bytes.TrimSpace(rest[dec.InputOffset():])
			return len(tail) == 0 || string(tail) == "}"
		}
	}
}

// reconcile pairs what the chain approved with the journal's requests and returns
// the proposals to commit, in the chain's order. It refuses unless every approval
// the chain recorded has its request in the journal, unchanged: an approval that
// was acknowledged to the agent but cannot be recovered means the recovered change
// would be less than what the run approved. Journal entries the chain has no
// approval for are what a crash leaves (written, not yet audited, so never
// released) and are ignored only after the last decision the chain holds; anywhere
// else they are corruption.
func reconcile(h history.History, j journalRead) ([]LedgerProposal, error) {
	bySeq := map[int]journalEntry{}
	for _, e := range j.Entries {
		bySeq[e.Seq] = e
	}
	audited := map[int]bool{}
	var kept []LedgerProposal
	for _, a := range h.Approvals() {
		audited[a.Seq] = true
		e, ok := bySeq[a.Seq]
		if !ok {
			return nil, fmt.Errorf("proposal %d was approved and is on the audit chain, but the journal does not have it: the change cannot be recovered whole", a.Seq)
		}
		if sha256Hex(e.Request) != a.RequestSHA256 {
			return nil, fmt.Errorf("the journal's request for proposal %d is not the one the audit chain recorded", a.Seq)
		}
		var req domain.YieldRequest
		if err := json.Unmarshal(e.Request, &req); err != nil {
			return nil, fmt.Errorf("the journal's request for proposal %d is unreadable: %w", a.Seq, err)
		}
		kept = append(kept, LedgerProposal{Seq: a.Seq, Source: a.Source, Edits: req.ProposedEdits}) // who decided is the chain's word, not the journal's
	}
	unconsumed := h.Unconsumed()
	for _, e := range j.Entries {
		if sum, ok := unconsumed[e.Seq]; ok && sum == sha256Hex(e.Request) {
			continue // the chain says this authorization expired before it was consumed
		}
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
