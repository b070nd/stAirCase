//go:build !barriers

package barrier_test

import (
	"os"
	"testing"
	"time"

	"github.com/b070nd/stAirCase/src/internal/barrier"
)

// TestHit_is_inert_in_a_normal_build: the environment that holds a drill's binary
// does nothing to one built without -tags barriers, so a released program cannot be stopped this way.
func TestHit_is_inert_in_a_normal_build(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STAIRCASE_BARRIER", barrier.JournalSynced)
	t.Setenv("STAIRCASE_BARRIER_DIR", dir)
	done := make(chan struct{})
	go func() { barrier.Hit(barrier.JournalSynced); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Hit held the process in a build without the barriers tag")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("a marker was written: %v", entries)
	}
}
