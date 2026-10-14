package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// awaySession runs `staircase claude --away` with the fake Claude Code, which writes HEALTH.md; the check passes on it.
func awaySession(t *testing.T) (repo string, git func(args ...string) string) {
	repo, _, git = sessionRepo(t)
	sessionAway, sessionAllow, runChecks = true, []string{"HEALTH.md"}, []string{"test -f HEALTH.md"}
	t.Cleanup(func() {
		sessionAway, sessionOnEvidence, sessionInScope, sessionAllow, runChecks = false, false, false, nil, nil
		decideAccept, decideReject, decideShown, decideMessage = false, false, "", ""
	})
	require.NoError(t, claudeSession(nil, []string{"add", "a", "health", "file"}))
	return repo, git
}

// TestDecide_an_away_session_waits_and_accept_delivers_it (ADR 0006): `staircase claude --away` ends with nothing on the run
// branch; `staircase decide 1 --accept` delivers exactly the approved change, which verifies; deciding again is refused.
func TestDecide_an_away_session_waits_and_accept_delivers_it(t *testing.T) {
	_, git := awaySession(t)
	base := strings.TrimSpace(git("rev-parse", "main"))
	assert.Equal(t, base, strings.TrimSpace(git("rev-parse", "staircase/run-1")), "nothing is delivered before the review")

	decideAccept = true
	require.NoError(t, decideHandler(nil, []string{"1"}))
	assert.Equal(t, "ok\n", git("show", "staircase/run-1:HEALTH.md"))
	verifyMinCAL, verifyKey, verifyFile, verifyCheckAnchor, verifyAll, verifySigners = 2, "", "", false, false, ""
	require.NoError(t, verifyHandler(nil, []string{"staircase/run-1"}))

	assert.Error(t, decideHandler(nil, []string{"1"}), "a delivered run has nothing to decide")
}

// TestDecide_reject_and_a_stale_review: --shown names the review entry the decision was made on; another is refused. Rejecting
// leaves the branch at its base.
func TestDecide_reject_and_a_stale_review(t *testing.T) {
	_, git := awaySession(t)
	base := strings.TrimSpace(git("rev-parse", "staircase/run-1"))
	decideReject, decideShown = true, "0000000000000000"
	assert.ErrorContains(t, decideHandler(nil, []string{"1"}), "not the review")
	decideShown = ""
	require.NoError(t, decideHandler(nil, []string{"1"}))
	assert.Equal(t, base, strings.TrimSpace(git("rev-parse", "staircase/run-1")))
	decideReject, decideAccept = false, true
	assert.Error(t, decideHandler(nil, []string{"1"}), "a rejected run cannot be accepted afterwards")
}

// TestDecide_accept_and_reject_are_one_or_the_other: both, or neither without a terminal, is refused before anything happens.
func TestDecide_accept_and_reject_are_one_or_the_other(t *testing.T) {
	decideAccept, decideReject = true, true
	t.Cleanup(func() { decideAccept, decideReject = false, false })
	assert.ErrorContains(t, decideHandler(nil, []string{"1"}), "either")
}

func sampleReview() orchestrator.Review {
	return orchestrator.Review{RunID: 12, Entry: "1a2b3c4d5e6f7a8b9c0d", Base: "abc123abc123ff", Tree: "fedcba987654",
		Files:  map[string]string{"src/one.txt": "x", "src/two.txt": "y"},
		Checks: []certificate.Check{{Command: "go test ./...", ExitCode: 0}},
		ParkedProposals: []orchestrator.ParkedProposal{
			{Action: "file_edit", Files: []string{"src/bad.txt"}, Why: `check "go test ./..." failed (exit 1): FAIL`},
			{Action: "shell_exec", Why: "it needs a person's decision"}}}
}

