package approvalhttp

import (
	"os"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/wslock"
)

// TestLoopback: a session's key is sent only to this machine.
func TestLoopback(t *testing.T) {
	for raw, want := range map[string]bool{
		"http://127.0.0.1:8765": true, "http://localhost:1": true, "http://[::1]:9": true,
		"http://192.0.2.1:9": false, "http://evil.example": false, "https://127.0.0.1:1": false,
		"http://127.0.0.1.evil.example": false, "ftp://127.0.0.1": false, "": false,
	} {
		if got := loopback(raw); got != want {
			t.Errorf("loopback(%q) = %v, want %v", raw, got, want)
		}
	}
}

// TestSessions_a_dead_process_leaves_a_registration_that_reconcile_removes: a session holds a lock on its
// registration for as long as its process lives; the operating system frees it when the process ends, a kill
// included. Closing the lock without releasing the files is exactly that.
func TestSessions_a_dead_process_leaves_a_registration_that_reconcile_removes(t *testing.T) {
	if !wslock.Enforced {
		t.Skip("locks exclude nothing on this platform")
	}
	dir := t.TempDir()
	live, err := Register(dir, Session{Name: "alive", URL: "http://127.0.0.1:1", Token: "k1"})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Release()
	dead, err := Register(dir, Session{Name: "killed", URL: "http://127.0.0.1:2", Token: "k2"})
	if err != nil {
		t.Fatal(err)
	}
	_ = wslock.Unlock(dead.lock.Fd()) // the process ended: no cleanup ran, the files are still there
	_ = dead.lock.Close()

	if !alive(dir, live.S.Instance) || alive(dir, dead.S.Instance) {
		t.Fatalf("liveness: live=%v dead=%v", alive(dir, live.S.Instance), alive(dir, dead.S.Instance))
	}
	gone := Reconcile(dir)
	if len(gone) != 1 || gone[0].Name != "killed" {
		t.Fatalf("reconcile removed %+v, want only the dead session", gone)
	}
	if _, err := os.Stat(sessionFile(dir, dead.S.Instance)); !os.IsNotExist(err) {
		t.Fatal("the dead session's file is still there")
	}
	if _, err := os.Stat(sessionFile(dir, live.S.Instance)); err != nil {
		t.Fatal("the live session's file was removed")
	}
	if again := Reconcile(dir); len(again) != 0 {
		t.Fatalf("a second reconcile removed %+v", again)
	}
}

// TestSessions_each_start_is_a_new_instance: two sessions at the same address and key are two registrations.
func TestSessions_each_start_is_a_new_instance(t *testing.T) {
	dir := t.TempDir()
	a, _ := Register(dir, Session{Name: "run", URL: "http://127.0.0.1:9", Token: "same"})
	defer a.Release()
	b, _ := Register(dir, Session{Name: "run", URL: "http://127.0.0.1:9", Token: "same"})
	defer b.Release()
	if a.S.Instance == "" || a.S.Instance == b.S.Instance {
		t.Fatalf("instances %q and %q", a.S.Instance, b.S.Instance)
	}
}
