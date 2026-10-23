package history

import (
	"encoding/json"

	"github.com/b070nd/stAirCase/src/internal/domain"
)

// The events a run writes (ADR 0014). Each type carries the name it is written under; its JSON keys are the payload, as runs have
// always written them, and the fixture of internal/chain pins the bytes. A type that is also read (Bound, Decided and the others of
// payload.go) is the one type for both; the ones below are written, and a reader that needs one of them reads it as a new type.

func (Bound) eventName() string              { return "run_bound" }
func (Request) eventName() string            { return "yield_request" }
func (Decided) eventName() string            { return "yield_decided" }
func (Resumed) eventName() string            { return "run_resumed" }
func (LaunchClaimed) eventName() string      { return "launch_claimed" }
func (LaunchEnded) eventName() string        { return "launch_ended" }
func (LaunchAcknowledged) eventName() string { return "launch_acknowledged" }
func (PolicySnapshot) eventName() string     { return "policy_snapshot" }
func (InitiatorSigned) eventName() string    { return "initiator_signed" }
func (AgentSession) eventName() string       { return "agent_session" }
func (StateEmit) eventName() string          { return "state_emit" }
func (ShellRan) eventName() string           { return "shell_ran" }
func (PhaseChecked) eventName() string       { return "phase_checked" }
func (AwaitingReview) eventName() string     { return "awaiting_review" }
func (DeliveryAuthorized) eventName() string { return "delivery_authorized" }
func (Ended) eventName() string              { return "run_path" }
func (DriftHalt) eventName() string          { return "drift_halt" }
func (EvidenceFailed) eventName() string     { return "evidence_failed" }
func (CertificateFailed) eventName() string  { return "certificate_failed" }

// ReviewAccepted and ReviewRejected are review_accepted and review_rejected: the owner's decision on an offered review.
type (
	ReviewAccepted ReviewDecided
	ReviewRejected ReviewDecided
)

func (ReviewAccepted) eventName() string { return "review_accepted" }
func (ReviewRejected) eventName() string { return "review_rejected" }

// TaskAgreed is task_agreed: who agreed to the task, and the plan they agreed to.
type TaskAgreed struct {
	By         string `json:"by"`
	PlanDigest string `json:"plan_digest,omitempty"`
}

func (TaskAgreed) eventName() string { return "task_agreed" }

// CommitPrepared is commit_prepared: the commit a run is about to name, on which base, with which tree, on which branch.
type CommitPrepared struct {
	Commit string `json:"commit"`
	Base   string `json:"base"`
	Tree   string `json:"tree"`
	Branch string `json:"branch"`
}

func (CommitPrepared) eventName() string { return "commit_prepared" }

// CertificateIssued is certificate_issued: the commit's change certificate, at its level, and the digest of its envelope.
type CertificateIssued struct {
	Commit         string `json:"commit"`
	CAL            int    `json:"cal"`
	EnvelopeSHA256 string `json:"envelope_sha256"`
}

func (CertificateIssued) eventName() string { return "certificate_issued" }

// RunRecovered is run_recovered and ReviewDelivered is review_delivered: the commit that recovery, or the delivery of an accepted
// review, made from what the chain says was approved, and what of its evidence could not be written.
type RunRecovered struct {
	Commit         string   `json:"commit"`
	EvidenceErrors []string `json:"evidence_errors"`
	FinalReview    bool     `json:"final_review"`
	Forced         bool     `json:"forced"`
	Proposals      int      `json:"proposals"`
}

type ReviewDelivered RunRecovered

func (RunRecovered) eventName() string    { return "run_recovered" }
func (ReviewDelivered) eventName() string { return "review_delivered" }

// RecoveryRepaired is recovery_repaired: a recovery found its commit already made and repaired the evidence of it.
type RecoveryRepaired struct {
	Commit         string   `json:"commit"`
	EvidenceErrors []string `json:"evidence_errors"`
}

func (RecoveryRepaired) eventName() string { return "recovery_repaired" }

// DriftReport is drift_report: what drift supervision saw over the run. The report is kept as the supervisor wrote it (ADR 0010, Q8).
type DriftReport struct {
	Report json.RawMessage `json:"report"`
}

func (DriftReport) eventName() string { return "drift_report" }

// AgentUnresponsive is agent_unresponsive: the agent did not stop within the grace the run gave it after cancellation.
type AgentUnresponsive struct {
	GraceSeconds float64 `json:"grace_seconds"`
}

func (AgentUnresponsive) eventName() string { return "agent_unresponsive" }

// ApprovalPathEscape is approval_path_escape: an approved proposal named a path outside the worktree.
type ApprovalPathEscape struct {
	Agent  string `json:"agent"`
	Detail string `json:"detail"`
}

