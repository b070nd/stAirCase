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

	"github.com/b070nd/stAirCase/src/internal/blueprint"
	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/persistence"
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

const helloBlueprint = `name: hello
supervisor: supervisor
agents:
  - {name: supervisor, model: claude-sonnet-4-6, prompt: "You coordinate the work."}
  - {name: coder, model: claude-sonnet-4-6, prompt_file: prompts/coder.md}
edges:
  - {from: supervisor, to: coder}
  - {from: coder, to: supervisor}
cases:
  - slug: greet
    prd: Create a greeting file
    stories:
      - text: Create GREETING.md
        scope: {allow: [GREETING.md], max_files: 1}
`

func teamRepo(t *testing.T) (string, func(map[string][]byte) string) {
	return sourceRepo(t, map[string][]byte{
		"policy.json":                       []byte(`{"rules":[]}`),
		"blueprints/hello/blueprint.yaml":   []byte(helloBlueprint),
		"blueprints/hello/prompts/coder.md": []byte("You write code.\n"),
	})
}

func newStore(t *testing.T, ws string) *persistence.Store {
	db, err := persistence.InitDB(ws)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return persistence.NewStore(db)
}

// TestUse_brings_the_teams_blueprints: blueprints/<name>/ of the pinned commit
// are read exactly as `blueprint import` reads a folder, checked, and imported
// as snapshots whose source commit is the pin, so every team member gets the
// same blueprint under the same hash.
func TestUse_brings_the_teams_blueprints(t *testing.T) {
	ws := t.TempDir()
	require.NoError(t, crypto.GenerateSigningKey(ws))
	src, _ := teamRepo(t)
	pin, err := Use(ws, src, "main")
	require.NoError(t, err)

	direct := filepath.Join(t.TempDir(), "hello")
	require.NoError(t, os.MkdirAll(filepath.Join(direct, "prompts"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(direct, "blueprint.yaml"), []byte(helloBlueprint), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(direct, "prompts", "coder.md"), []byte("You write code.\n"), 0o644))
	want, err := blueprint.Load(direct)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"hello": want.Hash()}, pin.Blueprints, "the same hash a direct import gives")

	store := newStore(t, ws)
	got, err := ImportBlueprints(store, ws, pin)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.True(t, got[0].Created)
	assert.Equal(t, "hello", got[0].Name)
	snap, err := store.FindBlueprint(want.Hash())
	require.NoError(t, err)
	require.NotNil(t, snap)
	assert.Equal(t, pin.Commit, snap.GitSHA, "reproducible: the source commit is the pinned one")

	again, err := ImportBlueprints(store, ws, pin)
	require.NoError(t, err)
	assert.False(t, again[0].Created, "importing again changes nothing")
}

// TestUse_refuses_a_bad_blueprint_and_changes_nothing: an invalid blueprint, one
// reaching outside its folder, one that is not a blueprint, or a symlink in it
// stops the whole install, policy included.
func TestUse_refuses_a_bad_blueprint_and_changes_nothing(t *testing.T) {
	for name, files := range map[string]map[string][]byte{
		"unknown field":     {"blueprints/hello/blueprint.yaml": []byte(strings.Replace(helloBlueprint, "supervisor: supervisor", "supervisor: supervisor\nsurprise: 1", 1))},
		"file outside":      {"blueprints/hello/blueprint.yaml": []byte(strings.Replace(helloBlueprint, "prompts/coder.md", "../../policy.json", 1))},
		"no blueprint.yaml": {"blueprints/empty/README.md": []byte("not a blueprint\n")},
	} {
		t.Run(name, func(t *testing.T) {
			ws := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(ws, "policy.json"), []byte(`{"rules":[],"limits":{"max_auto_approved":1}}`), 0o600))
			src, _ := sourceRepo(t, map[string][]byte{"policy.json": []byte(`{"rules":[]}`), "blueprints/hello/prompts/coder.md": []byte("x\n")})
			require.NoError(t, exec.Command("git", "-C", src, "rm", "-q", "-r", "blueprints/hello").Run())
			for f, b := range files {
				require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(src, f)), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(src, f), b, 0o644))
			}
			run := func(args ...string) {
				out, err := exec.Command("git", append([]string{"-C", src}, args...)...).CombinedOutput()
				require.NoError(t, err, string(out))
			}
			run("add", "-A")
			run("commit", "-q", "-m", "bad blueprint")
			_, err := Use(ws, src, "main")
			require.Error(t, err)
			got, _ := os.ReadFile(filepath.Join(ws, "policy.json"))
			assert.Contains(t, string(got), "max_auto_approved", "the old policy is still there")
			_, statErr := os.Stat(filepath.Join(ws, PinFile))
			assert.True(t, os.IsNotExist(statErr), "nothing was pinned")
		})
	}

	t.Run("symlink", func(t *testing.T) {
		ws := t.TempDir()
		src, commit := teamRepo(t)
		require.NoError(t, os.Symlink("../../policy.json", filepath.Join(src, "blueprints", "hello", "link.md")))
		commit(nil)
		_, err := Use(ws, src, "main")
		assert.ErrorContains(t, err, "symlink")
	})
}

// TestUse_keeps_old_blueprint_snapshots: a blueprint removed from the
// repository later is no longer listed by the pin, but the snapshot it had
// stays: runs bound to it keep their evidence.
func TestUse_keeps_old_blueprint_snapshots(t *testing.T) {
	ws := t.TempDir()
	src, commit := teamRepo(t)
	pin, err := Use(ws, src, "main")
	require.NoError(t, err)
	store := newStore(t, ws)
	_, err = ImportBlueprints(store, ws, pin)
	require.NoError(t, err)

	require.NoError(t, exec.Command("git", "-C", src, "rm", "-q", "-r", "blueprints").Run())
	commit(nil)
	pin2, err := Use(ws, src, "main")
	require.NoError(t, err)
	assert.Empty(t, pin2.Blueprints)
	all, err := store.ListBlueprints()
	require.NoError(t, err)
	assert.Len(t, all, 1, "the snapshot a bound run may depend on is kept")
}
