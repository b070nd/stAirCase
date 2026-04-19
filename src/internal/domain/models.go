package domain

import (
	"time"
)

// ─── Status constants ─────────────────────────────────────────────────────────
// Using typed string constants rather than bare literals prevents silent typos
// from being stored in the database (e.g. "COMPLTEED" instead of "COMPLETED").

// Case status values — enforced by CHECK constraint on new databases.
const (
	CaseStatusPending   = "PENDING"
	CaseStatusRunning   = "RUNNING"
	CaseStatusCompleted = "COMPLETED"
	CaseStatusFailed    = "FAILED"
)

// Run status values — enforced by CHECK constraint on new databases.
const (
	RunStatusRunning = "RUNNING"
	RunStatusSuccess = "SUCCESS"
	RunStatusFailed  = "FAILED"
	RunStatusKilled  = "KILLED"
)

// UserStory status values — enforced by CHECK constraint on new databases.
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
	ID          int64  `json:"id"`
	VendorID    int64  `json:"vendor_id"`
	Name        string `json:"name"`
	SourcePath  string `json:"source_path,omitempty"`
	WebhookURL  string `json:"webhook_url,omitempty"`
}

type ProjectDependency struct {
	ID            int64 `json:"id"`
	SourceProjectID int64 `json:"source_project_id"`
	TargetProjectID int64 `json:"target_project_id"`
}

type Secret struct {
	ID             int64  `json:"id"`
	KeyName        string `json:"key_name"`
	EncryptedValue string `json:"encrypted_value"`
	ScopedToProjectID *int64 `json:"scoped_to_project_id,omitempty"`
}

type Component struct {
	ID       int64  `json:"id"`
	ProjectID int64 `json:"project_id"`
	Name     string `json:"name"`
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