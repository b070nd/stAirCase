// Package history is the one replay of a run's audit chain (ADR 0010): what a verified chain says a run bound, proposed,
// decided, parked, adopted, offered and delivered, read in one walk. A chain that no run can have written has no history; it is
// refused with a GrammarError that names the entry. The package decides only from verified chains (Read); ReadRows shows a chain as
// stored, marked, for the commands that display one. It is also the one way an entry is written (Writer, ADR 0014): every event is
// a type here, and a payload's keys are those of its type. It touches no git and no files.
package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stAirCase/src/internal/chain"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/persistence"
)

// History is what a run's verified chain says about it.
type History struct {
	RunID int64 // the run the chain belongs to, when it was read from a store (Read); 0 for a bare replay
	Bound Bound
	// Decisions are every yield_decided, in chain order, final reviews included; each but a final review carries its request.
	Decisions   []Decision
	LastDecided int // the highest proposal number decided, approved or not

	Policy    *PolicySnapshot  // the policy the run loaded; nil when none is on the chain
	Initiator *InitiatorSigned // who started the run, when they signed
	Session   AgentSession     // the agent's vendor session as the chain last named it

	Segment   int           // how many segments the run has had: 1, plus one per run_resumed
	Elapsed   time.Duration // active time: each segment from its start to its last entry
	Undecided int           // requests of the last segment nobody decided
	Halted    bool          // drift supervision halted the run, and no resume acknowledged it since
	Usage     []StateEmit
	Shells    ShellCount

	Launches []Launch // every agent launch, in chain order

	// Votes are the reviewer models' verdicts, in chain order (validator_voted), each with the chain position it was written at.
	Votes []Vote

	Review *Review // the last review an away run offered
	// Authorized is the delivery_authorized entry of a run that delivers by policy: set once the run decided to deliver without a
	// person. Nothing is decided on the chain after it.
	Authorized *DeliveryAuthorized
	// Sealed is the first entry that ended the agent's part of the run (delivery_authorized, until the commit is named, then
	// commit_prepared) or the chain (certificate_issued, run_recovered, review_delivered): "" while the run can still be continued.
	Sealed    string
	Delivered []Delivery // the commit-naming events, in chain order
	Head      string     // the last entry's hash

	entries map[string]bool
	end     ending
}

// ending is what the chain says about how the run ended, for Outcome.
type ending struct {
	segStart     int    // the position of the last segment's start: run_bound or its last run_resumed
	ended        *Ended // the last segment's run_path; nil while the chain does not say it ended
	haltReason   string // why drift supervision halted the run, while Halted
	checked      int    // the position of the last phase_checked, its entry hash, and what it found
	checkedEntry string
	check        PhaseChecked
	commit       string   // the delivered commit: named by an event written after the branch moved
	recovered    bool     // it was delivered by recovery of an interrupted run
	evidence     []string // what the last report on the delivered commit's evidence said was missing
}

// Decision is one yield_decided with where it is and what it answered.
type Decision struct {
	Decided
	Index   int      // its position on the chain, from 1
	Entry   string   // its entry hash
	Request *Request // the request it used up; nil for a final review
}

// Vote is one reviewer model's verdict as the chain holds it.
type Vote struct {
	ValidatorVoted
	Index int    // its position on the chain, from 1
	Entry string // its entry hash
}

// VotesOn are the votes on the proposal whose request is at this entry, in the order the models were asked.
func (h History) VotesOn(requestEntry string) []Vote {
	var out []Vote
	for _, v := range h.Votes {
		if v.RequestEntry == requestEntry {
			out = append(out, v)
		}
	}
	return out
}

// Panelled is a proposal the reviewer models voted on: its number and paths, their votes in the order they were asked, and who
// decided it in the end (the models together, or a person when they split or one failed).
type Panelled struct {
	Seq       int
	Paths     []string
	Votes     []Vote
	DecidedBy string // the decision's source
	Approved  bool
}

