package crypto_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/b070nd/staircase-core/src/internal/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// randomKey returns a freshly-generated 32-byte AES-256 key via GenerateKey.
func randomKey(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, crypto.GenerateKey(dir))
	key, err := crypto.LoadKey(dir)
	require.NoError(t, err)
	return key
}

// ─── Encrypt / Decrypt ────────────────────────────────────────────────────────

func TestEncryptDecrypt_roundtrip(t *testing.T) {
	key := randomKey(t)
	ct, err := crypto.Encrypt(key, "hello world")
	require.NoError(t, err)
	pt, err := crypto.Decrypt(key, ct)
	require.NoError(t, err)
	assert.Equal(t, "hello world", pt)
}

func TestEncryptDecrypt_empty_plaintext(t *testing.T) {
	key := randomKey(t)
	ct, err := crypto.Encrypt(key, "")
	require.NoError(t, err)
	pt, err := crypto.Decrypt(key, ct)
	require.NoError(t, err)
	assert.Equal(t, "", pt)
}

func TestEncryptDecrypt_unicode(t *testing.T) {
	key := randomKey(t)
	plaintext := "こんにちは 🔐 stAirCase"
	ct, err := crypto.Encrypt(key, plaintext)
	require.NoError(t, err)
	pt, err := crypto.Decrypt(key, ct)
	require.NoError(t, err)
	assert.Equal(t, plaintext, pt)
}

func TestEncrypt_nonce_is_unique(t *testing.T) {
	// Two encryptions of the same plaintext must produce different ciphertexts
	// because each uses a fresh random nonce.
	key := randomKey(t)
	ct1, err := crypto.Encrypt(key, "same message")
	require.NoError(t, err)
	ct2, err := crypto.Encrypt(key, "same message")
	require.NoError(t, err)
	assert.NotEqual(t, ct1, ct2, "ciphertexts must differ due to random nonces")
}

func TestDecrypt_wrong_key_fails(t *testing.T) {
	key1 := randomKey(t)
	key2 := randomKey(t)
	ct, err := crypto.Encrypt(key1, "secret")
	require.NoError(t, err)
	_, err = crypto.Decrypt(key2, ct)
	assert.Error(t, err, "decrypting with wrong key must fail")
}

func TestDecrypt_tampered_ciphertext_fails(t *testing.T) {
	key := randomKey(t)
	ct, err := crypto.Encrypt(key, "authentic data")
	require.NoError(t, err)
	// Corrupt the last character of the base64 string.
	bs := []byte(ct)
	bs[len(bs)-3] ^= 0x01
	_, err = crypto.Decrypt(key, string(bs))
	assert.Error(t, err, "tampered ciphertext must not decrypt successfully")
}

func TestDecrypt_invalid_base64_fails(t *testing.T) {
	key := randomKey(t)
	_, err := crypto.Decrypt(key, "not-valid-base64!!!")
	assert.Error(t, err)
}

// ─── GenerateKey / LoadKey ────────────────────────────────────────────────────

func TestGenerateKey_creates_key_file(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, crypto.GenerateKey(dir))
	info, err := os.Stat(filepath.Join(dir, ".key"))
	require.NoError(t, err)
	assert.Equal(t, int64(32), info.Size())
}

func TestGenerateKey_mode_0600(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, crypto.GenerateKey(dir))
	info, err := os.Stat(filepath.Join(dir, ".key"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestGenerateKey_idempotent(t *testing.T) {
	// Second call must not overwrite an existing key.
	dir := t.TempDir()
	require.NoError(t, crypto.GenerateKey(dir))
	key1, err := crypto.LoadKey(dir)
	require.NoError(t, err)

	require.NoError(t, crypto.GenerateKey(dir)) // second call
	key2, err := crypto.LoadKey(dir)
	require.NoError(t, err)

	assert.Equal(t, key1, key2, "key must be unchanged after second GenerateKey call")
}

func TestLoadKey_wrong_size_returns_error(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".key"), []byte("too-short"), 0o600))
	_, err := crypto.LoadKey(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "32")
}

func TestLoadKey_missing_file_returns_error(t *testing.T) {
	dir := t.TempDir() // no .key file written
	_, err := crypto.LoadKey(dir)
	require.Error(t, err)
}

func TestLoadKey_unreadable_file_returns_error(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission check not enforced on Windows")
	}
	dir := t.TempDir()
	require.NoError(t, crypto.GenerateKey(dir))
	keyPath := filepath.Join(dir, ".key")
	// Mode 0o000: stat succeeds and passes the 0o177 permission mask check,
	// but ReadFile then fails because there is no read permission.
	require.NoError(t, os.Chmod(keyPath, 0o000))
	t.Cleanup(func() { _ = os.Chmod(keyPath, 0o600) })

	_, err := crypto.LoadKey(dir)
	assert.Error(t, err)
}

