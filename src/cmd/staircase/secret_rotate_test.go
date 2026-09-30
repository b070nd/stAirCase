package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setSecretFromStdin runs `staircase secret set name` with value on stdin.
func setSecretFromStdin(t *testing.T, name, value string) error {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	_, err = w.WriteString(value)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old; _ = r.Close() }()
	return secretSetCmd.RunE(secretSetCmd, []string{name})
}

// TestSecretRotate_a_crash_after_the_database_committed: the process dies
// after the secrets were re-encrypted but before the key file was replaced
// (real database, real files). Until the rotation is finished, nothing may
// use the old key: a write would leave a secret under a key that the finished
// rotation then drops (F101). `secret rotate` finishes it, and every secret,
// old and new, then decrypts with the one key.
func TestSecretRotate_a_crash_after_the_database_committed(t *testing.T) {
	ws := t.TempDir()
	viper.Set("STAIRCASE_DIR", ws)
	t.Cleanup(func() { viper.Set("STAIRCASE_DIR", "") })
	require.NoError(t, crypto.GenerateKey(ws))
	require.NoError(t, setSecretFromStdin(t, "BEFORE", "first value"))

	store, db, err := openStore()
	require.NoError(t, err)
	func() { // the rotation, killed right after the database transaction commits
		defer func() { require.NotNil(t, recover(), "the simulated crash") }()
		_ = crypto.RotateKey(ws, func(oldKey, newKey []byte) error {
			conn, err := db.Conn(context.Background())
			require.NoError(t, err)
			defer func() { _ = conn.Close() }()
			require.NoError(t, store.RotateSecretsOnConn(context.Background(), conn, oldKey, newKey, crypto.Decrypt, crypto.Encrypt))
			panic("killed")
		}, func([]byte) bool { return false })
	}()
	require.NoError(t, db.Close())

	_, err = crypto.LoadKey(ws)
	assert.ErrorContains(t, err, "rotation was interrupted", "nothing may use the old key now")
	assert.ErrorContains(t, setSecretFromStdin(t, "DURING", "written with the old key"), "rotation was interrupted")

	require.NoError(t, secretRotateCmd.RunE(secretRotateCmd, nil), "rotate finishes what was interrupted")
	require.NoError(t, setSecretFromStdin(t, "AFTER", "second value"))

	key, err := crypto.LoadKey(ws)
	require.NoError(t, err)
	store, db, err = openStore()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	all, err := store.ListAllSecrets()
	require.NoError(t, err)
	names := map[string]string{}
	for _, s := range all {
		v, err := crypto.Decrypt(key, s.EncryptedValue)
		require.NoError(t, err, "%s must decrypt with the workspace key", s.KeyName)
		names[s.KeyName] = v
	}
	assert.Equal(t, map[string]string{"BEFORE": "first value", "AFTER": "second value"}, names)
	_, statErr := os.Stat(filepath.Join(ws, ".key-rotate-journal"))
	assert.True(t, os.IsNotExist(statErr))
}

// TestSecretRotate_a_crash_before_the_database_committed: killed after the
// journal was written but before any secret was re-encrypted. The key is
// refused until `secret rotate` rolls the attempt back and rotates afresh; no
// secret is lost.
func TestSecretRotate_a_crash_before_the_database_committed(t *testing.T) {
	ws := t.TempDir()
	viper.Set("STAIRCASE_DIR", ws)
	t.Cleanup(func() { viper.Set("STAIRCASE_DIR", "") })
	require.NoError(t, crypto.GenerateKey(ws))
	require.NoError(t, setSecretFromStdin(t, "KEEP", "still here"))

	func() {
		defer func() { require.NotNil(t, recover(), "the simulated crash") }()
		_ = crypto.RotateKey(ws, func(_, _ []byte) error { panic("killed") }, func([]byte) bool { return false })
	}()
	_, err := crypto.LoadKey(ws)
	assert.ErrorContains(t, err, "rotation was interrupted")

	require.NoError(t, secretRotateCmd.RunE(secretRotateCmd, nil))
	key, err := crypto.LoadKey(ws)
	require.NoError(t, err)
	store, db, err := openStore()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	all, err := store.ListAllSecrets()
	require.NoError(t, err)
	require.Len(t, all, 1)
	v, err := crypto.Decrypt(key, all[0].EncryptedValue)
	require.NoError(t, err)
	assert.Equal(t, "still here", v)
}
