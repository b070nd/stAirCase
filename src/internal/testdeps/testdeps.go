// Package testdeps is for tests that need a program the machine may not have.
// Without STAIRCASE_REQUIRE_DEPS such a test is skipped, with a message, so a
// developer's machine runs what it can. With it (CI, the release checks) a missing
// program fails the test: a gate that skips what it cannot run is not a gate.
package testdeps

import (
	"os"
	"os/exec"
	"testing"
)

// Required reports whether missing programs fail tests instead of skipping them.
func Required() bool { return os.Getenv("STAIRCASE_REQUIRE_DEPS") != "" }

// Need makes the test depend on the program name.
func Need(t testing.TB, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		if Required() {
			t.Fatalf("%s is required (STAIRCASE_REQUIRE_DEPS is set) and was not found: %v", name, err)
		}
		t.Skipf("%s is needed", name)
	}
}
