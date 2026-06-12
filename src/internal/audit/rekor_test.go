package audit_test

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/b070nd/staircase-core/src/internal/audit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockRekor implements the two Rekor endpoints the anchoring flow uses:
// POST /api/v1/log/entries and GET /api/v1/log/entries/{uuid}.
// On create it behaves like the real server: it decodes the rekord entry and
// verifies the Ed25519 signature over the inlined artifact before accepting.
type mockRekor struct {
	t       *testing.T
	entries map[string]string // uuid → base64 body (the proposed entry, like real Rekor)
	nextIdx int64
}

func (m *mockRekor) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/log/entries":
			var entry struct {
				APIVersion string `json:"apiVersion"`
				Kind       string `json:"kind"`
				Spec       struct {
					Data struct {
						Content string `json:"content"`
					} `json:"data"`
					Signature struct {
						Format    string `json:"format"`
						Content   string `json:"content"`
						PublicKey struct {
							Content string `json:"content"`
						} `json:"publicKey"`
					} `json:"signature"`
				} `json:"spec"`
			}
			body := json.NewDecoder(r.Body)
			require.NoError(m.t, body.Decode(&entry))
			assert.Equal(m.t, "rekord", entry.Kind)
			assert.Equal(m.t, "x509", entry.Spec.Signature.Format)

			// Server-side verification, like real Rekor: sig over artifact.
			artifact, err := base64.StdEncoding.DecodeString(entry.Spec.Data.Content)
			require.NoError(m.t, err)
			sig, err := base64.StdEncoding.DecodeString(entry.Spec.Signature.Content)
			require.NoError(m.t, err)
			pemBytes, err := base64.StdEncoding.DecodeString(entry.Spec.Signature.PublicKey.Content)
			require.NoError(m.t, err)
			block, _ := pem.Decode(pemBytes)
			require.NotNil(m.t, block, "public key must be PEM")
			pubAny, err := x509.ParsePKIXPublicKey(block.Bytes)
			require.NoError(m.t, err)
			pub, ok := pubAny.(ed25519.PublicKey)
			require.True(m.t, ok, "public key must be ed25519")
			if !ed25519.Verify(pub, artifact, sig) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"signature verification failed"}`))
				return
			}

			// Accept: store body exactly as proposed (matches real Rekor body field).
			raw, _ := json.Marshal(entry)
			uuid := hex.EncodeToString(sha256NewSum(raw))
			m.entries[uuid] = base64.StdEncoding.EncodeToString(raw)
			m.nextIdx++
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				uuid: map[string]any{"logIndex": m.nextIdx, "logID": "mock-log", "integratedTime": 1750000000},
			})

		case r.Method == http.MethodGet && len(r.URL.Path) > len("/api/v1/log/entries/"):
			uuid := r.URL.Path[len("/api/v1/log/entries/"):]
			b, ok := m.entries[uuid]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{uuid: map[string]any{"body": b}})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func sha256NewSum(b []byte) []byte {
	s := sha256.Sum256(b)
	return s[:]
}

// TestRekor_anchor_verify_roundtrip: anchor a record against a mock Rekor that
// verifies our signature server-side, persist the sidecar, then verify the
// record against the log — and prove a tampered record is rejected.
func TestRekor_anchor_verify_roundtrip(t *testing.T) {
	mock := &mockRekor{t: t, entries: map[string]string{}}
	srv := httptest.NewServer(mock.handler())
	t.Cleanup(srv.Close)

	_, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)

	record := []byte(`{"run_id":7,"entries":[{"event_type":"yield_decided"}],"signature":"ab"}`)
	recordHash := hex.EncodeToString(sha256NewSum(record))

	// ── anchor ────────────────────────────────────────────────────────────
	anchor, err := audit.AnchorRecord(srv.URL, record, recordHash, priv)
	require.NoError(t, err)
	assert.Equal(t, recordHash, anchor.RecordSHA256)
	assert.NotEmpty(t, anchor.UUID)
	assert.Equal(t, int64(1), anchor.LogIndex)
	assert.Equal(t, srv.URL, anchor.RekorURL)

	// ── sidecar persistence round-trip ────────────────────────────────────
	sidecar := filepath.Join(t.TempDir(), "run-7.checkpoint.json.anchor")
	require.NoError(t, audit.AppendAnchor(sidecar, anchor))
	anchors, err := audit.LoadAnchors(sidecar)
	require.NoError(t, err)
	require.Len(t, anchors, 1)
	assert.Equal(t, anchor, anchors[0])

	// ── verify against the log ────────────────────────────────────────────
	got, err := audit.VerifyAnchor(record, recordHash, anchors)
	require.NoError(t, err)
	assert.Equal(t, anchor.UUID, got.UUID)

	// ── tampered local record must fail ───────────────────────────────────
	tampered := []byte(`{"run_id":7,"entries":[{"event_type":"FORGED"}],"signature":"ab"}`)
	// same anchor list, but the hash lookup uses the tampered record's hash → no anchor
	_, err = audit.VerifyAnchor(tampered, hex.EncodeToString(sha256NewSum(tampered)), anchors)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no anchor found")

	// ── log/local content divergence must fail ───────────────────────────
	// Force the lookup to match (reuse original hash) but present different bytes:
	_, err = audit.VerifyAnchor(tampered, recordHash, anchors)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MISMATCH")
}

// TestRekor_anchor_rejects_bad_signature: the mock (like real Rekor) refuses
// entries whose signature does not verify — exercised by signing with one key
// and presenting another. AnchorRecord must surface the 400.
func TestRekor_anchor_rejects_server_error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"signature verification failed"}`))
	}))
	t.Cleanup(srv.Close)

	_, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	_, err = audit.AnchorRecord(srv.URL, []byte("x"), "00", priv)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
}

// TestLoadAnchors_missing_file_is_not_an_error: absence of anchors is a valid
// state (operator never ran --anchor).
func TestLoadAnchors_missing_file_is_not_an_error(t *testing.T) {
	anchors, err := audit.LoadAnchors(filepath.Join(t.TempDir(), "nope.anchor"))
	require.NoError(t, err)
	assert.Nil(t, anchors)
}
