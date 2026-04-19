package persistence

import "github.com/b070nd/staircase-core/src/internal/domain"

// Status constants re-exported from domain so callers that already import
// persistence do not need a separate domain import just for string values.
// The canonical definitions (and the values themselves) live in domain/models.go.

// Case status values.
const (
	CaseStatusPending   = domain.CaseStatusPending
	CaseStatusRunning   = domain.CaseStatusRunning
	CaseStatusCompleted = domain.CaseStatusCompleted
	CaseStatusFailed    = domain.CaseStatusFailed
)

// Run status values.
const (
	RunStatusRunning = domain.RunStatusRunning
	RunStatusSuccess = domain.RunStatusSuccess
	RunStatusFailed  = domain.RunStatusFailed
	RunStatusKilled  = domain.RunStatusKilled
)

// UserStory status values.
const (
	StoryStatusPending     = domain.StoryStatusPending
	StoryStatusImplemented = domain.StoryStatusImplemented
	StoryStatusInvalidated = domain.StoryStatusInvalidated
)
