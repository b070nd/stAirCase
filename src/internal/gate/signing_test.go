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

// TestRunAll_unsigned_gates_blocked_when_key_present: a workspace that has
// signing infrastructure must not execute plugin gates from an unsigned
// gates.json — deleting the sidecar would otherwise bypass tamper detection.
func TestRunAll_unsigned_gates_blocked_when_key_present(t *testing.T) {
	gate.ReplaceRegistry(t, nil)
	ctx, wsDir := newGateEnv(t)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "gates.json"),
		[]byte(`[{"name":"plugin-probe","category":"security","severity":"WARN","script":"/bin/true"}]`), 0o644))
	require.NoError(t, crypto.GenerateSigningKey(wsDir))
	// deliberately no gates.json.sig

	r := gate.RunAll(ctx)

	assert.Equal(t, gate.StatusFail, r.Overall)
	assert.True(t, r.Blocking())
	require.NotEmpty(t, r.Gates)
	assert.Equal(t, "gates.json-integrity", r.Gates[0].Name)
	for _, g := range r.Gates {
		assert.NotEqual(t, "plugin-probe", g.Name, "plugin gate must not execute")
	}
}

// TestRunAll_unsigned_gates_warn_only_without_key: workspaces that never
// initialized signing keep the advisory warning and plugin gates still run.
func TestRunAll_unsigned_gates_warn_only_without_key(t *testing.T) {
	gate.ReplaceRegistry(t, nil)
	ctx, wsDir := newGateEnv(t)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "gates.json"),
		[]byte(`[{"name":"plugin-probe","category":"security","severity":"WARN","script":"/bin/true"}]`), 0o644))
	// no signing key, no sig — legacy/unsigned workspace

	r := gate.RunAll(ctx)

	executed := false
	for _, g := range r.Gates {
		assert.NotEqual(t, "gates.json-integrity", g.Name, "no integrity failure expected")
		if g.Name == "plugin-probe" {
			executed = true
		}
	}
	assert.True(t, executed, "plugin gate must still execute in unsigned workspace")
}
