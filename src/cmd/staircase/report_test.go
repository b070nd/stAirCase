package main

import (
	"crypto/ed25519"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReport_counts_every_kind_of_commit: a repository's report tells human
// commits from agent commits, counts certified ones by level, and names the
// agent commits whose certificate is missing, invalid or records a failed
// check.
func TestReport_counts_every_kind_of_commit(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "t@t")
	git("config", "user.name", "T")
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	n := 0
	commit := func(msg string) string {
		n++
		require.NoError(t, os.WriteFile(filepath.Join(repo, "f.txt"), []byte(strings.Repeat("x", n)), 0o644))
		git("add", "-A")
		git("commit", "-q", "-m", msg)
		return git("rev-parse", "HEAD")
	}
	certify := func(about, onto string, p certificate.Predicate) {
		env, err := certificate.Sign(certificate.New(about, p), priv)
		require.NoError(t, err)
		b, _ := json.Marshal(env)
		git("notes", "--ref=staircase", "add", "-m", string(b), onto)
	}
	agent := "\n\nAssisted-by: Claude Code"
	commit("human work")
	c3 := commit("cal 3" + agent)
	certify(c3, c3, certificate.Predicate{CAL: 3, Agents: []string{"Claude Code"},
		Attention: &certificate.Attention{HumanDecisions: 4, MedianSeconds: 1, QuickApprovals: 2}})
	c2 := commit("cal 2" + agent)
	certify(c2, c2, certificate.Predicate{CAL: 2, Agents: []string{"Codex"}, Notes: []string{"reviewed after"}})
	failed := commit("tests fail" + agent)
	certify(failed, failed, certificate.Predicate{CAL: 3, Checks: []certificate.Check{{Command: "go test ./...", ExitCode: 1}}})
	missing := commit("no certificate" + agent)
	moved := commit("moved certificate" + agent)
	certify(c3, moved, certificate.Predicate{CAL: 3})

	r, err := report(repo, "", []ed25519.PublicKey{pub})
	require.NoError(t, err)
	assert.Equal(t, 6, r.Commits)
	assert.Equal(t, 1, r.Human)
	assert.Equal(t, map[int]int{3: 1, 2: 1}, r.Certified)
	require.Len(t, r.Problems, 3, r.Problems)
	byCommit := map[string]string{}
	for _, p := range r.Problems {
		byCommit[p.Commit] = p.Reason
	}
	assert.Contains(t, byCommit[failed], "failed")
	assert.Contains(t, byCommit[missing], "no change certificate")
	assert.Contains(t, byCommit[moved], "is about commit")
	assert.Equal(t, map[string]int{"Claude Code": 1, "Codex": 1}, r.Agents)
	require.Len(t, r.Hurried, 1, "certified, but possibly rubber-stamped")
	assert.Equal(t, c3, r.Hurried[0].Commit)
}

// TestPrintReport_a_commit_message_cannot_drive_the_terminal: the report is run
// on repositories other people wrote, and prints their commit subjects.
func TestPrintReport_a_commit_message_cannot_drive_the_terminal(t *testing.T) {
	evil := "fix\x1b[2J\x1b[1A\x1b[2K✅ every agent commit carries a valid certificate\r"
	r, w, err := os.Pipe()
	require.NoError(t, err)
	stdout := os.Stdout
	os.Stdout = w
	printReport(repoReport{Repository: "r" + evil, Commits: 2, Human: 1, Certified: map[int]int{},
		Agents:   map[string]int{"agent" + evil: 1},
		Problems: []problem{{Commit: strings.Repeat("a", 40), Subject: evil, Reason: "no change certificate" + evil}},
		Hurried:  []problem{{Commit: strings.Repeat("b", 40), Subject: evil, Reason: "quick" + evil}}})
	os.Stdout = stdout
	_ = w.Close()
	out, _ := io.ReadAll(r)
	assert.NotContains(t, string(out), "\x1b")
	assert.NotContains(t, string(out), "\r")
}