// Panelled lists the proposals that have votes, in the order they were decided. A proposal still waiting for its decision is not
// among them.
func (h History) Panelled() []Panelled {
	var out []Panelled
	for _, d := range h.Decisions {
		if d.Request == nil {
			continue
		}
		if votes := h.VotesOn(d.Request.Entry); len(votes) > 0 {
			out = append(out, Panelled{Seq: d.Seq, Paths: d.Paths, Votes: votes, DecidedBy: d.Source, Approved: d.Approved})
		}
	}
	return out
}

// VoteSummary counts the reviewer models' votes: all of them, the proposals they were on, and the proposals the panel did not settle
// (the models disagreed, or one gave no verdict), which went to a person.
func (h History) VoteSummary() (votes, proposals, unsettled int) {
	verdicts := map[string]map[string]bool{}
	for _, v := range h.Votes {
		votes++
		if verdicts[v.RequestEntry] == nil {
			verdicts[v.RequestEntry] = map[string]bool{}
		}
		verdicts[v.RequestEntry][v.Verdict] = true
	}
	for _, seen := range verdicts {
		proposals++
		if seen[VoteError] || (seen[VoteApprove] && seen[VoteReject]) {
			unsettled++
		}
	}
	return votes, proposals, unsettled
}

// Review is an offered review: the awaiting_review entry, the checks it names, and what the owner decided since.
type Review struct {
	AwaitingReview
	Index   int
	Entry   string
	Checked *PhaseChecked // the phase_checked entry it names, when that is earlier on the chain
	Decided string        // "accepted", "rejected", or "" while it waits
}

// Launch is a claimed agent launch and how it ended: "" while no outcome is on the chain.
type Launch struct {
	LaunchClaimed
	Index   int
	Entry   string
	Outcome string
	// Class is why the launch did not end well (see ClassOf): "" while it has no outcome, and when it ended by itself or the run
	// stopped it.
	Class string
	// Acked: a person acknowledged the launch's lost end, by launch_acknowledged or by the resume that continued it.
	Acked bool
}

// Lost reports whether the launch's end was never established: no outcome, one a restart recorded, or an agent that did not stop.
func (l Launch) Lost() bool {
	return l.Outcome == "" || l.Outcome == LaunchUnknown || l.Outcome == LaunchUnresponsive
}

// ShellCount is how many approved commands ran, and how many of them in the sandbox.
type ShellCount struct{ Ran, Sandboxed int }

// Delivery is a commit-naming event.
type Delivery struct {
	Event string
	Delivered
}

// GrammarError is a chain whose hashes are right but that no run can have written.
type GrammarError struct {
	Index  int    // the entry's position, from 1
	Event  string // its type
	Reason string
}

func (e *GrammarError) Error() string {
	return fmt.Sprintf("entry %d (%s): %s", e.Index, e.Event, e.Reason)
}

// Read replays the run's chain, read once and verified as read.
func Read(store *persistence.Store, runID int64) (History, error) {
	events, err := store.ListVerifiedEventLogs(runID)
	if err != nil {
		return History{}, fmt.Errorf("the audit chain of run #%d does not verify, so nothing in it can be trusted: %w", runID, err)
	}
	h, err := replay(events)
	if err != nil {
		return History{}, fmt.Errorf("the audit history of run #%d is not one a run can have written: %w", runID, err)
	}
	h.RunID = runID
	return h, nil
}

