package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/stretchr/testify/assert"
)

// TestResumeRefusal_says_only_what_was_changed: a refused continuation changed nothing, unless this attempt recorded the outcome of
// a lost launch; then it says so and does not claim that nothing was changed.
func TestResumeRefusal_says_only_what_was_changed(t *testing.T) {
	refused := fmt.Errorf("%w: run #3 was halted for drift", orchestrator.ErrNotContinuable)

	plain := resumeRefusal(refused, 3)
	assert.ErrorIs(t, plain, orchestrator.ErrNotContinuable)
	assert.Contains(t, plain.Error(), "Nothing was changed.")
	assert.Contains(t, plain.Error(), "staircase recover 3")

	recorded := resumeRefusal(fmt.Errorf("%w: %w", orchestrator.ErrLaunchRecorded, refused), 3)
	assert.NotContains(t, recorded.Error(), "Nothing was changed")
	assert.Contains(t, recorded.Error(), "Only the lost launch's outcome was recorded, as unknown_after_restart, and nothing else was changed.")

	other := errors.New("db init failed")
	assert.Equal(t, other, resumeRefusal(other, 3), "an error that is not a refusal passes through")
	assert.NoError(t, resumeRefusal(nil, 3))
}
