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

// TestVerify_rebuild_enforces_the_ledger: a run also leaves its ledger as a git
// note (refs/notes/staircase-ledger), and verify --rebuild, which a CI check
// can run with nothing but the repository, refuses a validly signed
// certificate on a commit that holds other bytes than the ledger produces, and
// a certified commit whose ledger is not there (F99).
func TestVerify_rebuild_enforces_the_ledger(t *testing.T) {
	_, ws, git := sessionRepo(t)
	require.NoError(t, claudeSession(nil, []string{"add", "a", "health", "file"}))
	commit := strings.TrimSpace(git("rev-parse", "staircase/run-1"))
	verifyMinCAL, verifyKey, verifyFile, verifySigners, verifyAll, verifyRebuild = 0, "", "", "", false, false
	t.Cleanup(func() { verifyAll, verifyRebuild, verifyLedger = false, false, "" })

	file, err := os.ReadFile(filepath.Join(ws, "audit", "run-1.ledger.json"))
	require.NoError(t, err)
	assert.Equal(t, string(file), strings.TrimSuffix(git("notes", "--ref=staircase-ledger", "show", commit), "\n"),
		"the ledger travels with the commit as a note")

	verifyRebuild = true
	require.NoError(t, verifyHandler(nil, []string{commit}))
	verifyAll = true
	require.NoError(t, verifyHandler(nil, []string{"main.." + commit}), "a range, every commit rebuilt")
	verifyAll = false

	// A certified commit with another tree: the certificate is valid, the
	// genuine ledger is attached, and the bytes are not what it produces.
	parent := strings.TrimSpace(git("rev-parse", commit+"^"))
	priv, err := crypto.LoadSigningKey(ws)
	require.NoError(t, err)
	var env certificate.Envelope
	require.NoError(t, json.Unmarshal([]byte(git("notes", "--ref=staircase", "show", commit)), &env))
	pub, _ := crypto.LoadSigningPublicKey(ws)
	st, err := certificate.Open(env, pub)
	require.NoError(t, err)
	tree := strings.TrimSpace(git("rev-parse", parent+"^{tree}")) // the parent's own tree: nothing was approved
	forged := strings.TrimSpace(git("commit-tree", tree, "-p", parent, "-m", "forged"))
	forgedEnv, err := certificate.Sign(certificate.New(forged, st.Predicate), priv)
	require.NoError(t, err)
	fb, _ := json.Marshal(forgedEnv)
	git("notes", "--ref=staircase", "add", "-m", string(fb), forged)

	verifyRebuild = false
	require.NoError(t, verifyHandler(nil, []string{forged}), "signed, about this commit, high enough")
	verifyRebuild = true
	assert.ErrorContains(t, verifyHandler(nil, []string{forged}), "no ledger", "no ledger note on the forged commit")
	git("notes", "--ref=staircase-ledger", "add", "-m", string(file), forged)
	assert.ErrorContains(t, verifyHandler(nil, []string{forged}), "does not have the tree the ledger produces")
}
