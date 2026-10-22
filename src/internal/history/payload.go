package history

import (
	"encoding/json"
	"fmt"

	"github.com/b070nd/stAirCase/src/internal/domain"
)

// The payloads of the events a reader uses, with the JSON keys runs write (ADR 0010). A key is never renamed: chains written
// before and after read the same, and payload_test.go pins each one.

// Bound is run_bound: what a run started from, before its agent ran.
type Bound struct {
	BaseSHA         string            `json:"base_sha"`
	Branch          string            `json:"branch"`
	Worktree        string            `json:"worktree"`
	TopologyVersion int               `json:"topology_version"`
	PlanDigest      string            `json:"plan_digest,omitempty"`
	BlueprintHash   string            `json:"blueprint_hash,omitempty"`
	Harness         string            `json:"harness,omitempty"`
	AckDrift        int64             `json:"ack_drift,omitempty"` // the halted run whose drift the person acknowledged
	AwayBrief       *AwayBrief        `json:"away_brief,omitempty"`
	AwayCommands    *DeclaredCommands `json:"away_commands,omitempty"`
	AwayTools       *DeclaredTools    `json:"away_tools,omitempty"`
	TermsSHA256     string            `json:"terms_sha256,omitempty"` // the saved options' digest; absent before ADR 0010
	// A hosted session resumed after its earlier run was decided (ADR 0008): the session, and the run it follows.
	ResumesSession string `json:"resumes_session,omitempty"`
	ResumesRun     int64  `json:"resumes_run,omitempty"`
}

// AwayBrief is the brief an away run's agent was given (ADR 0008).
type AwayBrief struct {
	Version int    `json:"version"`
	SHA256  string `json:"sha256"`
}

// DeclaredCommands are the commands an away run may run without asking (ADR 0009).
type DeclaredCommands struct {
	Commands []string `json:"commands"`
	SHA256   string   `json:"sha256"`
}

// DeclaredTools are the MCP tools an away run may call without asking.
type DeclaredTools struct {
	Tools  []string `json:"tools"`
	SHA256 string   `json:"sha256"`
}

// Request is yield_request: a proposal as the agent made it. A request too large for the chain is kept as its kind, size and
// digest only (Truncated).
type Request struct {
	domain.YieldRequest
	Truncated bool `json:"truncated,omitempty"`

	Index int    `json:"-"` // its position on the chain, from 1
	Entry string `json:"-"` // its entry hash
}

// Decided is yield_decided: the decision on a proposal, or a final review.
type Decided struct {
	Seq           int               `json:"seq"`
	Source        string            `json:"source"`
	Agent         string            `json:"agent"`
	ActionType    string            `json:"action_type"`
	Approved      bool              `json:"approved"`
	Feedback      string            `json:"feedback"`
	RequestSHA256 string            `json:"request_sha256"`
	BaseSHA       string            `json:"base_sha"`
	RequestEntry  string            `json:"request_entry,omitempty"` // the chain entry of its request; absent before ADR 0010
	Files         map[string]string `json:"files,omitempty"`         // the digest of exactly the approved (or offered) state
	Paths         []string          `json:"paths,omitempty"`         // what the proposal touched
	Drift         string            `json:"drift,omitempty"`
	Guard         string            `json:"guard,omitempty"`
	ReviewAfter   bool              `json:"review_after,omitempty"`
	Sandboxed     bool              `json:"sandboxed,omitempty"`
	Unconsumed    bool              `json:"unconsumed,omitempty"` // an approval journaled, then expired before the chain
	ParkedWhy     string            `json:"parked_why,omitempty"`
	Adopts        int               `json:"adopts,omitempty"` // the parked proposal an owner's approval takes over (ADR 0007)
	DecideMS      *int64            `json:"decide_ms,omitempty"`
	Lines         int               `json:"lines,omitempty"`
	Signed
	// Varying by source, kept as written (ADR 0010, Q8).
	Evidence       json.RawMessage `json:"evidence,omitempty"`
	ValidatorState json.RawMessage `json:"validator_state,omitempty"`
}

// Signed is what a decision records about a person's signature on it (signed.go): who signed, with which key, the text, and
// whether the signer is trusted; or why the signature was refused.
type Signed struct {
	Signature        string `json:"signature,omitempty"`
	SignedBy         string `json:"signed_by,omitempty"`
	Key              string `json:"key,omitempty"`
	Trusted          *bool  `json:"trusted,omitempty"` // present whenever a signature is: an untrusted one says so
	SignedText       string `json:"signed_text,omitempty"`
	SignatureRefused string `json:"signature_refused,omitempty"`
}

// Encode is the payload of an event as it is put on the chain: v's fields and the event's type, keys in order.
func Encode(event string, v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &fields); err != nil {
		return "", fmt.Errorf("the payload of %s is not a JSON object: %w", event, err)
	}
	fields["type"], _ = json.Marshal(event)
	b, err = json.Marshal(fields)
	return string(b), err
}

