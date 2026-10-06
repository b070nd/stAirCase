package orchestrator

import (
	"testing"

	"github.com/b070nd/stAirCase/src/internal/wslock"
)

// TestRunOwner: the owner lock is held for a run's life by one claimant; its state is known only for a run that
// took it, free after release, and never claimed twice.
func TestRunOwner(t *testing.T) {
	if !wslock.Enforced {
		t.Skip("locks exclude nothing on this platform")
	}
	ws := t.TempDir()
	if held, known := RunOwner(ws, 1); held || known {
		t.Fatalf("a run that never took the lock: held=%v known=%v, want neither", held, known)
	}
	release, err := claimRun(ws, 1)
	if err != nil {
		t.Fatal(err)
	}
	if held, known := RunOwner(ws, 1); !held || !known {
		t.Fatalf("a live run: held=%v known=%v", held, known)
	}
	if _, err := claimRun(ws, 1); err == nil {
		t.Fatal("two claimants own one run")
	}
	if _, err := claimRun(ws, 2); err != nil {
		t.Fatalf("another run is claimed independently: %v", err)
	}
	release()
	if held, known := RunOwner(ws, 1); held || !known {
		t.Fatalf("after release: held=%v known=%v, want free and known", held, known)
	}
	if r2, err := claimRun(ws, 1); err != nil {
		t.Fatalf("a released run can be claimed again: %v", err)
	} else {
		r2()
	}
}
