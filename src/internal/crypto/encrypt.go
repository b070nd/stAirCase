package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ciphertextVersion is a prefix written at the start of every new ciphertext.
// On decrypt, its presence identifies the algorithm version so future
// algorithm changes can be handled transparently. Legacy ciphertexts that
// lack the prefix are still decrypted with AES-256-GCM (backward compat).
const ciphertextVersion = "v1:"

const KeyFile = ".key"

// GenerateKey writes a fresh 32-byte (AES-256) random key to keyPath with mode 0600.
// An atomic tempfile-then-rename pattern is used so that a process crash
// mid-write cannot leave a truncated or zero-byte key file on disk.
func GenerateKey(wsDir string) error {
	keyPath := filepath.Join(wsDir, KeyFile)
	if _, err := os.Stat(keyPath); err == nil {
		return nil // already exists — init is idempotent
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("generate key: %w", err)
	}
	// Write to a sibling temp file, set permissions, then rename into place.
	// os.Rename is atomic on POSIX; on Windows it is best-effort.
	tmp, err := os.CreateTemp(wsDir, ".key-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp key: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(key); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp key: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod temp key: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp key: %w", err)
	}
	if err := os.Rename(tmpPath, keyPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("install key: %w", err)
	}
	return nil
}

// LoadKey reads the 32-byte workspace key.
// On Unix it also verifies that the key file has mode 0600; a wider permission
// set indicates the file may have been exposed and is treated as an error.
// (On Windows POSIX mode bits are not meaningful and the check is skipped.)
//
// LoadKey automatically resumes any "committed" rotation journal left by a
// crashed rotation before reading the key.  This ensures that any command
// that loads the key self-heals an interrupted rotation without requiring the
// user to re-run 'staircase secret rotate'.
func LoadKey(wsDir string) ([]byte, error) {
	if err := ResumeIfCommitted(wsDir); err != nil {
		return nil, fmt.Errorf("load workspace key: rotate recovery: %w", err)
	}
	keyPath := filepath.Join(wsDir, KeyFile)
	if runtime.GOOS != "windows" {
		info, err := os.Stat(keyPath)
		if err != nil {
			return nil, fmt.Errorf("load workspace key (run 'staircase init' first): %w", err)
		}
		if perm := info.Mode().Perm(); perm&0o177 != 0 {
			return nil, fmt.Errorf(
				"workspace key %q has insecure permissions %04o (expected 0600) — fix with: chmod 600 %q",
				keyPath, perm, keyPath,
			)
		}
	}
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("load workspace key (run 'staircase init' first): %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("workspace key corrupt: expected 32 bytes, got %d", len(key))
	}
	return key, nil
}

// Encrypt encrypts plaintext with AES-256-GCM and returns "v1:" + base64(nonce||ciphertext).
// The "v1:" prefix allows future algorithm migrations to be detected on decrypt.
func Encrypt(key []byte, plaintext string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return ciphertextVersion + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt. Accepts both versioned ("v1:…") and legacy
// (bare base64) ciphertexts so that secrets stored before the version prefix
// was introduced can still be decrypted without a migration step.
func Decrypt(key []byte, encoded string) (string, error) {
	// Strip the version prefix if present; otherwise treat as legacy format.
	encoded = strings.TrimPrefix(encoded, ciphertextVersion)

	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	ns := gcm.NonceSize()
	if len(data) < ns {
		return "", fmt.Errorf("ciphertext too short")
	}
	pt, err := gcm.Open(nil, data[:ns], data[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(pt), nil
}
