package persistence_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAppendEventLogChained_concurrent_no_fork proves the SOC2 audit chain stays
// unbroken when many goroutines append to the same run concurrently — the bug
// the serialized AppendEventLogChained fixes (two appenders reading the same
// prevHash and forking the chain).
func TestAppendEventLogChained_concurrent_no_fork(t *testing.T) {
	s := newTestStore(t)
	_, _, topoID := scaffoldTopology(t, s)
	topo, err := s.GetTopology(topoID)
	require.NoError(t, err)
	c, err := s.CreateCase(topo.ProjectID)
	require.NoError(t, err)
	run, err := s.CreateRun(c.ID, 1, "staircase/run-1")
	require.NoError(t, err)

	const writers = 16
	const perWriter = 25
	var wg sync.WaitGroup
	wg.Add(writers)
	for w := 0; w < writers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				_, err := s.AppendEventLogChained(run.ID, "state_emit",
					fmt.Sprintf("w%d-i%d", w, i), "")
				assert.NoError(t, err)
			}
		}(w)
	}
	wg.Wait()

	logs, err := s.ListEventLogs(run.ID)
	require.NoError(t, err)
	assert.Len(t, logs, writers*perWriter, "every append must be persisted")

	// The chain must verify end-to-end: no entry may reference a prevHash that
	// another concurrent append also claimed.
	require.NoError(t, s.VerifyChain(run.ID), "audit chain must remain unbroken under concurrency")
}
