package persistence_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestForeignKeys_enforced_on_every_pooled_connection proves FK enforcement is
// active regardless of which pooled connection serves the query. Previously the
// one-shot `PRAGMA foreign_keys=ON` only covered a single connection; the DSN
// pragma now applies it to all of them.
func TestForeignKeys_enforced_on_every_pooled_connection(t *testing.T) {
	s := newTestStore(t)

	// run_event_logs.run_id REFERENCES runs(id). Appending to a non-existent
	// run must be rejected by FK enforcement, not silently inserted.
	_, err := s.AppendEventLog(999999, "state_emit", "orphan", "", "")
	require.Error(t, err, "append to non-existent run must violate the run_id FK")

	// Hammer many parallel inserts so the pool hands out multiple connections;
	// every one must enforce the constraint.
	var wg sync.WaitGroup
	const n = 32
	wg.Add(n)
	errCount := make([]bool, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_, e := s.AppendEventLog(999999, "state_emit", "orphan", "", "")
			errCount[i] = e != nil
		}(i)
	}
	wg.Wait()
	for i := range errCount {
		assert.True(t, errCount[i], "every pooled connection must enforce the FK (insert %d)", i)
	}
}
