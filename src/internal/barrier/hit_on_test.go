//go:build barriers

package barrier_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/b070nd/stAirCase/src/internal/barrier"
)

// TestHit_holds_at_the_named_visit: the marker appears when the named point is
// reached for the n-th time and not before, and other points do not hold.
func TestHit_holds_at_the_named_visit(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STAIRCASE_BARRIER", barrier.Consumed+"@2")
	t.Setenv("STAIRCASE_BARRIER_DIR", dir)
	marker := filepath.Join(dir, barrier.Consumed+".reached")
	barrier.Hit(barrier.GitCAS)   // another point: returns
	barrier.Hit(barrier.Consumed) // the first visit: returns
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("held at the first visit")
	}
	go barrier.Hit(barrier.Consumed) // the second: holds
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no marker at the second visit")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