// TestDecide_the_inbox_is_a_tree_of_what_waits: each run with its change, the checks of its tree and what was parked, and the
// review entry a decision names.
func TestDecide_the_inbox_is_a_tree_of_what_waits(t *testing.T) {
	text := inboxTree([]orchestrator.Review{sampleReview()})
	for _, want := range []string{"run #12", "1a2b3c4d5e6f", "src/one.txt", "src/two.txt", "go test ./...", "passed", "parked",
		"src/bad.txt", "exit 1", "shell_exec", "Y", "N", "D", "Z"} {
		assert.Contains(t, text, want)
	}
	assert.Contains(t, inboxTree(nil), "Nothing waits")
}

// TestDecide_keys: Y and I accept, N and O reject, D defers and shows what was parked, Z opens the clarifying session; anything
// else asks again. A decision names the review entry that was on the screen.
func TestDecide_keys(t *testing.T) {
	for _, tc := range []struct {
		in      string
		decided string // "accept", "reject" or "" (deferred)
		clarify bool
	}{
		{"y\n", "accept", false}, {"I\n", "accept", false}, {"n\n", "reject", false}, {"O\n", "reject", false},
		{"d\n", "", false}, {"what\ny\n", "accept", false}, {"z\nn\n", "reject", true}, {"", "", false},
	} {
		var out bytes.Buffer
		decided, shown, clarified := "", "", false
		ask(strings.NewReader(tc.in), &out, sampleReview(),
			func(accept bool, entry string) error {
				decided, shown = map[bool]string{true: "accept", false: "reject"}[accept], entry
				return nil
			},
			func(orchestrator.Review) error { clarified = true; return nil })
		assert.Equal(t, tc.decided, decided, "input %q", tc.in)
		assert.Equal(t, tc.clarify, clarified, "input %q", tc.in)
		if decided != "" {
			assert.Equal(t, "1a2b3c4d5e6f7a8b9c0d", shown, "the decision names the entry that was shown")
		}
		if tc.in == "d\n" {
			assert.Contains(t, out.String(), "src/bad.txt", "D shows the alternatives: what was parked")
		}
	}
}

// TestDecide_clarify_opens_a_restricted_session: Z writes the change, its evidence and what was parked into a disposable
// directory, and starts the clarifying session there, never in the repository; nothing it does decides anything.
func TestDecide_clarify_opens_a_restricted_session(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "clarify.log")
	require.NoError(t, os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n{ pwd; ls; printf '%s\\n' \"$@\"; } >"+log+"\n"), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	repo := t.TempDir()
	gitIn := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	gitIn("init", "-q", "-b", "main")
	rv := sampleReview()
	require.NoError(t, clarify(rv, repo, "add a health file"))
	b, err := os.ReadFile(log)
	require.NoError(t, err)
	got := string(b)
	assert.NotContains(t, strings.SplitN(got, "\n", 2)[0], repo, "not started in the repository")
	assert.Contains(t, got, "REVIEW.md")
	assert.Contains(t, got, "plan", "read-only: Claude Code's plan mode")
}

// TestSession_away_says_what_will_happen_and_needs_a_check: the agreement tells the person nobody is asked, what is parked and
// that the run ends waiting for staircase decide; --away without a check is refused before anything starts.
func TestSession_away_says_what_will_happen_and_needs_a_check(t *testing.T) {
	text := agreement(sessionSetup{harness: "claude-code", name: "Claude Code", task: "t", root: "/r", base: "abc",
		allow: []string{"src/**"}, checks: []string{"go test ./..."}, onEvidence: true, away: true})
	for _, want := range []string{"nobody is asked", "parked", "staircase decide", "Nothing is committed"} {
		assert.Contains(t, text, want)
	}
	t.Cleanup(func() {
		sessionAway, sessionOnEvidence, sessionInScope, sessionAllow, runChecks = false, false, false, nil, nil
	})
	sessionAway, sessionAllow, runChecks = true, []string{"src/**"}, nil
	assert.ErrorContains(t, claudeSession(nil, []string{"task"}), "--check")
}
