package crypto_test

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/b070nd/staircase-core/src/internal/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── GenerateSigningKey ───────────────────────────────────────────────────────

func TestGenerateSigningKey_creates_key_files(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, crypto.GenerateSigningKey(dir))

	keyPath := filepath.Join(dir, crypto.SigningKeyFile)
	pubPath := filepath.Join(dir, crypto.SigningPubFile)

	keyData, err := os.ReadFile(keyPath)
	require.NoError(t, err)
	assert.Len(t, keyData, ed25519.PrivateKeySize, "private key must be 64 bytes")

	pubData, err := os.ReadFile(pubPath)
	require.NoError(t, err)
	assert.Len(t, pubData, ed25519.PublicKeySize, "public key must be 32 bytes")
}

func TestGenerateSigningKey_nonwritable_dir_returns_error(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX read-only dir not enforced on Windows")
	}
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o555)) // remove write bit
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	err := crypto.GenerateSigningKey(dir)
	assert.Error(t, err, "GenerateSigningKey in a non-writable directory must fail")
}

func TestGenerateSigningKey_idempotent(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, crypto.GenerateSigningKey(dir))

	// Read the original key.
	orig, err := os.ReadFile(filepath.Join(dir, crypto.SigningKeyFile))
	require.NoError(t, err)

	// Call again — should not overwrite.
	require.NoError(t, crypto.GenerateSigningKey(dir))
	after, err := os.ReadFile(filepath.Join(dir, crypto.SigningKeyFile))
	require.NoError(t, err)

	assert.Equal(t, orig, after, "second call must not regenerate the key")
}

func TestGenerateSigningKey_private_key_has_0600_permissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions not meaningful on Windows")
	}
	dir := t.TempDir()
	require.NoError(t, crypto.GenerateSigningKey(dir))

	info, err := os.Stat(filepath.Join(dir, crypto.SigningKeyFile))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

// TestGenerateSigningKey_pub_key_write_fails covers the path where the private
// key is written successfully but the public key write fails. We block the
// destination path by pre-creating a directory there; os.Rename(file→dir)
// returns EISDIR on POSIX, triggering the cleanup that removes the private key.
func TestGenerateSigningKey_pub_key_write_fails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX rename-over-directory semantics not guaranteed on Windows")
	}
	dir := t.TempDir()
	// Block the public key path with a directory so Rename fails.
	pubPath := filepath.Join(dir, crypto.SigningPubFile)
	require.NoError(t, os.MkdirAll(pubPath, 0o755))

	err := crypto.GenerateSigningKey(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write signing public key",
		"error must identify the pub key write as the failure point")

	// After the pub key write fails, GenerateSigningKey must clean up the
	// private key so the pair is always in a consistent state.
	_, statErr := os.Stat(filepath.Join(dir, crypto.SigningKeyFile))
	assert.True(t, os.IsNotExist(statErr),
		"private key must be removed when pub key write fails")
}

// ─── LoadSigningKey ───────────────────────────────────────────────────────────

func TestLoadSigningKey_roundtrip(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, crypto.GenerateSigningKey(dir))

	priv, err := crypto.LoadSigningKey(dir)
	require.NoError(t, err)
	assert.Len(t, priv, ed25519.PrivateKeySize)
}

func TestLoadSigningKey_missing_returns_error(t *testing.T) {
	_, err := crypto.LoadSigningKey(t.TempDir())
	assert.Error(t, err)
}

func TestLoadSigningKey_wrong_size_returns_error(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, crypto.SigningKeyFile), []byte("short"), 0o600))
	_, err := crypto.LoadSigningKey(dir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "corrupt")
}

func TestLoadSigningKey_unreadable_file_returns_error(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission check not enforced on Windows")
	}
	dir := t.TempDir()
	require.NoError(t, crypto.GenerateSigningKey(dir))
	keyPath := filepath.Join(dir, crypto.SigningKeyFile)
	// Mode 0o000 passes the 0o177 mask check but makes ReadFile fail.
	require.NoError(t, os.Chmod(keyPath, 0o000))
	t.Cleanup(func() { _ = os.Chmod(keyPath, 0o600) })

	_, err := crypto.LoadSigningKey(dir)
	assert.Error(t, err)
}

