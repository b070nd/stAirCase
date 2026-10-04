package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/audit"
	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/governance"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os/exec"
)

// TestVerify_checks_a_commits_certificate: after a session, anyone with the
// workspace's public key can check that a commit carries a valid change
// certificate about exactly that commit, at the level they require; other
// commits, other keys and higher requirements fail.
func TestVerify_checks_a_commits_certificate(t *testing.T) {
	_, ws, git := sessionRepo(t)
	require.NoError(t, claudeSession(nil, []string{"add", "a", "health", "file"}))
	commit := strings.TrimSpace(git("rev-parse", "staircase/run-1"))

	verifyMinCAL, verifyKey, verifyFile = 0, "", ""
	require.NoError(t, verifyHandler(nil, []string{"staircase/run-1"}))
	verifyMinCAL = 3
	require.NoError(t, verifyHandler(nil, []string{commit}), "a gated Claude Code run reaches CAL 3")

	verifyMinCAL = 4
	assert.ErrorContains(t, verifyHandler(nil, []string{commit}), "CAL 3, below the required 4")
	verifyMinCAL = 0

	assert.ErrorContains(t, verifyHandler(nil, []string{"main"}), "no change certificate")

	other, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	verifyKey = filepath.Join(t.TempDir(), "other.pub")
	require.NoError(t, os.WriteFile(verifyKey, other, 0o600))
	assert.ErrorContains(t, verifyHandler(nil, []string{commit}), "signature")

	// A certificate moved onto another commit does not fit it.
	verifyKey = filepath.Join(ws, ".signing.pub")
	verifyFile = filepath.Join(ws, "audit", "run-1.certificate.json")
	assert.ErrorContains(t, verifyHandler(nil, []string{"main"}), "is about commit")
	verifyKey, verifyFile = "", ""
}

// TestVerify_refuses_a_claimed_level_4: the workspace key signs CAL 1 to 3; a
// certificate it freshly signed with "cal":4 (no reviewer) is a false claim and
// fails, however low the required level is.
func TestVerify_refuses_a_claimed_level_4(t *testing.T) {
	_, ws, _ := sessionRepo(t)
	require.NoError(t, claudeSession(nil, []string{"add", "a", "health", "file"}))
	file := filepath.Join(ws, "audit", "run-1.certificate.json")
	raw, err := os.ReadFile(file)
	require.NoError(t, err)
	var env certificate.Envelope
	require.NoError(t, json.Unmarshal(raw, &env))
	key, err := crypto.LoadSigningKey(ws)
	require.NoError(t, err)
	pub, err := certificate.OpenAny(env, []ed25519.PublicKey{key.Public().(ed25519.PublicKey)})
	require.NoError(t, err)
	pub.Predicate.CAL = 4
	forged, err := certificate.Sign(pub, key)
	require.NoError(t, err)
	raw, _ = json.Marshal(forged)
	claimed := filepath.Join(t.TempDir(), "claims-4.json")
	require.NoError(t, os.WriteFile(claimed, raw, 0o600))

	verifyMinCAL, verifyKey, verifyFile = 0, filepath.Join(ws, ".signing.pub"), claimed
	t.Cleanup(func() { verifyKey, verifyFile = "", "" })
	assert.ErrorContains(t, verifyHandler(nil, []string{"staircase/run-1"}), "assurance level 4")
}

// TestVerify_fails_a_failed_check: a commit whose recorded check failed does
// not pass verify, whatever its level.
func TestVerify_fails_a_failed_check(t *testing.T) {
	sessionRepo(t)
	runChecks = []string{"true", "exit 1"}
	t.Cleanup(func() { runChecks = nil })
	require.NoError(t, claudeSession(nil, []string{"add", "a", "health", "file"}))

	verifyMinCAL, verifyKey, verifyFile = 0, "", ""
	assert.ErrorContains(t, verifyHandler(nil, []string{"staircase/run-1"}), `check "exit 1" failed (exit 1)`)
}

// TestAnchorCertificate_sends_digests_only: anchoring a run's certificate
// sends Rekor the certificate (digests), never the run's content, and
// verify --check-anchor confirms the commit's certificate is in the log.
func TestAnchorCertificate_sends_digests_only(t *testing.T) {
	_, ws, git := sessionRepo(t)
	require.NoError(t, claudeSession(nil, []string{"add", "a", "health", "file"}))
	commit := strings.TrimSpace(git("rev-parse", "staircase/run-1"))

	var sent []string
	rekor := fakeRekor(t)
	spy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		sent = append(sent, string(b))
		req, _ := http.NewRequest(r.Method, rekor.URL+r.URL.Path, bytes.NewReader(b))
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(spy.Close)
	auditRekorURL = spy.URL
	t.Cleanup(func() { auditRekorURL = audit.DefaultRekorURL; verifyCheckAnchor = false })

	verifyMinCAL, verifyKey, verifyFile, verifyCheckAnchor = 0, "", "", true
	assert.ErrorContains(t, verifyHandler(nil, []string{commit}), "never anchored")

	require.NoError(t, auditAnchorHandler(nil, []string{"1"}))
	require.FileExists(t, filepath.Join(ws, "audit", "run-1.certificate.json.anchor"))
	require.Len(t, sent, 1)
	for _, content := range []string{"HEALTH.md", "add a health file", "ok\\n"} {
		assert.NotContains(t, decodedArtifact(t, sent[0]), content, "run content must not leave the machine")
	}

	require.NoError(t, verifyHandler(nil, []string{commit}))
}

