package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sshKey makes a throwaway SSH key for principal; it returns the public key
// path and its allowed_signers line.
func sshKey(t *testing.T, principal string) (pub, allowed string) {
	dir := t.TempDir()
	key := filepath.Join(dir, "id_ed25519")
	out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", principal, "-f", key).CombinedOutput()
	require.NoError(t, err, "%s", out)
	b, err := os.ReadFile(key + ".pub")
	require.NoError(t, err)
	return key + ".pub", principal + " " + strings.TrimSpace(string(b))
}

// TestSign_two_party: a second person signs a run's certificate with their
// SSH key; verified against an allowed_signers file, the change reaches
// CAL 4. The requester's own signature, an untrusted key or no signers file
// do not raise the level.
func TestSign_two_party(t *testing.T) {
	_, _, git := sessionRepo(t) // the repository's git email, the requester, is t@t
	require.NoError(t, claudeSession(nil, []string{"add", "a", "health", "file"}))
	commit := strings.TrimSpace(git("rev-parse", "staircase/run-1"))
	bobKey, bobAllowed := sshKey(t, "bob@example.com")
	_, eveAllowed := sshKey(t, "eve@example.com")
	signers := func(lines ...string) string {
		f := filepath.Join(t.TempDir(), "allowed_signers")
		require.NoError(t, os.WriteFile(f, []byte(strings.Join(lines, "\n")+"\n"), 0o600))
		return f
	}
	verifyMinCAL, verifyKey, verifyFile, verifyCheckAnchor, verifyAll, verifySigners = 4, "", "", false, false, ""
	t.Cleanup(func() { verifyMinCAL, verifySigners = 0, "" })

	assert.ErrorContains(t, verifyHandler(nil, []string{commit}), "below the required 4", "no person has signed yet")

	signKey, signPrincipal = bobKey, "bob@example.com"
	require.NoError(t, signHandler(nil, []string{commit}))

	verifySigners = signers(eveAllowed)
	assert.ErrorContains(t, verifyHandler(nil, []string{commit}), "below the required 4", "bob is not a trusted signer")

	verifySigners = signers(bobAllowed)
	require.NoError(t, verifyHandler(nil, []string{commit}), "bob, trusted and not the requester: CAL 4")

	// A person cannot be both the one who asked and the second party.
	requesterKey, requesterAllowed := sshKey(t, "t@t")
	signKey, signPrincipal = requesterKey, "t@t"
	require.NoError(t, signHandler(nil, []string{commit}))
	verifySigners = signers(requesterAllowed)
	assert.ErrorContains(t, verifyHandler(nil, []string{commit}), "below the required 4")
}

