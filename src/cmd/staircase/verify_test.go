package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVerify_checks_a_commits_certificate: after a session, anyone with the
// workspace's public key can check that a commit carries a valid change
// certificate about exactly that commit, at the level they require; other
// commits, other keys and higher requirements fail.
func TestVerify_checks_a_commits_certificate(t *testing.T) {
	_, ws, git := sessionRepo(t)
	require.NoError(t, claudeSession(nil, []string{"add", "a", "health", "file"}))
	commit := strings.TrimSpace(git("rev-parse", "staircase/run-1"))

	verifyMinCAL, verifyKey, verifyFile = 0, "", ""
	require.NoError(t, verifyHandler(nil, []string{"staircase/run-1"}))
	verifyMinCAL = 3
	require.NoError(t, verifyHandler(nil, []string{commit}), "a gated Claude Code run reaches CAL 3")

	verifyMinCAL = 4
	assert.ErrorContains(t, verifyHandler(nil, []string{commit}), "CAL 3, below the required 4")
	verifyMinCAL = 0

	assert.ErrorContains(t, verifyHandler(nil, []string{"main"}), "no change certificate")

	other, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	verifyKey = filepath.Join(t.TempDir(), "other.pub")
	require.NoError(t, os.WriteFile(verifyKey, other, 0o600))
	assert.ErrorContains(t, verifyHandler(nil, []string{commit}), "signature")

	// A certificate moved onto another commit does not fit it.
	verifyKey = filepath.Join(ws, ".signing.pub")
	verifyFile = filepath.Join(ws, "audit", "run-1.certificate.json")
	assert.ErrorContains(t, verifyHandler(nil, []string{"main"}), "is about commit")
	verifyKey, verifyFile = "", ""
}
