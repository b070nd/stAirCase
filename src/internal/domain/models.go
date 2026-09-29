package domain

import (
	"time"
)

// ─── Status constants ─────────────────────────────────────────────────────────
// Using typed string constants rather than bare literals prevents silent typos
// from being stored in the database (e.g. "COMPLTEED" instead of "COMPLETED").

// Case status values - enforced by CHECK constraint on new databases.
const (
	CaseStatusPending   = "PENDING"
	CaseStatusRunning   = "RUNNING"
	CaseStatusCompleted = "COMPLETED"
	CaseStatusFailed    = "FAILED"
)

// Run status values - enforced by CHECK constraint on new databases.
const (
	RunStatusRunning = "RUNNING"
	RunStatusSuccess = "SUCCESS"
	RunStatusFailed  = "FAILED"
	RunStatusKilled  = "KILLED"
)

// UserStory status values - enforced by CHECK constraint on new databases.
const (
	StoryStatusPending     = "PENDING"
	StoryStatusImplemented = "IMPLEMENTED"
	StoryStatusInvalidated = "INVALIDATED"
)

type Vendor struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type Project struct {
	ID         int64  `json:"id"`
	VendorID   int64  `json:"vendor_id"`
	Name       string `json:"name"`
	SourcePath string `json:"source_path,omitempty"`
	WebhookURL string `json:"webhook_url,omitempty"`
}

type ProjectDependency struct {
	ID              int64 `json:"id"`
	SourceProjectID int64 `json:"source_project_id"`
	TargetProjectID int64 `json:"target_project_id"`
}

type Secret struct {
	ID                int64  `json:"id"`
	KeyName           string `json:"key_name"`
	EncryptedValue    string `json:"encrypted_value"`
	ScopedToProjectID *int64 `json:"scoped_to_project_id,omitempty"`
}

type Component struct {
	ID        int64  `json:"id"`
	ProjectID int64  `json:"project_id"`
	Name      string `json:"name"`
}

type Case struct {
	ID           int64      `json:"id"`
	ProjectID    int64      `json:"project_id"`
	Status       string     `json:"status"` // PENDING, RUNNING, COMPLETED, FAILED
	LastModified time.Time  `json:"last_modified"`
	PrdJSON      string     `json:"prd_json,omitempty"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
}

type UserStory struct {
	ID           int64  `json:"id"`
	CaseID       int64  `json:"case_id"`
	Description  string `json:"description"`
	Status       string `json:"status"` // PENDING, IMPLEMENTED, INVALIDATED
	CustomConfig string `json:"custom_config,omitempty"`
}

// Blueprint is an imported blueprint snapshot, immutable and identified by
// the sha256 of its canonical content.
type Blueprint struct {
	Hash       string    `json:"hash"`
	Name       string    `json:"name"`
	Content    string    `json:"content"`
	SourceDir  string    `json:"source_dir,omitempty"`
	GitSHA     string    `json:"git_sha,omitempty"` // the source repository's commit, when it was clean
	ImportedAt time.Time `json:"imported_at"`
}

type SwarmTopology struct {
	ID             int64  `json:"id"`
	ProjectID      int64  `json:"project_id"`
	Version        int    `json:"version"`
	SupervisorName string `json:"supervisor_name"`
	CheckpointType string `json:"checkpoint_type"`
	RuntimeType    string `json:"runtime_type"` // langgraph, crewai, autogen
}

type AgentNode struct {
	ID          int64  `json:"id"`
	TopologyID  int64  `json:"topology_id"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	Model       string `json:"model"`
	ComponentID *int64 `json:"component_id,omitempty"`
}

type AgentTool struct {
	ID         int64  `json:"id"`
	AgentID    int64  `json:"agent_id"`
	ToolName   string `json:"tool_name"`
	ToolConfig string `json:"tool_config,omitempty"`
}

type Edge struct {
	ID         int64  `json:"id"`
	TopologyID int64  `json:"topology_id"`
	FromNode   string `json:"from_node"`
	ToNode     string `json:"to_node"`
	Condition  string `json:"condition,omitempty"`
}