// Resumed is run_resumed: a continuation began (ADR 0005).
type Resumed struct {
	Segment    int             `json:"segment"`
	Generation string          `json:"generation"`
	Approvals  json.RawMessage `json:"approvals"`
	NextSeq    int             `json:"next_seq"`
	Policy     string          `json:"policy"`
	PlanDigest string          `json:"plan_digest"`
	Undecided  int             `json:"undecided_requests"`
	AckDrift   bool            `json:"ack_drift"`
	Elapsed    int             `json:"elapsed_secs"`
	Context    string          `json:"context"`
	// JournalDropped is how many journal lines the continuation dropped: never consumed, past the chain's last decision.
	JournalDropped int `json:"journal_dropped,omitempty"`
	// AckUnknown is the launch whose end was never established that a person acknowledged before this continuation.
	AckUnknown int `json:"ack_unknown,omitempty"`
	// WithdrewReview is the awaiting_review entry a resumed hosted session withdrew (ADR 0008).
	WithdrewReview string `json:"withdrew_review,omitempty"`
}

// LaunchClaimed is launch_claimed: an agent launch, put on the chain before the agent's process starts. Attempt counts the
// run's launches from 1; Generation names this launch alone, so its outcome can name it.
type LaunchClaimed struct {
	Attempt    int    `json:"attempt"`
	Generation string `json:"generation"`
	Segment    int    `json:"segment"`
}

// LaunchEnded is launch_ended: how a claimed launch ended, written once its agent stopped, or by a restart that found none.
type LaunchEnded struct {
	Attempt    int    `json:"attempt"`
	Generation string `json:"generation"`
	Outcome    string `json:"outcome"`
	// Class is the lifecycle failure class of a launch that did not end well (see ClassOf), "" when it ended by itself or the run
	// stopped it. A chain from before classes has none; Launch.Class derives it from the outcome.
	Class string `json:"class,omitempty"`
}

// LaunchAcknowledged is launch_acknowledged: a person's acknowledgement of a launch whose end was never established (ADR 0005,
// amended), written without launching anything. It names the exact launch, so it cannot cover a later loss.
type LaunchAcknowledged struct {
	Attempt    int    `json:"attempt"`
	Generation string `json:"generation"`
}

// The outcomes of a launch. The first three are established ends; the last two are not.
const (
	LaunchExited       = "exited"                // the agent ended by itself
	LaunchFailed       = "failed"                // the agent ended with an error
	LaunchStopped      = "stopped"               // the run stopped the agent (cancelled, halted, over its budget or its time)
	LaunchUnresponsive = "unresponsive"          // the run asked the agent to stop and it did not, in time
	LaunchUnknown      = "unknown_after_restart" // no outcome was recorded: a restart found the claim open
)

// The lifecycle failure classes of a launch (V11-C01 scope 8, after Cloudroom's error envelope): why an agent's launch did not end
// well, apart from anything its checks or its reviewer decided. A class is about the agent's process and the records of it; it is
// never a check result and never acceptance. Each belongs to the outcomes that can carry it (see classFits).
const (
	ClassRejected          = "rejected"           // the agent was refused or never ran governed: it did not start, or its hooks never arrived
	ClassHostExit          = "host_exit"          // the agent's process ended with an error
	ClassStorage           = "storage"            // a record of the launch could not be written while the agent ran
	ClassCleanup           = "cleanup"            // the agent did not stop when asked, so its end is not established
	ClassUncertainDispatch = "uncertain_dispatch" // the launch was claimed and no outcome was recorded: nobody knows what the agent did
)

// ClassOf is the class a launch's outcome implies when its launch_ended names none (a chain from before classes, or a restart's
// record): "" for an outcome that is not a lifecycle failure.
func ClassOf(outcome string) string {
	switch outcome {
	case LaunchFailed:
		return ClassHostExit
	case LaunchUnresponsive:
		return ClassCleanup
	case LaunchUnknown:
		return ClassUncertainDispatch
	}
	return ""
}

// classFits says whether a launch that ended with outcome can have class.
func classFits(class, outcome string) bool {
	switch class {
	case ClassRejected, ClassHostExit:
		return outcome == LaunchFailed
	case ClassCleanup:
		return outcome == LaunchUnresponsive
	case ClassUncertainDispatch:
		return outcome == LaunchUnknown
	case ClassStorage: // the launch ended in some way the run established, and a record of it was lost
		return outcome == LaunchExited || outcome == LaunchFailed || outcome == LaunchStopped
	}
	return false
}

// ClassPhrase is a class in the words a person reads.
func ClassPhrase(class string) string {
	switch class {
	case ClassRejected:
		return "rejected (the agent was refused or never ran governed)"
	case ClassHostExit:
		return "a host exit (the agent's process ended with an error)"
	case ClassStorage:
		return "storage trouble (a record of the launch could not be written)"
	case ClassCleanup:
		return "a cleanup failure (the agent did not stop when asked)"
	case ClassUncertainDispatch:
		return "an uncertain dispatch (the launch has no known end: nobody knows what the agent did)"
	}
	return class
}