func TestLoadKey_insecure_permissions_rejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission check not enforced on Windows")
	}
	dir := t.TempDir()
	require.NoError(t, crypto.GenerateKey(dir))
	// Widen the key file permissions to 0644 — simulates accidental chmod.
	keyPath := filepath.Join(dir, ".key")
	require.NoError(t, os.Chmod(keyPath, 0o644))

	_, err := crypto.LoadKey(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insecure permissions", "error must mention insecure permissions")
}

// ─── Version prefix (C-3) ─────────────────────────────────────────────────────

func TestEncrypt_produces_v1_prefix(t *testing.T) {
	key := make([]byte, 32)
	ct, err := crypto.Encrypt(key, "hello")
	require.NoError(t, err)
	assert.True(t, len(ct) > 3 && ct[:3] == "v1:", "ciphertext must start with v1: prefix, got: %s", ct)
}

func TestDecrypt_v1_prefix_roundtrip(t *testing.T) {
	key := make([]byte, 32)
	plaintext := "super secret value"
	ct, err := crypto.Encrypt(key, plaintext)
	require.NoError(t, err)
	require.Contains(t, ct, "v1:")

	got, err := crypto.Decrypt(key, ct)
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)
}

func TestDecrypt_legacy_no_prefix_still_works(t *testing.T) {
	// Simulate a ciphertext stored before the v1: prefix was introduced.
	// Encrypt, strip the prefix, then verify Decrypt still succeeds.
	key := make([]byte, 32)
	plaintext := "legacy secret"
	ct, err := crypto.Encrypt(key, plaintext)
	require.NoError(t, err)

	legacy := ct[3:] // strip "v1:"
	got, err := crypto.Decrypt(key, legacy)
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)
}

// ─── Error paths ─────────────────────────────────────────────────────────────

func TestGenerateKey_nonwritable_dir_returns_error(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX read-only dir not enforced on Windows")
	}
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o555)) // remove write bit
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	err := crypto.GenerateKey(dir)
	assert.Error(t, err, "GenerateKey in a non-writable directory must fail")
}

func TestEncrypt_bad_key_size_returns_error(t *testing.T) {
	// AES requires 16, 24, or 32-byte keys; a 3-byte key triggers aes.NewCipher error.
	_, err := crypto.Encrypt([]byte{1, 2, 3}, "plaintext")
	assert.Error(t, err, "Encrypt with non-AES key size must fail")
}

func TestDecrypt_bad_key_size_returns_error(t *testing.T) {
	// Valid base64 so the decode succeeds; aes.NewCipher then rejects the bad key.
	_, err := crypto.Decrypt([]byte{1, 2, 3}, "dGVzdA==") // decodes to "test" (4 bytes)
	assert.Error(t, err, "Decrypt with non-AES key size must fail")
}

func TestDecrypt_ciphertext_too_short_returns_error(t *testing.T) {
	key := randomKey(t) // 32-byte key → valid AES + GCM nonce size is 12
	// base64 of a single byte decodes to 1 byte — less than the 12-byte GCM nonce.
	oneByteB64 := "Wg==" // base64 of 0x5A
	_, err := crypto.Decrypt(key, oneByteB64)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ciphertext too short")
}

// ─── CHECK 4.2.5: named test aliases for checklist grep ──────────────────────
// The checklist uses: go test -run "TestDecrypt.*Corrupt|TestDecrypt.*WrongKey"
// The equivalents above use snake_case names; these CamelCase aliases ensure
// the grep succeeds while exercising the same logic.

func TestDecryptWrongKey(t *testing.T) {
	key1 := randomKey(t)
	key2 := randomKey(t)
	ct, err := crypto.Encrypt(key1, "secret data")
	require.NoError(t, err)
	_, err = crypto.Decrypt(key2, ct)
	assert.Error(t, err, "decrypting with wrong key must fail (AES-GCM auth tag mismatch)")
}

func TestDecryptCorrupt(t *testing.T) {
	key := randomKey(t)
	ct, err := crypto.Encrypt(key, "authentic data")
	require.NoError(t, err)
	// Flip one bit in the ciphertext body (after the nonce, before the tag).
	bs := []byte(ct)
	bs[len(bs)/2] ^= 0xFF
	_, err = crypto.Decrypt(key, string(bs))
	assert.Error(t, err, "corrupt ciphertext must not decrypt (AES-GCM auth tag fails)")
}