type Run struct {
	ID              int64      `json:"id"`
	CaseID          int64      `json:"case_id"`
	TopologyVersion int        `json:"topology_version"`
	Status          string     `json:"status"` // RUNNING, SUCCESS, FAILED, KILLED
	StartTime       time.Time  `json:"start_time"`
	EndTime         *time.Time `json:"end_time,omitempty"`
	GitBranch       string     `json:"git_branch"`
	GitCommitHash   string     `json:"git_commit_hash,omitempty"`
}

type RunEventLog struct {
	ID            int64     `json:"id"`
	RunID         int64     `json:"run_id"`
	EventType     string    `json:"event_type"`
	Payload       string    `json:"payload"`
	Timestamp     time.Time `json:"timestamp"`
	EventHash     string    `json:"event_hash"`
	GitCommitHash string    `json:"git_commit_hash,omitempty"`
}

// ─── HITL yield types ─────────────────────────────────────────────────────────
// Defined here so that packages that handle yield decisions (approvalhttp,
// policy, tui) do not have to import the ipc package, which also owns the
// low-level UDS server.  ipc re-exports these as type aliases.

// ProposedEdit describes one search-and-replace change the agent wants to make.
type ProposedEdit struct {
	File         string `json:"file"`
	SearchBlock  string `json:"search_block"`
	ReplaceBlock string `json:"replace_block"`
	// ContentHash is the runtime's own SHA-256 of the file after the edit. It is
	// advisory and never trusted: the orchestrator derives the approved content
	// from the base commit and the edits shown (see orchestrator/approval.go).
	ContentHash string `json:"content_hash,omitempty"`
}

// YieldRequest is an agent's proposal (an edit or a shell command) awaiting a
// decision: refusal, policy, or a human.
type YieldRequest struct {
	Type            string         `json:"type"` // "yield_request"
	AgentName       string         `json:"agent_name"`
	ActionType      string         `json:"action_type"` // "file_edit", "shell_exec", "custom"
	ProposedEdits   []ProposedEdit `json:"proposed_edits,omitempty"`
	ReasoningTrace  string         `json:"reasoning_trace"`
	ConfidenceScore float64        `json:"confidence_score"`

	// BatchID groups related yield requests to prevent TUI fatigue when an
	// agent proposes several edits in a single logical operation.
	// Phase 7 implementation: the server will buffer yields sharing the same
	// BatchID and present them as a single approval screen. For now the field
	// is accepted on the wire and displayed by the TUI but not yet batched.
	BatchID string `json:"batch_id,omitempty"`

	// ReviewAfter marks changes that already happened in the worktree (a
	// command made them): the orchestrator fills ProposedEdits with them,
	// approving keeps them and rejecting reverts them.
	ReviewAfter bool `json:"review_after,omitempty"`
	// Sandboxed, with ReviewAfter, says the changes come from an approved
	// command that ran in the sandbox: decided before it ran, so the run can
	// still reach CAL 3. Set by the orchestrator only.
	Sandboxed bool `json:"sandboxed,omitempty"`

	// Drift, set by the orchestrator, says why drift supervision sends this
	// proposal to a human (outside the stories' scope, a limit, a checkpoint).
	Drift string `json:"drift,omitempty"`

	// Guard, set by the orchestrator, says why a person must decide this
	// change even if a rule or the validator would approve it: it adds hidden
	// Unicode, changes dependencies or writes what looks like a secret.
	Guard string `json:"guard,omitempty"`

	// Review, set by the orchestrator, is the automated validator's note when
	// a human decides in its place (a sampled approval, repeated rejections,
	// a sensitive path, the validator unavailable).
	Review string `json:"review,omitempty"`

	// Before, set by the orchestrator, is the approved content of each file a
	// whole-file change replaces, so a person sees what changes (a line diff).
	Before map[string]string `json:"before,omitempty"`
}

// YieldResponse is the decision on a YieldRequest, returned to the agent.
type YieldResponse struct {
	Type     string `json:"type"` // "yield_response"
	Approved bool   `json:"approved"`
	Feedback string `json:"feedback,omitempty"`
}

// Action types of a YieldRequest.
const (
	ActionFileEdit    = "file_edit"    // create, edit or delete files
	ActionShellExec   = "shell_exec"   // run a shell command
	ActionFinalReview = "final_review" // a human approves a run's whole change
)

// Decide is the answer to a yield request.
func Decide(approved bool, feedback string) YieldResponse {
	return YieldResponse{Type: "yield_response", Approved: approved, Feedback: feedback}
}
