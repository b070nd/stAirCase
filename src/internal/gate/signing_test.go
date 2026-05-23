package gate_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/b070nd/staircase-core/src/internal/crypto"
	"github.com/b070nd/staircase-core/src/internal/gate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupSignedGates creates a temp workspace with a valid gates.json + signing
// key pair + gates.json.sig so tests can exercise VerifyGatesSignature.
func setupSignedGates(t *testing.T) (wsDir string, gatesData []byte) {
	t.Helper()
	wsDir = t.TempDir()
	gatesData = []byte(`[{"name":"test","category":"security","severity":"WARN","script":"/bin/true"}]`)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "gates.json"), gatesData, 0o644))
	require.NoError(t, crypto.GenerateSigningKey(wsDir))

	privKey, err := crypto.LoadSigningKey(wsDir)
	require.NoError(t, err)
	sigHex := crypto.Sign(privKey, gatesData)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, gate.GatesSigFile), []byte(sigHex), 0o644))
	return wsDir, gatesData
}

// TestVerifyGatesSignature_valid returns (true, nil) when sidecar matches.
func TestVerifyGatesSignature_valid(t *testing.T) {
	wsDir, _ := setupSignedGates(t)
	sigPresent, err := gate.VerifyGatesSignature(wsDir)
	require.NoError(t, err)
	assert.True(t, sigPresent)
}

// TestVerifyGatesSignature_tampered returns (true, err) when gates.json is
// modified after signing.
func TestVerifyGatesSignature_tampered(t *testing.T) {
	wsDir, _ := setupSignedGates(t)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "gates.json"), []byte(`[]`), 0o644))

	sigPresent, err := gate.VerifyGatesSignature(wsDir)
	assert.True(t, sigPresent)
	assert.Error(t, err)
}

// TestVerifyGatesSignature_absent_sig returns (false, nil) when gates.json
// exists but no .sig is present.
func TestVerifyGatesSignature_absent_sig(t *testing.T) {
	wsDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "gates.json"), []byte(`[]`), 0o644))

	sigPresent, err := gate.VerifyGatesSignature(wsDir)
	assert.NoError(t, err)
	assert.False(t, sigPresent)
}

// TestVerifyGatesSignature_no_gates_file returns (false, nil) for empty workspace.
func TestVerifyGatesSignature_no_gates_file(t *testing.T) {
	sigPresent, err := gate.VerifyGatesSignature(t.TempDir())
	assert.NoError(t, err)
	assert.False(t, sigPresent)
}