// replay is the fold. The grammar (ADR 0010, section 2): run_bound first and once; every decision but a final review uses up a
// request of the same segment and kind audited before it (the one it names, when it names one); proposal numbers increase;
// commit_prepared leaves room only for a final review; certificate_issued, run_recovered and review_delivered end the chain; launches
// are numbered in order, each outcome names the open launch, and nothing launches or resumes while a launch has no outcome; a launch
// is acknowledged only after it ended unknown or unresponsive, by name, once, and before the chain ends; delivery_authorized follows
// the latest phase_checked, of the same passing tree, once, in policy mode under the run's terms and signed policy, and nothing
// is written after it but what a delivery writes (afterAuthorization). Event
// types and fields it does not know are ignored.
func replay(events []domain.RunEventLog) (History, error) {
	h := History{Segment: 1, entries: map[string]bool{}}
	var pending []*Request // this segment's requests not yet decided, in chain order
	agentEnded, ended := "", ""
	reviewedSince := false // a review was offered since the last phase_checked
	var segStart, last time.Time
	for i, e := range events {
		n := i + 1
		fail := func(format string, a ...any) error {
			return &GrammarError{Index: n, Event: e.EventType, Reason: fmt.Sprintf(format, a...)}
		}
		read := func(v any) error {
			if json.Unmarshal([]byte(e.Payload), v) != nil {
				return fail("its payload is unreadable")
			}
			return nil
		}
		if i == 0 && e.EventType != "run_bound" {
			return h, fail("the audit history begins with %q, not run_bound", e.EventType)
		}
		h.entries[e.EventHash] = true
		// delivery_authorized itself falls through: a second one is refused by its own rule, with its own reason
		if h.Authorized != nil && e.EventType != "delivery_authorized" && !afterAuthorization[e.EventType] {
			return h, fail("%s follows delivery_authorized: once delivery is authorized, only the commit and its evidence are written", e.EventType)
		}
		switch e.EventType {
		case "run_bound":
			if i > 0 {
				return h, fail("the audit history binds the run to a commit more than once")
			}
			if err := read(&h.Bound); err != nil {
				return h, err
			}
			segStart, h.end.segStart = e.Timestamp, n
		case "run_resumed":
			if s := firstOf(ended, agentEnded); s != "" {
				return h, fail("the run was resumed after %s", s)
			}
			if o := h.OpenLaunch(); o != nil {
				return h, fail("the run was resumed while launch %d has no outcome", o.Attempt)
			}
			var p Resumed
			if err := read(&p); err != nil {
				return h, err
			}
			h.Elapsed += last.Sub(segStart)
			segStart, h.end.segStart, h.end.ended = e.Timestamp, n, nil
			h.Segment++
			pending = nil // a request an earlier segment left is not one this segment can decide
			if p.AckDrift {
				h.Halted, h.end.haltReason = false, ""
			}
			if at := p.AckUnknown - 1; at >= 0 && at < len(h.Launches) {
				h.Launches[at].Acked = true // attempts are numbered from 1, in order
			}
		case "launch_claimed":
			if s := firstOf(ended, agentEnded); s != "" {
				return h, fail("an agent launch follows %s", s)
			}
			if o := h.OpenLaunch(); o != nil {
				return h, fail("an agent is launched while launch %d has no outcome", o.Attempt)
			}
			l := Launch{Index: n, Entry: e.EventHash}
			if err := read(&l.LaunchClaimed); err != nil {
				return h, err
			}
			if l.Attempt != len(h.Launches)+1 {
				return h, fail("the launch claims attempt %d, after %d launches", l.Attempt, len(h.Launches))
			}
			h.Launches = append(h.Launches, l)
		case "launch_ended":
			if ended != "" {
				return h, fail("a launch's outcome follows %s", ended)
			}
			var p LaunchEnded
			if err := read(&p); err != nil {
				return h, err
			}
			o := h.OpenLaunch()
			if o == nil || o.Attempt != p.Attempt || o.Generation != p.Generation {
				return h, fail("the outcome names no open launch (attempt %d, generation %q)", p.Attempt, p.Generation)
			}
			switch p.Outcome {
			case LaunchExited, LaunchFailed, LaunchStopped, LaunchUnresponsive, LaunchUnknown:
			default:
				return h, fail("an unknown outcome %q", p.Outcome)
			}
			class := p.Class
			switch {
			case class == "":
				class = ClassOf(p.Outcome)
			case !classFits(class, p.Outcome):
				return h, fail("the class %q does not fit the outcome %q", class, p.Outcome)
			}
			o.Outcome, o.Class = p.Outcome, class
			if p.Outcome == LaunchUnknown {
				continue // written by a restart, after the run was down: not time the segment used
			}
		case "launch_acknowledged":
			if ended != "" {
				return h, fail("a launch's acknowledgement follows %s", ended)
			}
			var p LaunchAcknowledged
			if err := read(&p); err != nil {
				return h, err
			}
			// Only the last launch can be acknowledged, and only when its end was never established.
			n := len(h.Launches)
			if n == 0 || !h.Launches[n-1].Lost() || h.Launches[n-1].Outcome == "" ||
				h.Launches[n-1].Attempt != p.Attempt || h.Launches[n-1].Generation != p.Generation {
				return h, fail("the acknowledgement names no lost launch (attempt %d, generation %q): only a launch that ended unknown or unresponsive is acknowledged", p.Attempt, p.Generation)
			}
			if h.Launches[n-1].Acked {
				return h, fail("launch %d is already acknowledged", p.Attempt)
			}
			h.Launches[n-1].Acked = true
			continue // written by a person after the run was down: not time the segment used
		case "policy_snapshot":
			h.Policy = &PolicySnapshot{}
			if err := read(h.Policy); err != nil {
				return h, err
			}
		case "initiator_signed":
			var p InitiatorSigned
			if err := read(&p); err != nil {
				return h, err
			}
			if p.Signature != "" { // a record from before the signature was kept names nobody
				h.Initiator = &p
			}
		case "agent_session":
			var p AgentSession
			if err := read(&p); err != nil {
				return h, err
			}
			if p.ID != "" {
				h.Session = p
			}
		case "state_emit":
			var p StateEmit
			if err := read(&p); err != nil {
				return h, err
			}
			h.Usage = append(h.Usage, p)
		case "shell_ran":
			var p ShellRan
			if err := read(&p); err != nil {
				return h, err
			}
			h.Shells.Ran++
			if p.Sandboxed {
				h.Shells.Sandboxed++
			}
		case "drift_halt":
			var p DriftHalt
			if err := read(&p); err != nil {
				return h, err
			}
			h.Halted, h.end.haltReason = true, p.Reason
		case "run_path":
			h.end.ended = &Ended{}
			if err := read(h.end.ended); err != nil {
				return h, err
			}
		case "evidence_failed":
			var p EvidenceFailed
			if err := read(&p); err != nil {
				return h, err
			}
			h.end.commit, h.end.evidence = p.Commit, p.Errors
		case "certificate_failed":
			var p CertificateFailed
			if err := read(&p); err != nil {
				return h, err
			}
			h.end.commit = p.Commit
		case "recovery_repaired":
			var p Delivered
			if err := read(&p); err != nil {
				return h, err
			}
			h.end.evidence = p.EvidenceErrors
		case "yield_request":
			if agentEnded != "" {
				return h, fail("a request follows %s", agentEnded)
			}
			r := &Request{Index: n, Entry: e.EventHash}
			if err := read(r); err != nil {
				return h, err
			}
			pending = append(pending, r)
		case "validator_voted":
			var v Vote
			if err := read(&v.ValidatorVoted); err != nil {
				return h, err
			}
			if s := firstOf(ended, agentEnded); s != "" {
				return h, fail("a reviewer model voted after %s", s)
			}
			if err := voteRefused(h, pending, v.ValidatorVoted); err != "" {
				return h, fail("%s", err)
			}
			v.Index, v.Entry = n, e.EventHash
			h.Votes = append(h.Votes, v)
		case "yield_decided":
			d := Decision{Index: n, Entry: e.EventHash}
			if err := read(&d.Decided); err != nil {
				return h, err
			}
			if ended != "" {
				return h, fail("a decision (proposal %d) follows %s", d.Seq, ended)
			}
			if d.Seq <= h.LastDecided {
				return h, fail("proposal numbers do not increase: %d follows %d", d.Seq, h.LastDecided)
			}
			h.LastDecided = d.Seq
			if d.ActionType != domain.ActionFinalReview {
				if agentEnded != "" {
					return h, fail("a decision (proposal %d) follows %s", d.Seq, agentEnded)
				}
				at := slices.IndexFunc(pending, func(r *Request) bool {
					if d.RequestEntry != "" {
						return r.Entry == d.RequestEntry
					}
					return r.ActionType == d.ActionType // a chain from before request_entry: the oldest of its kind
				})
				if at < 0 || pending[at].ActionType != d.ActionType {
					return h, fail("the decision of proposal %d (%s) has no request before it", d.Seq, d.ActionType)
				}
				d.Request = pending[at]
				pending = slices.Delete(pending, at, at+1)
				if why := panelDecisionRefused(h, d); why != "" {
					return h, fail("%s", why)
				}
			}
			h.Decisions = append(h.Decisions, d)
		case "phase_checked":
			// read again when an awaiting_review or a delivery_authorized names it
			h.end.checked, h.end.checkedEntry, h.end.check, reviewedSince = n, e.EventHash, PhaseChecked{}, false
			if err := read(&h.end.check); err != nil {
				return h, err
			}
		case "delivery_authorized":
			if h.Authorized != nil {
				return h, fail("delivery is already authorized")
			}
			p := DeliveryAuthorized{Index: n, Entry: e.EventHash}
			if err := read(&p); err != nil {
				return h, err
			}
			if why := authorizationRefused(h, events[:i], p, reviewedSince); why != "" {
				return h, fail("%s", why)
			}
			h.Authorized = &p
			if h.Sealed == "" {
				h.Sealed = e.EventType
			}
		case "awaiting_review":
			reviewedSince = true
			rv := &Review{Index: n, Entry: e.EventHash}
			if err := read(&rv.AwaitingReview); err != nil {
				return h, err
			}
			for _, c := range events[:i] {
				if c.EventType == "phase_checked" && c.EventHash == rv.PhaseChecked {
					rv.Checked = &PhaseChecked{}
					_ = json.Unmarshal([]byte(c.Payload), rv.Checked) // read above
				}
			}
			h.Review = rv
		case "review_accepted", "review_rejected":
			if err := read(&ReviewDecided{}); err != nil {
				return h, err
			}
			if h.Review != nil {
				h.Review.Decided = map[string]string{"review_accepted": "accepted", "review_rejected": "rejected"}[e.EventType]
			}
		case "commit_prepared", "certificate_issued", "run_recovered", "review_delivered":
			var p Delivered
			if err := read(&p); err != nil {
				return h, err
			}
			h.Delivered = append(h.Delivered, Delivery{e.EventType, p})
			if e.EventType != "commit_prepared" { // written after the branch moved: the commit is delivered
				h.end.commit = p.Commit
			}
			if e.EventType == "run_recovered" || e.EventType == "review_delivered" {
				h.end.evidence, h.end.recovered = p.EvidenceErrors, e.EventType == "run_recovered"
			}
			if h.Sealed == "" || h.Sealed == "delivery_authorized" { // naming the commit refines the seal an authorization made
				h.Sealed = e.EventType
			}
			if e.EventType == "commit_prepared" {
				agentEnded = firstOf(agentEnded, e.EventType)
			} else {
				ended = firstOf(ended, e.EventType)
			}
		}
		last = e.Timestamp
	}
	h.Elapsed += last.Sub(segStart)
	h.Undecided = len(pending)
	if len(events) > 0 {
		h.Head = events[len(events)-1].EventHash
	}
	return h, nil
}

