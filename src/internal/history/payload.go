package history

import (
	"encoding/json"

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
	// A person's signature, when they signed (signed.go).
	Signature        string `json:"signature,omitempty"`
	SignedBy         string `json:"signed_by,omitempty"`
	Key              string `json:"key,omitempty"`
	Trusted          bool   `json:"trusted,omitempty"`
	SignedText       string `json:"signed_text,omitempty"`
	SignatureRefused string `json:"signature_refused,omitempty"`
	// Varying by source, kept as written (ADR 0010, Q8).
	Evidence       json.RawMessage `json:"evidence,omitempty"`
	ValidatorState json.RawMessage `json:"validator_state,omitempty"`
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
}

// ReviewDecided is review_accepted or review_rejected: the owner's decision on an offered review.
type ReviewDecided struct {
	AwaitingReview string `json:"awaiting_review"`
	Feedback       string `json:"feedback,omitempty"`
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
