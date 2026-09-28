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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
