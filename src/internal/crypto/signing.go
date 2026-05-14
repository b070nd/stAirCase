package crypto

// Ed25519 signing for audit checkpoints.
//
// Key files:
//
//	$STAIRCASE_DIR/.signing.key  — 64-byte Ed25519 private key (seed+public, mode 0600)
//	$STAIRCASE_DIR/.signing.pub  — 32-byte Ed25519 public  key (mode 0644)
//
// Both are stored as raw bytes (not PEM/base64) to keep the code simple and the
// file sizes predictable.  The key pair is generated once by `staircase init`.
//
// Signatures are 64-byte Ed25519 signatures encoded as lowercase hex.

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const (
	SigningKeyFile = ".signing.key"
	SigningPubFile = ".signing.pub"
)

// GenerateSigningKey creates a fresh Ed25519 key pair under wsDir.
// Idempotent: if the key files already exist the function returns without error.
// The private key is written with mode 0600; the public key with mode 0644.
func GenerateSigningKey(wsDir string) error {
	keyPath := filepath.Join(wsDir, SigningKeyFile)
	pubPath := filepath.Join(wsDir, SigningPubFile)

	if _, err := os.Stat(keyPath); err == nil {
		return nil // already initialised
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate signing key: %w", err)
	}

	// Write private key (64 bytes: seed || public).
	if err := writeFileAtomic(wsDir, keyPath, priv, 0600); err != nil {
		return fmt.Errorf("write signing key: %w", err)
	}
	// Write public key (32 bytes).
	if err := writeFileAtomic(wsDir, pubPath, pub, 0644); err != nil {
		// Best-effort cleanup of the private key so the pair is always consistent.
		_ = os.Remove(keyPath)
		return fmt.Errorf("write signing public key: %w", err)
	}
	return nil
}

// LoadSigningKey reads the Ed25519 private key from wsDir and verifies its
// permissions on non-Windows systems (must be 0600).
func LoadSigningKey(wsDir string) (ed25519.PrivateKey, error) {
	keyPath := filepath.Join(wsDir, SigningKeyFile)

	if runtime.GOOS != "windows" {
		info, err := os.Stat(keyPath)
		if err != nil {
			return nil, fmt.Errorf("load signing key (run 'staircase init' first): %w", err)
		}
		if perm := info.Mode().Perm(); perm&0o177 != 0 {
			return nil, fmt.Errorf(
				"signing key %q has insecure permissions %04o (expected 0600) — fix with: chmod 600 %q",
				keyPath, perm, keyPath,
			)
		}
	}

	raw, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("load signing key (run 'staircase init' first): %w", err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("signing key corrupt: expected %d bytes, got %d", ed25519.PrivateKeySize, len(raw))
	}
	return ed25519.PrivateKey(raw), nil
}

// LoadSigningPublicKey reads the Ed25519 public key from wsDir.
func LoadSigningPublicKey(wsDir string) (ed25519.PublicKey, error) {
	pubPath := filepath.Join(wsDir, SigningPubFile)
	raw, err := os.ReadFile(pubPath)
	if err != nil {
		return nil, fmt.Errorf("load signing public key (run 'staircase init' first): %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("signing public key corrupt: expected %d bytes, got %d", ed25519.PublicKeySize, len(raw))
	}
	return ed25519.PublicKey(raw), nil
}

// Sign signs message with the Ed25519 private key and returns a 128-character
// lowercase hex string.
func Sign(priv ed25519.PrivateKey, message []byte) string {
	sig := ed25519.Sign(priv, message)
	return hex.EncodeToString(sig)
}

// Verify verifies a hex-encoded Ed25519 signature against message and pub.
// Returns an error when the signature is invalid or has the wrong length.
func Verify(pub ed25519.PublicKey, message []byte, sigHex string) error {
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("signature has wrong size: expected %d bytes, got %d", ed25519.SignatureSize, len(sig))
	}
	if !ed25519.Verify(pub, message, sig) {
		return fmt.Errorf("signature verification failed")
	}
	return nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// writeFileAtomic writes data to path via a sibling temp file + rename so a
// mid-write crash cannot leave a truncated file.
func writeFileAtomic(dir, path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
