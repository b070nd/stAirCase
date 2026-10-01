package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

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

// TestDrillRotateHelper is the process the SIGKILL drill kills: it is only a
// test when the drill asks for it, and then rotates the workspace key over and
// over until it is killed.
func TestDrillRotateHelper(t *testing.T) {
	ws := os.Getenv("STAIRCASE_DRILL_ROTATE_WS")
	if ws == "" {
		t.Skip("only run by the drill")
	}
	viper.Set("STAIRCASE_DIR", ws)
	null, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	os.Stdout = null // the command prints its success each time
	for {
		_ = secretRotateCmd.RunE(secretRotateCmd, nil)
	}
}

// TestDrill_sigkill_during_secret_rotate: `secret rotate` runs as a real
// process in a loop and is killed with SIGKILL at random moments, 150 times,
// against a real database. After each kill the workspace must be usable again:
// the key either loads and every secret decrypts under it, or it is refused as
// "interrupted" and one `secret rotate` repairs it, after which every secret
// decrypts. A secret must never be stranded (F101).
func TestDrill_sigkill_during_secret_rotate(t *testing.T) {
	if testing.Short() {
		t.Skip("drill: skipped in -short mode")
	}
	ws := t.TempDir()
	viper.Set("STAIRCASE_DIR", ws)
	t.Cleanup(func() { viper.Set("STAIRCASE_DIR", "") })
	require.NoError(t, crypto.GenerateKey(ws))
	secrets := map[string]string{"ALPHA": "one", "BRAVO": "two", "CHARLIE": "three", "DELTA": "four", "ECHO": "five"}
	for name, v := range secrets {
		require.NoError(t, setSecretFromStdin(t, name, v))
	}

	check := func(when string) {
		key, err := crypto.LoadKey(ws)
		require.NoError(t, err, when)
		store, db, err := openStore()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		all, err := store.ListAllSecrets()
		require.NoError(t, err)
		require.Len(t, all, len(secrets), when)
		for _, s := range all {
			v, err := crypto.Decrypt(key, s.EncryptedValue)
			require.NoError(t, err, "%s: %s is stranded under another key", when, s.KeyName)
			require.Equal(t, secrets[s.KeyName], v, when)
		}
	}

	self, err := os.Executable()
	require.NoError(t, err)
	rng := rand.New(rand.NewPCG(1, 2)) // fixed seed: the timings are random-looking but a failure can be replayed
	outcomes := map[string]int{}
	for i := 0; i < 150; i++ {
		cmd := exec.Command(self, "-test.run=^TestDrillRotateHelper$")
		cmd.Env = append(os.Environ(), "STAIRCASE_DRILL_ROTATE_WS="+ws)
		require.NoError(t, cmd.Start())
		time.Sleep(time.Duration(40+rng.IntN(160)) * time.Millisecond) // past the start-up, into the rotations
		require.NoError(t, cmd.Process.Signal(syscall.SIGKILL))
		_ = cmd.Wait()

		stage := "no journal"
		if b, err := os.ReadFile(filepath.Join(ws, ".key-rotate-journal")); err == nil {
			stage = "journal " + map[bool]string{true: "pending", false: "committed"}[strings.Contains(string(b), `"pending"`)]
		}
		if _, err := crypto.LoadKey(ws); err != nil {
			require.ErrorContains(t, err, "rotation was interrupted", "iteration %d: the only acceptable refusal", i)
			require.NoError(t, secretRotateCmd.RunE(secretRotateCmd, nil), "iteration %d: one rotate repairs it", i)
			outcomes[stage+": refused, repaired by one rotate"]++
		} else {
			outcomes[stage+": key loads"]++
		}
		check(fmt.Sprintf("iteration %d (%s)", i, stage))
	}
	t.Logf("150 SIGKILLs of a rotating process: %v", outcomes)
	require.NoError(t, secretRotateCmd.RunE(secretRotateCmd, nil))
	left, _ := filepath.Glob(filepath.Join(ws, ".key-rotate-*"))
	t.Logf("files a kill left behind after a final rotate: %v", left)
	assert.Empty(t, left, "a killed rotation must not leave key material lying around")
}