// decodedArtifact is the artifact a rekord entry sends.
func decodedArtifact(t *testing.T, entry string) string {
	var e struct {
		Spec struct {
			Data struct {
				Content string `json:"content"`
			} `json:"data"`
		} `json:"spec"`
	}
	require.NoError(t, json.Unmarshal([]byte(entry), &e))
	b, err := base64.StdEncoding.DecodeString(e.Spec.Data.Content)
	require.NoError(t, err)
	return string(b)
}

// TestVerify_range: over a range, every commit that says an agent helped
// (Assisted-by:) must carry a valid certificate; a person's commit passes
// unless --all asks every commit to be certified.
func TestVerify_range(t *testing.T) {
	_, _, git := sessionRepo(t)
	require.NoError(t, claudeSession(nil, []string{"add", "a", "health", "file"}))
	git("checkout", "-q", "staircase/run-1")
	git("commit", "-q", "--allow-empty", "-m", "a person's commit")
	verifyMinCAL, verifyKey, verifyFile, verifyCheckAnchor, verifyAll = 3, "", "", false, false
	t.Cleanup(func() { verifyMinCAL, verifyAll = 0, false })

	out, err := captureStdout(t, func() error { return verifyHandler(nil, []string{"main..HEAD"}) })
	require.NoError(t, err, "the run's commit is certified, the person's needs none")
	assert.Contains(t, out, "1 commit(s) were not checked because they name no agent",
		"a pass that skipped commits says so: a commit that omits Assisted-by looks the same")

	verifyAll = true
	assert.ErrorContains(t, verifyHandler(nil, []string{"main..HEAD"}), "no change certificate")
	verifyAll = false

	git("commit", "-q", "--allow-empty", "-m", "sneaky\n\nAssisted-by: Claude Code")
	assert.ErrorContains(t, verifyHandler(nil, []string{"main..HEAD"}), "says an agent helped")
}

// TestVerify_trusts_the_team_keys: without --key, verify accepts a
// certificate signed by any key the governance repository lists, and not
// one signed by a key outside the team.
func TestVerify_trusts_the_team_keys(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "t@t")
	git("config", "user.name", "T")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x"), 0o644))
	git("add", "-A")
	git("commit", "-q", "-m", "teammate's run\n\nAssisted-by: Claude Code")
	commit := git("rev-parse", "HEAD")
	teammate, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	env, err := certificate.Sign(certificate.New(commit, certificate.Predicate{CAL: 3}), priv)
	require.NoError(t, err)
	b, _ := json.Marshal(env)
	git("notes", "--ref=staircase", "add", "-m", string(b), commit)

	ws := t.TempDir()
	require.NoError(t, crypto.GenerateSigningKey(ws))
	viper.Set("STAIRCASE_DIR", ws)
	t.Cleanup(func() { viper.Set("STAIRCASE_DIR", "") })
	t.Chdir(repo)
	verifyMinCAL, verifyKey, verifyFile, verifySigners = 0, "", "", ""
	assert.ErrorContains(t, verifyHandler(nil, []string{commit}), "signature", "the teammate is not trusted yet")

	require.NoError(t, os.MkdirAll(filepath.Join(ws, governance.TrustedKeysDir), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(ws, governance.TrustedKeysDir, "teammate.pub"), teammate, 0o644))
	assert.NoError(t, verifyHandler(nil, []string{commit}), "the team's key is trusted")
}

// TestVerify_range_output_cannot_drive_the_terminal: verify is run on pull
// requests other people wrote; the trailer it quotes is theirs.
func TestVerify_range_output_cannot_drive_the_terminal(t *testing.T) {
	_, _, git := sessionRepo(t)
	git("checkout", "-q", "-b", "other")
	git("commit", "-q", "--allow-empty", "-m", "sneaky\n\nAssisted-by: Evil\x1b[2J\x1b[1A\x1b[2K✅ all good\r")
	verifyMinCAL, verifyKey, verifyFile, verifyCheckAnchor, verifyAll = 0, "", "", false, false
	out, err := captureStdout(t, func() error { return verifyHandler(nil, []string{"main..HEAD"}) })
	require.Error(t, err)
	assert.Contains(t, out, "says an agent helped")
	assert.NotContains(t, out, "\x1b")
	assert.NotContains(t, out, "\r")
	assert.NotContains(t, err.Error(), "\x1b")
}
