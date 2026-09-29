package governance

import (
	"crypto/ed25519"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sourceRepo(t *testing.T, files map[string][]byte) (dir string, commit func(map[string][]byte) string) {
	dir = t.TempDir()
	git := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "t@t")
	git("config", "user.name", "T")
	commit = func(files map[string][]byte) string {
		for name, b := range files {
			require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, name), b, 0o644))
		}
		git("add", "-A")
		git("commit", "-q", "-m", "governance")
		return git("rev-parse", "HEAD")
	}
	commit(files)
	return dir, commit
}

// TestUse_installs_the_teams_rules_and_keys: the pinned commit's policy,
// allowed signers and team keys are installed in the workspace, the policy
// re-signed with the workspace key, and the pin recorded; status sees the
// source move on and a local edit.
func TestUse_installs_the_teams_rules_and_keys(t *testing.T) {
	ws := t.TempDir()
	require.NoError(t, crypto.GenerateSigningKey(ws))
	alice, _, _ := ed25519.GenerateKey(nil)
	pol := []byte(`{"rules":[{"action_types":["file_edit"],"effect":"approve","allowed_extensions":[".md"]}]}`)
	src, commit := sourceRepo(t, map[string][]byte{"policy.json": pol, "allowed_signers": []byte("alice@example.com ssh-ed25519 AAAA\n"), "keys/alice.pub": alice})

	pin, err := Use(ws, src, "main")
	require.NoError(t, err)
	got, _ := os.ReadFile(filepath.Join(ws, "policy.json"))
	assert.Equal(t, string(pol), string(got))
	signed, err := policy.VerifyPolicySignature(ws)
	require.NoError(t, err)
	assert.True(t, signed, "re-signed with the workspace key")
	got, _ = os.ReadFile(filepath.Join(ws, "allowed_signers"))
	assert.Contains(t, string(got), "alice@example.com")
	keys, err := TrustedKeys(ws)
	require.NoError(t, err)
	assert.Len(t, keys, 2, "the workspace's own key and alice's")
	assert.Contains(t, keys, alice)

	st, err := Status(ws)
	require.NoError(t, err)
	assert.Equal(t, pin.Commit, st.Pin.Commit)
	assert.False(t, st.Behind)
	assert.Empty(t, st.Modified)

	commit(map[string][]byte{"policy.json": []byte(`{"rules":[]}`)})
	require.NoError(t, os.WriteFile(filepath.Join(ws, "allowed_signers"), []byte("mallory@example.com ssh-ed25519 BBBB\n"), 0o644))
	st, err = Status(ws)
	require.NoError(t, err)
	assert.True(t, st.Behind, "the source has a newer commit")
	assert.Equal(t, []string{"allowed_signers"}, st.Modified)
}

// TestUse_refuses_a_broken_bundle_and_changes_nothing: an invalid policy or
// a key that is not an Ed25519 public key is refused, and the workspace keeps
// what it had.
func TestUse_refuses_a_broken_bundle_and_changes_nothing(t *testing.T) {
	ws := t.TempDir()
	require.NoError(t, crypto.GenerateSigningKey(ws))
	before := []byte(`{"rules":[]}`)
	require.NoError(t, os.WriteFile(filepath.Join(ws, "policy.json"), before, 0o600))
	for name, files := range map[string]map[string][]byte{
		"misspelt policy field": {"policy.json": []byte(`{"rulez":[]}`)},
		"bad key":               {"policy.json": before, "keys/bob.pub": []byte("not a key")},
		"no policy":             {"README.md": []byte("x")},
	} {
		src, _ := sourceRepo(t, files)
		_, err := Use(ws, src, "main")
		assert.Error(t, err, name)
		got, _ := os.ReadFile(filepath.Join(ws, "policy.json"))
		assert.Equal(t, string(before), string(got), name)
		_, err = os.Stat(filepath.Join(ws, PinFile))
		assert.True(t, os.IsNotExist(err), name)
	}
}

// TestUse_installs_only_what_the_repository_has: without allowed_signers or
// keys in the repository, none are installed (an empty allowed_signers
// would look like a team with no reviewers).
func TestUse_installs_only_what_the_repository_has(t *testing.T) {
	ws := t.TempDir()
	src, _ := sourceRepo(t, map[string][]byte{"policy.json": []byte(`{"rules":[]}`)})
	pin, err := Use(ws, src, "main")
	require.NoError(t, err)
	assert.Equal(t, []string{"policy.json"}, slices.Collect(maps.Keys(pin.Files)))
	_, err = os.Stat(filepath.Join(ws, AllowedSigners))
	assert.True(t, os.IsNotExist(err))
}
