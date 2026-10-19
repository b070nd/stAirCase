// Package history is the one replay of a run's audit chain (ADR 0010): what a verified chain says a run bound, proposed,
// decided, parked, adopted, offered and delivered, read in one walk. A chain that no run can have written has no history; it is
// refused with a GrammarError that names the entry. The package reads only verified chains (Read) and does nothing else: no git,
// no files.
package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/persistence"
)

// History is what a run's verified chain says about it.
type History struct {
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

	Launches    []Launch // every agent launch, in chain order
	ackedLaunch int      // the lost launch a resume acknowledged last

	Review *Review // the last review an away run offered
	// Sealed is the first entry that ended the agent's part of the run (commit_prepared) or the chain (certificate_issued,
	// run_recovered, review_delivered): "" while the run can still be continued.
	Sealed    string
	Delivered []Delivery // the commit-naming events, in chain order
	Head      string     // the last entry's hash

	entries map[string]bool
}

// Decision is one yield_decided with where it is and what it answered.
type Decision struct {
	Decided
	Index   int      // its position on the chain, from 1
	Entry   string   // its entry hash
	Request *Request // the request it used up; nil for a final review
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
}

// lost reports whether the launch's end was never established: no outcome, one a restart recorded, or an agent that did not stop.
func (l Launch) lost() bool {
	return l.Outcome == "" || l.Outcome == OutcomeUnknown || l.Outcome == OutcomeUnresponsive
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
	return h, nil
}

// replay is the fold. The grammar (ADR 0010, section 2): run_bound first and once; every decision but a final review uses up a
// request of the same segment and kind audited before it (the one it names, when it names one); proposal numbers increase;
// commit_prepared leaves room only for a final review; certificate_issued, run_recovered and review_delivered end the chain; launches
// are numbered in order, each outcome names the open launch, and nothing launches or resumes while a launch has no outcome. Event
// types and fields it does not know are ignored.
func replay(events []domain.RunEventLog) (History, error) {
	h := History{Segment: 1, entries: map[string]bool{}}
	var pending []*Request // this segment's requests not yet decided, in chain order
	agentEnded, ended := "", ""
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
		switch e.EventType {
		case "run_bound":
			if i > 0 {
				return h, fail("the audit history binds the run to a commit more than once")
			}
			if err := read(&h.Bound); err != nil {
				return h, err
			}
			segStart = e.Timestamp
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
			segStart = e.Timestamp
			h.Segment++
			pending = nil // a request an earlier segment left is not one this segment can decide
			if p.AckDrift {
				h.Halted = false
			}
			if p.AckUnknown > 0 {
				h.ackedLaunch = p.AckUnknown
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
			case OutcomeExited, OutcomeFailed, OutcomeStopped, OutcomeUnresponsive, OutcomeUnknown:
			default:
				return h, fail("an unknown outcome %q", p.Outcome)
			}
			o.Outcome = p.Outcome
			if p.Outcome == OutcomeUnknown {
				continue // written by a restart, after the run was down: not time the segment used
			}
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
			h.Halted = true
		case "yield_request":
			if agentEnded != "" {
				return h, fail("a request follows %s", agentEnded)
			}
			r := &Request{Index: n, Entry: e.EventHash}
			if err := read(r); err != nil {
				return h, err
			}
			pending = append(pending, r)
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
			}
			h.Decisions = append(h.Decisions, d)
		case "phase_checked":
			// read when an awaiting_review names it
			if err := read(&PhaseChecked{}); err != nil {
				return h, err
			}
		case "awaiting_review":
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
			if h.Sealed == "" {
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

// Lost is how many of the run's launches have no established end (an open claim included).
func (h History) Lost() int {
	n := 0
	for _, l := range h.Launches {
		if l.lost() {
			n++
		}
	}
	return n
}

// LaunchParked is the attempt number of the run's last launch when its end was never established and no resume has acknowledged
// it since: the run is not launched again without a person. 0 when the run is not parked for this.
func (h History) LaunchParked() int {
	if n := len(h.Launches); n > 0 && h.Launches[n-1].lost() && h.Launches[n-1].Attempt != h.ackedLaunch {
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