func TestLoadSigningKey_bad_permissions_returns_error(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions not meaningful on Windows")
	}
	dir := t.TempDir()
	require.NoError(t, crypto.GenerateSigningKey(dir))
	// Widen permissions to trigger the security check.
	require.NoError(t, os.Chmod(filepath.Join(dir, crypto.SigningKeyFile), 0o644))
	_, err := crypto.LoadSigningKey(dir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "insecure permissions")
}

// ─── LoadSigningPublicKey ─────────────────────────────────────────────────────

func TestLoadSigningPublicKey_roundtrip(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, crypto.GenerateSigningKey(dir))

	pub, err := crypto.LoadSigningPublicKey(dir)
	require.NoError(t, err)
	assert.Len(t, pub, ed25519.PublicKeySize)
}

func TestLoadSigningPublicKey_missing_returns_error(t *testing.T) {
	_, err := crypto.LoadSigningPublicKey(t.TempDir())
	assert.Error(t, err)
}

func TestLoadSigningPublicKey_wrong_size_returns_error(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, crypto.SigningPubFile), []byte("x"), 0o644))
	_, err := crypto.LoadSigningPublicKey(dir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "corrupt")
}

// ─── Sign / Verify ────────────────────────────────────────────────────────────

func TestSign_produces_128_char_hex(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, crypto.GenerateSigningKey(dir))
	priv, err := crypto.LoadSigningKey(dir)
	require.NoError(t, err)

	sig := crypto.Sign(priv, []byte("hello"))
	assert.Len(t, sig, 128, "Ed25519 signature must be 64 bytes = 128 hex chars")
}

func TestVerify_valid_signature_returns_nil(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, crypto.GenerateSigningKey(dir))
	priv, err := crypto.LoadSigningKey(dir)
	require.NoError(t, err)
	pub, err := crypto.LoadSigningPublicKey(dir)
	require.NoError(t, err)

	msg := []byte("audit checkpoint payload")
	sig := crypto.Sign(priv, msg)

	assert.NoError(t, crypto.Verify(pub, msg, sig))
}

func TestVerify_tampered_message_returns_error(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, crypto.GenerateSigningKey(dir))
	priv, err := crypto.LoadSigningKey(dir)
	require.NoError(t, err)
	pub, err := crypto.LoadSigningPublicKey(dir)
	require.NoError(t, err)

	sig := crypto.Sign(priv, []byte("original"))
	err = crypto.Verify(pub, []byte("tampered"), sig)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "verification failed")
}

func TestVerify_wrong_public_key_returns_error(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	require.NoError(t, crypto.GenerateSigningKey(dir1))
	require.NoError(t, crypto.GenerateSigningKey(dir2))

	priv1, err := crypto.LoadSigningKey(dir1)
	require.NoError(t, err)
	pub2, err := crypto.LoadSigningPublicKey(dir2)
	require.NoError(t, err)

	sig := crypto.Sign(priv1, []byte("msg"))
	err = crypto.Verify(pub2, []byte("msg"), sig)
	assert.Error(t, err)
}

func TestVerify_invalid_hex_returns_error(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, crypto.GenerateSigningKey(dir))
	pub, err := crypto.LoadSigningPublicKey(dir)
	require.NoError(t, err)

	err = crypto.Verify(pub, []byte("msg"), "not-hex!!!")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "decode signature")
}

func TestVerify_wrong_length_signature_returns_error(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, crypto.GenerateSigningKey(dir))
	pub, err := crypto.LoadSigningPublicKey(dir)
	require.NoError(t, err)

	// Valid hex but wrong length.
	err = crypto.Verify(pub, []byte("msg"), "deadbeef")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "wrong size")
}

// ─── Sign determinism: same key, same message → consistent verify ─────────────

func TestSign_verify_large_payload(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, crypto.GenerateSigningKey(dir))
	priv, _ := crypto.LoadSigningKey(dir)
	pub, _ := crypto.LoadSigningPublicKey(dir)

	big := make([]byte, 64*1024)
	for i := range big {
		big[i] = byte(i % 256)
	}
	sig := crypto.Sign(priv, big)
	assert.NoError(t, crypto.Verify(pub, big, sig))
}
