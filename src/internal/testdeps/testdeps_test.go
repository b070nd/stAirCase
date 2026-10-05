package testdeps_test

import (
	"testing"

	"github.com/b070nd/stAirCase/src/internal/testdeps"
)

// fakeT records how a test would have ended.
type fakeT struct {
	testing.TB
	failed, skipped bool
}

func (f *fakeT) Helper()               {}
func (f *fakeT) Fatalf(string, ...any) { f.failed = true; panic(f) }
func (f *fakeT) Skipf(string, ...any)  { f.skipped = true; panic(f) }

func need(name string) (f *fakeT) {
	f = &fakeT{}
	defer func() { _ = recover() }()
	testdeps.Need(f, name)
	return f
}

// TestNeed: a program that is there is no reason to stop; one that is missing
// skips the test, unless the deps are required, which fails it.
func TestNeed(t *testing.T) {
	t.Setenv("STAIRCASE_REQUIRE_DEPS", "")
	if f := need("go"); f.failed || f.skipped {
		t.Fatalf("go is installed: %+v", f)
	}
	if f := need("no-such-program-staircase"); !f.skipped || f.failed {
		t.Fatalf("a missing program should skip: %+v", f)
	}
	t.Setenv("STAIRCASE_REQUIRE_DEPS", "1")
	if f := need("no-such-program-staircase"); !f.failed || f.skipped {
		t.Fatalf("a missing program should fail when deps are required: %+v", f)
	}
}
