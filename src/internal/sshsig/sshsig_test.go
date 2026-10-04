package sshsig

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/testdeps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// keypair makes an SSH key and an allowed_signers file that trusts it for
// principal.
func keypair(t *testing.T, principal string) (key, signers string) {
	testdeps.Need(t, "ssh-keygen")
	dir := t.TempDir()
	key = filepath.Join(dir, "id")
	out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", principal, "-f", key).CombinedOutput()
	require.NoError(t, err, string(out))
	pub, err := os.ReadFile(key + ".pub")
	require.NoError(t, err)
	signers = filepath.Join(dir, "allowed_signers")
	require.NoError(t, os.WriteFile(signers, append([]byte(principal+" "), pub...), 0o644))
	return key, signers
}

// TestSignCheckVerify: a signature is valid for the payload and namespace it
// was made for, names the key that made it, and is trusted only for a
// principal the allowed_signers file lists for that key.
func TestSignCheckVerify(t *testing.T) {
	key, signers := keypair(t, "alice@example.com")
	payload := []byte("staircase-decision-v1\nrun=1\nseq=2\ndecision=approve")

	sig, err := Sign(key, "staircase-decision", payload)
	require.NoError(t, err)

	fp, err := Check("staircase-decision", payload, sig)
	require.NoError(t, err)
	assert.Regexp(t, `^SHA256:`, fp)
	assert.NoError(t, Verify(signers, "alice@example.com", "staircase-decision", payload, sig))

	assert.Error(t, Verify(signers, "bob@example.com", "staircase-decision", payload, sig), "another principal")
	assert.Error(t, Verify(signers, "alice@example.com", "staircase-decision", []byte("decision=reject"), sig), "another payload")
	assert.Error(t, Verify(signers, "alice@example.com", "staircase-certificate", payload, sig), "another namespace")
	_, err = Check("staircase-decision", []byte("other"), sig)
	assert.Error(t, err)

	other, _ := keypair(t, "mallory@example.com")
	forged, err := Sign(other, "staircase-decision", payload)
	require.NoError(t, err)
	assert.Error(t, Verify(signers, "alice@example.com", "staircase-decision", payload, forged), "another key claiming alice")
}