func (ApprovalPathEscape) eventName() string { return "approval_path_escape" }

// Violation is what a check of the worktree found that no approval covers, with the file it is about when there is one.
type Violation struct {
	Detail string `json:"detail"`
	File   string `json:"file,omitempty"`
}

// ApprovalContentMismatch, RunBranchMoved and UnapprovedWorktreeChange are the events of the three violations: the worktree does not
// hold what the approvals derive, the run branch is not at its base, or something changed that nobody approved.
type (
	ApprovalContentMismatch  Violation
	RunBranchMoved           Violation
	UnapprovedWorktreeChange Violation
)

func (ApprovalContentMismatch) eventName() string  { return "approval_content_mismatch" }
func (RunBranchMoved) eventName() string           { return "run_branch_moved" }
func (UnapprovedWorktreeChange) eventName() string { return "unapproved_worktree_change" }

// SignalRated is signal_rated: the decision model's rating of a change; SignalFailed is the same event when the model gave none.
type SignalRated struct {
	CostUSD     float64 `json:"cost_usd"`
	Kind        string  `json:"kind"`
	Model       string  `json:"model"`
	Risky       float64 `json:"risky"`
	ServesStory float64 `json:"serves_story"`
}

type SignalFailed struct {
	Error string `json:"error"`
	Model string `json:"model"`
}

func (SignalRated) eventName() string  { return "signal_rated" }
func (SignalFailed) eventName() string { return "signal_rated" }

// CheckRan is check_ran: one check run on the commit a run made.
type CheckRan struct {
	Command      string `json:"command"`
	ExitCode     int    `json:"exit_code"`
	OutputSHA256 string `json:"output_sha256"`
	Sandboxed    bool   `json:"sandboxed"`
	// Environment names the declared directories the check saw, read-only; absent when it saw none.
	Environment []string `json:"environment,omitempty"`
}

func (CheckRan) eventName() string { return "check_ran" }

// DoneChecked is done_checked: the agent said it was done and the run's checks ran on a copy of its work, the commands that failed
// among them; DoneCheckFailed is the same event when staircase could not run them.
type DoneChecked struct {
	Attempt int      `json:"attempt"`
	Failed  []string `json:"failed"`
}

type DoneCheckFailed struct {
	Error string `json:"error"`
}

func (DoneChecked) eventName() string     { return "done_checked" }
func (DoneCheckFailed) eventName() string { return "done_checked" }

// ShellExecRejected is shell_exec_rejected: a command the run refused before it was ever put to a decision, as the agent proposed it.
type ShellExecRejected struct{ domain.YieldRequest }

func (ShellExecRejected) eventName() string { return "shell_exec_rejected" }

// RolledBack is rolled_back: `staircase case rollback` deleted the run's branch.
type RolledBack struct {
	Actor  string `json:"actor"`
	Branch string `json:"branch"`
}

func (RolledBack) eventName() string { return "rolled_back" }

// The verdicts of a reviewer model (ValidatorVoted.Verdict).
const (
	VoteApprove = "approve"
	VoteReject  = "reject"
	VoteError   = "error" // the model gave none: it failed, or its reply was not a verdict
)

// ValidatorVoted is validator_voted: one reviewer model's verdict on one proposal, written before the decision it went into. Every
// model of the panel that was asked has one, whether they agreed or not and whether it gave a verdict or failed, so what a decision
// by `validator:` rested on can be read back (ADR 0014, section 3). The reason is the model's own, and the digest is that of its
// whole reply.
type ValidatorVoted struct {
	Model        string `json:"model"`
	RequestEntry string `json:"request_entry"`
	Verdict      string `json:"verdict"`
	Reason       string `json:"reason"`
	ReplySHA256  string `json:"reply_sha256,omitempty"`
}

func (ValidatorVoted) eventName() string { return "validator_voted" }

// StoryAccepted is story_accepted: a person accepted a user story after verifying the work a run delivered.
type StoryAccepted struct {
	Actor   string `json:"actor"`
	StoryID int64  `json:"story_id"`
}

func (StoryAccepted) eventName() string { return "story_accepted" }

// SourceDirty is source_dirty: the checkout a run was started from had changes that are not in the commit the run works on, so the
// agent did not have them. It holds the counts of the tracked changes and of the untracked files, and the digest of their names (a
// status letter and the path, sorted, one a line). Ignored paths are never listed: they are not in a worktree either, and git does not
// say which exist.
type SourceDirty struct {
	Tracked     int    `json:"tracked"`
	Untracked   int    `json:"untracked"`
	NamesSHA256 string `json:"names_sha256"`
}

func (SourceDirty) eventName() string { return "source_dirty" }
