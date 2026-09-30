package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRebuild_checks_a_commit_against_its_ledger: after a session, rebuild
// replays the run's approved proposals on the base commit and finds the
// commit's tree. It refuses a ledger that is not the certificate's, a commit
// without a ledger, and a commit that reuses a genuine ledger but holds other
// bytes (even with a valid signature).
func TestRebuild_checks_a_commit_against_its_ledger(t *testing.T) {
	repo, ws, git := sessionRepo(t)
	require.NoError(t, claudeSession(nil, []string{"add", "a", "health", "file"}))
	commit := strings.TrimSpace(git("rev-parse", "staircase/run-1"))
	rebuildKey, rebuildLedger, rebuildCertificate = "", "", ""
	t.Cleanup(func() { rebuildKey, rebuildLedger, rebuildCertificate = "", "", "" })

	require.NoError(t, rebuildHandler(nil, []string{"staircase/run-1"}))

	// The ledger may be anywhere (a reviewer gets it from the author); it must be the certificate's.
	ledger := filepath.Join(ws, "audit", "run-1.ledger.json")
	b, err := os.ReadFile(ledger)
	require.NoError(t, err)
	copyTo := filepath.Join(t.TempDir(), "shared.json")
	require.NoError(t, os.WriteFile(copyTo, b, 0o600))
	require.NoError(t, os.Remove(ledger))
	assert.Error(t, rebuildHandler(nil, []string{commit}), "no ledger file")
	rebuildLedger = copyTo
	require.NoError(t, rebuildHandler(nil, []string{commit}))

	altered := strings.Replace(string(b), `"ok\n"`, `"OK\n"`, 1)
	require.NotEqual(t, string(b), altered)
	require.NoError(t, os.WriteFile(copyTo, []byte(altered), 0o600))
	assert.ErrorContains(t, rebuildHandler(nil, []string{commit}), "is not the ledger the certificate names")
	require.NoError(t, os.WriteFile(copyTo, b, 0o600))

	assert.ErrorContains(t, rebuildHandler(nil, []string{"main"}), "no change certificate")

	// A commit with the same parent and another tree, signed with a real key
	// and carrying the genuine ledger's digest.
	parent := strings.TrimSpace(git("rev-parse", commit+"^"))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "evil.txt"), []byte("evil\n"), 0o644))
	git("add", "evil.txt")
	tree := strings.TrimSpace(git("write-tree"))
	git("reset", "-q")
	forged := strings.TrimSpace(git("commit-tree", tree, "-p", parent, "-m", "forged"))
	priv, err := crypto.LoadSigningKey(ws)
	require.NoError(t, err)
	genuine := strings.TrimSpace(git("notes", "--ref=staircase", "show", commit))
	var env certificate.Envelope
	require.NoError(t, json.Unmarshal([]byte(genuine), &env))
	pub, _ := crypto.LoadSigningPublicKey(ws)
	st, err := certificate.Open(env, pub)
	require.NoError(t, err)
	forgedEnv, err := certificate.Sign(certificate.New(forged, st.Predicate), priv)
	require.NoError(t, err)
	fb, _ := json.Marshal(forgedEnv)
	git("notes", "--ref=staircase", "add", "-m", string(fb), forged)
	verifyMinCAL, verifyKey, verifyFile, verifySigners = 0, "", "", ""
	require.NoError(t, verifyHandler(nil, []string{forged}), "the certificate itself is valid")
	assert.ErrorContains(t, rebuildHandler(nil, []string{forged}), "does not have the tree the ledger produces")

	// A certificate from before ledgers cannot be rebuilt.
	old := st.Predicate
	old.Ledger = ""
	oldEnv, err := certificate.Sign(certificate.New(commit, old), priv)
	require.NoError(t, err)
	ob, _ := json.Marshal(oldEnv)
	f := filepath.Join(t.TempDir(), "old.json")
	require.NoError(t, os.WriteFile(f, ob, 0o600))
	rebuildCertificate = f
	assert.ErrorContains(t, rebuildHandler(nil, []string{commit}), "no ledger")
}