// PolicySnapshot is the policy a run decided under.
type PolicySnapshot struct {
	Digest string `json:"digest"`
	Signed bool   `json:"signed"`
}

// InitiatorSigned is the signed request of the person who started the run.
type InitiatorSigned struct {
	Principal string `json:"principal"`
	Signature string `json:"signature"`
}

// AgentSession is an agent harness's own session, so a later segment can resume it.
type AgentSession struct {
	Agent string `json:"agent"`
	ID    string `json:"id"`
	Mode  string `json:"mode"`
}

// StateEmit is one model step's usage.
type StateEmit struct {
	ActiveAgent string `json:"active_agent"`
	State       struct {
		Model string `json:"model"`
		In    int    `json:"input_tokens"`
		Out   int    `json:"output_tokens"`
	} `json:"state"`
}

// ShellRan is an approved command that ran.
type ShellRan struct {
	Agent     string `json:"agent"`
	Command   string `json:"command"`
	Sandboxed bool   `json:"sandboxed"`
}

// Check is one check's result, as phase_checked records it.
type Check struct {
	Command      string `json:"command"`
	ExitCode     int    `json:"exit_code"`
	Sandboxed    bool   `json:"sandboxed"`
	OutputSHA256 string `json:"output_sha256"`
}

// PhaseChecked is an away run's checks on the exact approved tree (ADR 0006).
type PhaseChecked struct {
	Tree   string  `json:"tree"`
	Checks []Check `json:"checks"`
	Passed bool    `json:"passed"`
	Failed string  `json:"failed"`
}

// AwaitingReview is the review an away run offers its owner.
type AwaitingReview struct {
	Tree         string            `json:"tree"`
	Files        map[string]string `json:"files"`
	PhaseChecked string            `json:"phase_checked"` // the entry of the checks it rests on
	Parked       int               `json:"parked"`
	// DeliveryRefused is, for a run that was to deliver by policy and fell back to the owner, the condition that sent it (ADR 0012,
	// section 2): absent for a run that offers its change as a matter of course.
	DeliveryRefused string `json:"delivery_refused,omitempty"`
}

// ModePolicy is the delivery mode of a delivery_authorized: the change was delivered by a rule of the owner's signed policy, with
// no person reviewing the whole change (ADR 0012).
const ModePolicy = "policy"

// DeliveryAuthorized is delivery_authorized (ADR 0012): an away run in policy delivery mode found every condition of delivering
// without a person met, and says on what: the phase_checked entry whose checks passed on Tree, the digest of exactly the approved
// files, the digest of the terms the run was bound to, the digest of the signed policy whose rules approved them, and the mode.
type DeliveryAuthorized struct {
	PhaseChecked string            `json:"phase_checked"`
	Tree         string            `json:"tree"`
	Files        map[string]string `json:"files"`
	TermsSHA256  string            `json:"terms_sha256"`
	Policy       string            `json:"policy"`
	Mode         string            `json:"mode"`

	Index int    `json:"-"` // its position on the chain, from 1
	Entry string `json:"-"` // its entry hash
}

// ReviewDecided is review_accepted or review_rejected: the owner's decision on an offered review.
type ReviewDecided struct {
	AwaitingReview string `json:"awaiting_review"`
	Tree           string `json:"tree,omitempty"` // the tree an acceptance accepted
	Feedback       string `json:"feedback"`
	// Empty marks an acceptance of a change that approved nothing: it delivers nothing and the run ends FAILED.
	Empty bool `json:"empty,omitempty"`
}

// Delivered is commit_prepared, certificate_issued, run_recovered or review_delivered: what a commit-naming event names.
type Delivered struct {
	Commit         string   `json:"commit"`
	Base           string   `json:"base,omitempty"`
	Tree           string   `json:"tree,omitempty"`
	Branch         string   `json:"branch,omitempty"`
	CAL            int      `json:"cal,omitempty"`
	EnvelopeSHA256 string   `json:"envelope_sha256,omitempty"`
	Proposals      int      `json:"proposals,omitempty"`
	FinalReview    bool     `json:"final_review,omitempty"`
	Forced         bool     `json:"forced,omitempty"`
	EvidenceErrors []string `json:"evidence_errors,omitempty"`
}

// Ended is run_path: the phases a segment of the run went through, written when its process ends the segment, and the status it
// ended with (absent on chains written before the status was recorded).
type Ended struct {
	Phases []string `json:"phases"`
	Status string   `json:"status,omitempty"`
}

// DriftHalt is drift_halt: drift supervision stopped the run, and why.
type DriftHalt struct {
	Reason string `json:"reason"`
}

// EvidenceFailed is evidence_failed: the run's commit is delivered, and what of its evidence could not be written.
type EvidenceFailed struct {
	Commit string   `json:"commit"`
	Errors []string `json:"errors"`
}

// CertificateFailed is certificate_failed: the run's commit is delivered, and its certificate could not be written.
type CertificateFailed struct {
	Commit string `json:"commit"`
	Error  string `json:"error"`
}