// afterAuthorization are the only events a chain holds after delivery_authorized (ADR 0012, section 3), an allow-list: what a
// delivery writes once it is authorized, and nothing else, so an event type that is added later, or that nobody knows, is refused
// until it is placed here on purpose. They are the commit-naming (commit_prepared, certificate_issued, and run_recovered and
// recovery_repaired of a recovery), the evidence (check_ran, certificate_failed, evidence_failed), the end of the launch the agent
// had (launch_ended), the run's path (run_path) and a rollback of the delivered run (rolled_back, written by `staircase case
// rollback`). review_delivered is not here: a run that authorized its delivery offered no review.
var afterAuthorization = map[string]bool{
	"commit_prepared": true, "certificate_issued": true, "run_recovered": true, "recovery_repaired": true,
	"check_ran": true, "certificate_failed": true, "evidence_failed": true,
	"launch_ended": true, "run_path": true, "rolled_back": true,
}

// authorizationRefused says why p is not an authorization a run can have written, or "" when it is: it names the latest
// phase_checked entry before it, of the same tree, whose checks passed and that no offered review followed, and it is made
// in policy mode under the terms the run was bound to and the signed policy it loaded.
func authorizationRefused(h History, before []domain.RunEventLog, p DeliveryAuthorized, reviewedSince bool) string {
	named := slices.IndexFunc(before, func(e domain.RunEventLog) bool {
		return e.EventType == "phase_checked" && e.EventHash == p.PhaseChecked
	})
	switch {
	case named < 0:
		return fmt.Sprintf("the authorization names no phase_checked entry (%q)", p.PhaseChecked)
	case named+1 != h.end.checked:
		return "the authorization names a phase_checked entry that is not the latest: the checks it rests on are not the last that ran"
	case h.end.check.Tree != p.Tree:
		return fmt.Sprintf("the authorization is for tree %q, not the tree %q the checks ran on", p.Tree, h.end.check.Tree)
	case !h.end.check.Passed:
		return "the checks the authorization rests on did not pass"
	case reviewedSince:
		return "a review was offered after the checks the authorization rests on: a run that offers its change does not deliver it by policy"
	case p.Mode != ModePolicy:
		return fmt.Sprintf("the authorization is for mode %q, not %q", p.Mode, ModePolicy)
	case h.Bound.TermsSHA256 == "" || p.TermsSHA256 != h.Bound.TermsSHA256:
		return "the authorization names other terms than the run was bound to"
	case h.Policy == nil || !h.Policy.Signed:
		return "the authorization rests on a policy that was not signed"
	case p.Policy != h.Policy.Digest:
		return "the authorization names another policy than the one the run loaded"
	}
	return ""
}

