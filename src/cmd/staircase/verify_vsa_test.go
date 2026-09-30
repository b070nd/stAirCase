package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/vsa"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetVSAFlags(t *testing.T) {
	verifyVSAOut, verifyResourceURI, verifyVerifierID, verifyPolicyURI, verifyVSAKey = "", "", "", "", ""
	t.Cleanup(func() {
		verifyVSAOut, verifyResourceURI, verifyVerifierID, verifyPolicyURI, verifyVSAKey = "", "", "", "", ""
		verifyMinCAL, verifyAll, verifyRebuild, verifyKey, verifyFile, verifySigners, verifyRequireInitiator = 0, false, false, "", "", "", false
	})
}

// TestVerify_writes_a_verification_summary: a commit that passes verify can be
// summarized in a signed SLSA VSA, written only for a commit that passed, about
// exactly that commit and its tree, naming the attestations it was based on.
func TestVerify_writes_a_verification_summary(t *testing.T) {
	_, ws, git := sessionRepo(t)
	require.NoError(t, claudeSession(nil, []string{"add", "a", "health", "file"}))
	commit := strings.TrimSpace(git("rev-parse", "staircase/run-1"))
	tree := strings.TrimSpace(git("rev-parse", commit+"^{tree}"))
	resetVSAFlags(t)
	out := t.TempDir()
	verifyVSAOut = out

	// The repository has no remote: a VSA needs its URI, and says so.
	verifyMinCAL = 3
	err := verifyHandler(nil, []string{commit})
	require.ErrorContains(t, err, "resource-uri")
	entries, _ := os.ReadDir(out)
	assert.Empty(t, entries, "nothing written")

	git("remote", "add", "origin", "git@github.com:acme/shop.git")
	verifyRebuild = true
	require.NoError(t, verifyHandler(nil, []string{commit}))
	b, err := os.ReadFile(filepath.Join(out, commit+".vsa.json"))
	require.NoError(t, err)
	var env certificate.Envelope
	require.NoError(t, json.Unmarshal(b, &env))
	pub, err := crypto.LoadSigningPublicKey(ws)
	require.NoError(t, err)
	assert.Equal(t, certificate.KeyID(pub), env.Signatures[0].KeyID, "signed with the workspace key by default")
	payload, _ := base64.StdEncoding.DecodeString(env.Payload)
	var st struct {
		Subject []struct {
			Digest map[string]string `json:"digest"`
		} `json:"subject"`
		PredicateType string `json:"predicateType"`
		Predicate     struct {
			ResourceURI        string   `json:"resourceUri"`
			VerificationResult string   `json:"verificationResult"`
			VerifiedLevels     []string `json:"verifiedLevels"`
			Verifier           struct {
				ID string `json:"id"`
			} `json:"verifier"`
			Inputs []struct {
				Name   string            `json:"name"`
				Digest map[string]string `json:"digest"`
			} `json:"inputAttestations"`
		} `json:"predicate"`
	}
	require.NoError(t, json.Unmarshal(payload, &st))
	assert.Equal(t, vsa.PredicateType, st.PredicateType)
	assert.Equal(t, commit, st.Subject[0].Digest["gitCommit"])
	assert.Equal(t, tree, st.Subject[0].Digest["gitTree"])
	assert.Equal(t, "git+https://github.com/acme/shop", st.Predicate.ResourceURI)
	assert.Equal(t, "PASSED", st.Predicate.VerificationResult)
	assert.Equal(t, "https://github.com/b070nd/stAirCase", st.Predicate.Verifier.ID)
	assert.Contains(t, st.Predicate.VerifiedLevels, "STAIRCASE_CAL_3")
	assert.Contains(t, st.Predicate.VerifiedLevels, "STAIRCASE_REBUILT")
	require.Len(t, st.Predicate.Inputs, 2, "the certificate and the ledger")
	cert := sha256.Sum256([]byte(git("notes", "--ref=staircase", "show", commit))) // what `git notes show | sha256sum` gives
	assert.Equal(t, hex.EncodeToString(cert[:]), st.Predicate.Inputs[0].Digest["sha256"])

	// A commit that does not pass gets no VSA, whatever else is asked.
	verifyMinCAL = 4
	require.Error(t, verifyHandler(nil, []string{commit}))
	entries, _ = os.ReadDir(out)
	assert.Len(t, entries, 1, "no summary for a commit that failed")

	// A range writes one per commit; the overrides name the verifier and the repository.
	verifyMinCAL, verifyAll, verifyRebuild = 3, true, false
	out2 := t.TempDir()
	verifyVSAOut, verifyResourceURI, verifyVerifierID = out2, "git+https://example.com/team/shop", "https://ci.example.com/verifier"
	require.NoError(t, verifyHandler(nil, []string{"main.." + commit}))
	b, err = os.ReadFile(filepath.Join(out2, commit+".vsa.json"))
	require.NoError(t, err)
	assert.Contains(t, string(b), "payload")
	require.NoError(t, json.Unmarshal(b, &env))
	payload, _ = base64.StdEncoding.DecodeString(env.Payload)
	assert.Contains(t, string(payload), `"resourceUri":"git+https://example.com/team/shop"`)
	assert.Contains(t, string(payload), `"id":"https://ci.example.com/verifier"`)
	assert.NotContains(t, string(payload), "STAIRCASE_REBUILT", "not rebuilt this time")
}
