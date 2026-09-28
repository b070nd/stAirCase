package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