// firstOf is the first of its arguments that is not empty.
func firstOf(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

// OpenLaunch is the last launch when no outcome of it is on the chain: its process was lost before it could record one. nil
// otherwise.
func (h History) OpenLaunch() *Launch {
	if n := len(h.Launches); n > 0 && h.Launches[n-1].Outcome == "" {
		return &h.Launches[n-1]
	}
	return nil
}

// LastChecked is the latest phase_checked entry on the chain (its hash) and what it found: the checks of an away run on the
// approved tree. ok is false when the chain holds none.
func (h History) LastChecked() (entry string, c PhaseChecked, ok bool) {
	return h.end.checkedEntry, h.end.check, h.end.checked > 0
}

// Failures are the launches that did not end well, each with its class, in chain order.
func (h History) Failures() []Launch {
	var out []Launch
	for _, l := range h.Launches {
		if l.Class != "" {
			out = append(out, l)
		}
	}
	return out
}

// launchNote is the last launch's class in words, for an outcome's reason: "" when it ended well.
func (h History) launchNote() string {
	if n := len(h.Launches); n > 0 && h.Launches[n-1].Class != "" {
		return fmt.Sprintf("its agent's launch %d ended in %s", h.Launches[n-1].Attempt, ClassPhrase(h.Launches[n-1].Class))
	}
	return ""
}

// MaxLostLaunches is how many launches of one run may end without an established end (lost, or unresponsive) before the run is
// not launched again: what it approved can still be recovered, but its agent is not started again.
const MaxLostLaunches = 3

// Attempts is how many times the run's agent was launched: the launches claimed on its chain, a claim whose agent never started
// included. A chain from before launches were claimed has none, and counts its segments.
func (h History) Attempts() int {
	if len(h.Launches) > 0 {
		return len(h.Launches)
	}
	return h.Segment
}

// Lost is how many of the run's launches have no established end (an open claim included).
func (h History) Lost() int {
	n := 0
	for _, l := range h.Launches {
		if l.Lost() {
			n++
		}
	}
	return n
}

// LaunchParked is the attempt number of the run's last launch when its end was never established and no person has acknowledged
// it (launch_acknowledged, or the resume that continued it): the run is not launched again without a person. 0 when the run is
// not parked for this.
func (h History) LaunchParked() int {
	if n := len(h.Launches); n > 0 && h.Launches[n-1].Lost() && !h.Launches[n-1].Acked {
		return h.Launches[n-1].Attempt
	}
	return 0
}

// Approvals are the approved file changes, in chain order: what reaches the branch.
func (h History) Approvals() []Decision {
	var out []Decision
	for _, d := range h.Decisions {
		if d.Approved && d.ActionType == domain.ActionFileEdit {
			out = append(out, d)
		}
	}
	return out
}

// Unconsumed are the journal lines of approvals that expired between the journal and the chain: proposal number -> the
// request's SHA-256. The chain says they were rejected; their journal line is neither an approval nor corruption.
func (h History) Unconsumed() map[int]string {
	out := map[int]string{}
	for _, d := range h.Decisions {
		if d.Unconsumed && !d.Approved {
			out[d.Seq] = d.RequestSHA256
		}
	}
	return out
}

// FinalReviewed reports whether a person approved, in a final review made after the last approval, exactly the state whose
// digest is files; it is an error when such a review approved other bytes. A review before a later approval is history: it
// says nothing about the approvals as they are now (ADR 0010, Q18).
func (h History) FinalReviewed(files map[string]string) (bool, error) {
	after := 0
	if a := h.Approvals(); len(a) > 0 {
		after = a[len(a)-1].Index
	}
	reviewed := false
	for _, d := range h.Decisions {
		if d.ActionType != domain.ActionFinalReview || !d.Approved || d.Index < after {
			continue
		}
		if !maps.Equal(d.Files, files) {
			return false, errors.New("the audit chain records a final review that approved other bytes than the approvals derive")
		}
		reviewed = true
	}
	return reviewed, nil
}

// Decision is the decision of proposal seq.
func (h History) Decision(seq int) (Decision, bool) {
	i := slices.IndexFunc(h.Decisions, func(d Decision) bool { return d.Seq == seq })
	if i < 0 {
		return Decision{}, false
	}
	return h.Decisions[i], true
}

// Adopted are the parked proposals an owner took over: approved decisions that name them (ADR 0007), by number, with the
// position of the adopting decision.
func (h History) Adopted() map[int]int {
	out := map[int]int{}
	for _, d := range h.Decisions {
		if d.Approved && d.Adopts > 0 {
			out[d.Adopts] = d.Index
		}
	}
	return out
}

// Parked are the proposals parked for the owner that the owner has not adopted.
func (h History) Parked() []Decision {
	adopted := h.Adopted()
	var out []Decision
	for _, d := range h.Decisions {
		if _, ok := adopted[d.Seq]; d.Source == "parked" && !ok {
			out = append(out, d)
		}
	}
	return out
}

// ReviewedSinceAdoption reports whether a review was offered after the owner adopted parked proposal seq: the adoption finished.
func (h History) ReviewedSinceAdoption(seq int) bool {
	at, ok := h.Adopted()[seq]
	return ok && h.Review != nil && h.Review.Index > at
}

// AutoApproved is how many proposals a policy rule approved (the policy's max_auto_approved counts them).
func (h History) AutoApproved() int {
	n := 0
	for _, d := range h.Decisions {
		if d.Approved && d.Source == "policy" && d.ActionType != domain.ActionFinalReview {
			n++
		}
	}
	return n
}

// TaskApproved reports whether something was approved as part of the agreed task or on evidence: a person reviews the result.
func (h History) TaskApproved() bool {
	return slices.ContainsFunc(h.Decisions, func(d Decision) bool {
		return d.Approved && (d.Source == "task" || d.Source == "evidence") && d.ActionType != domain.ActionFinalReview
	})
}

// ValidatorState is the reviewer model's state as the last decision that recorded it left it; nil when none did.
func (h History) ValidatorState() json.RawMessage {
	var st json.RawMessage
	for _, d := range h.Decisions {
		if d.ActionType != domain.ActionFinalReview && len(d.ValidatorState) > 0 && string(d.ValidatorState) != "null" {
			st = d.ValidatorState
		}
	}
	return st
}

// Supervised are the decisions the drift supervisor saw, as the live run showed them to it (ADR 0010, Q20): not a final review,
// not a declared command or tool (decided before the drift check), not a proposal stAirCase refused before deciding it. An
// approval that expired was checked and is replayed: it carries the paths it touched.
func (h History) Supervised() []Decision {
	var out []Decision
	for _, d := range h.Decisions {
		switch {
		case d.ActionType == domain.ActionFinalReview, d.Source == "declared":
		case d.Source == "orchestrator" && len(d.Paths) == 0:
		default:
			out = append(out, d)
		}
	}
	return out
}

// Delivers reports whether the chain holds an event of that type naming commit.
func (h History) Delivers(event, commit string) bool {
	return slices.ContainsFunc(h.Delivered, func(d Delivery) bool { return d.Event == event && d.Commit == commit })
}

// Holds reports whether entry is on the chain.
func (h History) Holds(entry string) bool { return entry != "" && h.entries[entry] }

// Rows are a run's entries as they are stored, for the commands that show a chain, and what checking their hashes said of each.
// Nothing decides from them: the history of a run is Read, which refuses a chain that does not verify.
type Rows struct {
	Entries []domain.RunEventLog
	// Broken has one element for each entry: nil where its hash is the one it should carry, else why not. A broken entry does not
	// hide the ones after it.
	Broken []error
}

// Intact says whether every hash matched.
func (r Rows) Intact() bool {
	for _, err := range r.Broken {
		if err != nil {
			return false
		}
	}
	return true
}

// First is the first broken entry's error, nil when the chain is intact.
func (r Rows) First() error {
	for _, err := range r.Broken {
		if err != nil {
			return err
		}
	}
	return nil
}

// ReadRows reads a run's entries as stored and checks them in the same read, so what is shown and what is said of it are one chain.
// It fails only when the rows cannot be listed: a tampered chain is returned, marked, so it can be shown.
func ReadRows(store *persistence.Store, runID int64) (Rows, error) {
	entries, err := store.ListEventLogs(runID)
	if err != nil {
		return Rows{}, err
	}
	return Rows{Entries: entries, Broken: chain.Check(entries)}, nil
}

// voteRefused says why v is not a vote a run can have written, or "" when it is: the run declared a panel, v is by a model on it,
// it names a request waiting for its decision, and the model has not voted on that request already.
func voteRefused(h History, pending []*Request, v ValidatorVoted) string {
	switch {
	case len(h.Bound.ValidatorModels) == 0:
		return fmt.Sprintf("%s voted, and the run declared no panel of reviewer models", v.Model)
	case !slices.Contains(h.Bound.ValidatorModels, v.Model):
		return fmt.Sprintf("%s voted and is not on the run's panel (%s)", v.Model, strings.Join(h.Bound.ValidatorModels, ", "))
	case v.Verdict != VoteApprove && v.Verdict != VoteReject && v.Verdict != VoteError:
		return fmt.Sprintf("the vote of %s is %q, which is not approve, reject or error", v.Model, v.Verdict)
	case !slices.ContainsFunc(pending, func(r *Request) bool { return r.Entry == v.RequestEntry }):
		return fmt.Sprintf("the vote of %s names no request that waits for a decision", v.Model)
	case slices.ContainsFunc(h.Votes, func(o Vote) bool { return o.RequestEntry == v.RequestEntry && o.Model == v.Model }):
		return fmt.Sprintf("%s voted twice on the same proposal", v.Model)
	}
	return ""
}

// panelDecisionRefused says why d is not a decision by the reviewer models that a run can have written, or "" when it is: a decision
// by `validator:` of a run that declared a panel is by that whole panel, and rests on one vote from each model on its request, every
// one agreeing with it. A decision by a person after a split or a failed model is not one of these, and votes that no decision by the
// panel used (the sampled review) are allowed.
func panelDecisionRefused(h History, d Decision) string {
	models := h.Bound.ValidatorModels
	if len(models) == 0 || !strings.HasPrefix(d.Source, "validator:") {
		return ""
	}
	if want := "validator:" + strings.Join(models, "+"); d.Source != want {
		return fmt.Sprintf("proposal %d was decided by %s, and the run's panel is %s", d.Seq, d.Source, want)
	}
	want := VoteReject
	if d.Approved {
		want = VoteApprove
	}
	voted := map[string]string{}
	for _, v := range h.VotesOn(d.RequestEntry) {
		voted[v.Model] = v.Verdict
	}
	for _, m := range models {
		switch got, ok := voted[m]; {
		case !ok:
			return fmt.Sprintf("proposal %d was decided by the reviewer models and %s has no vote on it", d.Seq, m)
		case got != want:
			return fmt.Sprintf("proposal %d was decided as %s by the reviewer models and %s voted %s", d.Seq, want, m, got)
		}
	}
	return ""
}
