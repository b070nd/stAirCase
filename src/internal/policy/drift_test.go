package policy_test

import (
	"testing"

	"github.com/b070nd/staircase-core/src/internal/policy"
	"github.com/stretchr/testify/assert"
)

func TestSupervisor_scope(t *testing.T) {
	s := policy.NewSupervisor([]string{"docs/**", "GREETING.md"}, policy.Limits{})
	reason, halt := s.Check([]string{"docs/a/b.md", "GREETING.md"})
	assert.Empty(t, reason)
	assert.False(t, halt)
	s.Decided([]string{"docs/a/b.md", "GREETING.md"}, true, false, reason)

	reason, halt = s.Check([]string{"docs/x.md", "src/main.go"})
	assert.Contains(t, reason, "outside the stories' scope: src/main.go")
	assert.False(t, halt, "no violation limit set")
	s.Decided([]string{"docs/x.md", "src/main.go"}, true, true, reason)

	r := s.Report()
	assert.Equal(t, []string{"GREETING.md", "docs/a/b.md", "docs/x.md"}, r.InScope)
	assert.Equal(t, []string{"src/main.go"}, r.OutOfScope)
	assert.Equal(t, 1, r.Violations)
	assert.Equal(t, 1, r.Overrides)
	assert.Equal(t, 1, r.Auto)
	assert.Equal(t, 1, r.Human)
}

func TestSupervisor_no_scope_means_no_scope_check(t *testing.T) {
	s := policy.NewSupervisor(nil, policy.Limits{})
	reason, _ := s.Check([]string{"anything/at/all.go"})
	assert.Empty(t, reason)
}

func TestSupervisor_limits(t *testing.T) {
	s := policy.NewSupervisor([]string{"a/**"}, policy.Limits{CheckpointEvery: 3, MaxFilesChanged: 2, MaxScopeViolations: 1})
	var reasons []string
	for _, f := range []string{"a/1", "a/2", "a/3"} {
		reason, halt := s.Check([]string{f})
		assert.False(t, halt)
		reasons = append(reasons, reason)
		s.Decided([]string{f}, true, reason != "", reason)
	}
	assert.Empty(t, reasons[0])
	assert.Empty(t, reasons[1])
	assert.Contains(t, reasons[2], "3 files changed (limit 2)")
	assert.Contains(t, reasons[2], "checkpoint: every 3rd proposal")

	reason, halt := s.Check([]string{"b/1"})
	assert.NotEmpty(t, reason)
	assert.False(t, halt, "first violation is within the limit")
	s.Decided([]string{"b/1"}, false, true, reason)
	_, halt = s.Check([]string{"b/2"})
	assert.True(t, halt, "second violation exceeds max_scope_violations 1")

	assert.Empty(t, s.Report().OutOfScope, "a rejected drift proposal changed nothing")
}

func TestLimits_Tighter(t *testing.T) {
	a := policy.Limits{CheckpointEvery: 5, MaxFilesChanged: 10, MaxRunDurationSecs: 600}
	b := policy.Limits{CheckpointEvery: 2, MaxScopeViolations: 3, MaxRunDurationSecs: 900}
	assert.Equal(t, policy.Limits{CheckpointEvery: 2, MaxFilesChanged: 10, MaxScopeViolations: 3, MaxRunDurationSecs: 600}, a.Tighter(b))
}