// TestVerify_authenticated_initiator: a run started with --sign-approvals
// carries its initiator's SSH signature. The second party must then be someone
// else than that person, whatever the git email says, and --require-initiator
// lets CAL 4 count only when the initiator proved who they are and is trusted
// (F96).
func TestVerify_authenticated_initiator(t *testing.T) {
	_, ws, git := sessionRepo(t) // git email t@t
	alicePub, aliceAllowed := sshKey(t, "alice@example.com")
	bobPub, bobAllowed := sshKey(t, "bob@example.com")
	alicePriv := strings.TrimSuffix(alicePub, ".pub")
	runSignKey, runSignAs = alicePriv, "alice@example.com"
	t.Cleanup(func() {
		runSignKey, runSignAs, verifyMinCAL, verifySigners, verifyRequireInitiator = "", "", 0, "", false
	})
	require.NoError(t, claudeSession(nil, []string{"add", "a", "health", "file"}))
	signed := strings.TrimSpace(git("rev-parse", "staircase/run-1"))
	runSignKey, runSignAs = "", ""
	require.NoError(t, claudeSession(nil, []string{"add", "another", "file"}))
	anon := strings.TrimSpace(git("rev-parse", "staircase/run-2"))

	signers := func(lines ...string) string {
		f := filepath.Join(t.TempDir(), "allowed_signers")
		require.NoError(t, os.WriteFile(f, []byte(strings.Join(lines, "\n")+"\n"), 0o600))
		return f
	}
	verifyMinCAL, verifyKey, verifyFile, verifyCheckAnchor, verifyAll = 4, "", "", false, false
	for _, c := range []string{signed, anon} {
		signKey, signPrincipal = bobPub, "bob@example.com"
		require.NoError(t, signHandler(nil, []string{c}))
	}

	// bob is a second person for both; with the initiator required, only for the one whose initiator is known.
	verifySigners, verifyRequireInitiator = signers(aliceAllowed, bobAllowed), false
	require.NoError(t, verifyHandler(nil, []string{signed}))
	require.NoError(t, verifyHandler(nil, []string{anon}))
	verifyRequireInitiator = true
	require.NoError(t, verifyHandler(nil, []string{signed}), "initiator alice proved herself and is trusted; bob is someone else")
	assert.ErrorContains(t, verifyHandler(nil, []string{anon}), "below the required 4", "nobody proved who asked for this run")

	// An initiator whose key the signers file does not list is not authenticated either.
	verifySigners = signers(bobAllowed)
	assert.ErrorContains(t, verifyHandler(nil, []string{signed}), "below the required 4")
	verifyRequireInitiator = false
	require.NoError(t, verifyHandler(nil, []string{signed}), "without the requirement the claim is best effort")

	// The initiator cannot be her own second party, although her git email is another string.
	signKey, signPrincipal = alicePub, "alice@example.com"
	require.NoError(t, signHandler(nil, []string{signed}))
	verifySigners = signers(aliceAllowed)
	assert.ErrorContains(t, verifyHandler(nil, []string{signed}), "below the required 4", "alice signed as a reviewer, but she is the initiator")

	// The same person with another key is still the initiator.
	alice2Pub, alice2Allowed := sshKey(t, "alice@example.com")
	signKey, signPrincipal = alice2Pub, "alice@example.com"
	require.NoError(t, signHandler(nil, []string{signed}))
	verifySigners = signers(alice2Allowed)
	assert.ErrorContains(t, verifyHandler(nil, []string{signed}), "below the required 4", "a second key of the initiator is not a second person")

	// The initiator's key under another name is not a second person either.
	signKey, signPrincipal = alicePub, "alice.home@example.com"
	require.NoError(t, signHandler(nil, []string{signed}))
	verifySigners = signers("alice.home@example.com " + strings.SplitN(aliceAllowed, " ", 2)[1])
	assert.ErrorContains(t, verifyHandler(nil, []string{signed}), "below the required 4", "the same key is the same person")

	// A certificate whose initiator name was changed after the signature was made
	// fails, even re-signed with the workspace key: the signature does not fit it.
	var env certificate.Envelope
	require.NoError(t, json.Unmarshal([]byte(git("notes", "--ref=staircase", "show", signed)), &env))
	priv, err := crypto.LoadSigningKey(ws)
	require.NoError(t, err)
	pub, _ := crypto.LoadSigningPublicKey(ws)
	st, err := certificate.Open(env, pub)
	require.NoError(t, err)
	st.Predicate.Initiator = &certificate.Initiator{Principal: "bob@example.com", Signature: st.Predicate.Initiator.Signature}
	forged, err := certificate.Sign(st, priv)
	require.NoError(t, err)
	fb, _ := json.Marshal(forged)
	verifyFile = filepath.Join(t.TempDir(), "forged.json")
	t.Cleanup(func() { verifyFile = "" })
	require.NoError(t, os.WriteFile(verifyFile, fb, 0o600))
	verifySigners = signers(aliceAllowed, bobAllowed)
	assert.ErrorContains(t, verifyHandler(nil, []string{signed}), "initiator's signature is not valid")
}
