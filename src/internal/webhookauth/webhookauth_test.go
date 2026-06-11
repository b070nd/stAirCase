package webhookauth_test

import (
	"testing"
	"time"

	"github.com/b070nd/staircase-core/src/internal/webhookauth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignVerify_roundtrip(t *testing.T) {
	secret := []byte("project-shared-secret")
	body := []byte(`{"type":"yield_response","approved":true}`)
	ts := "1700000000"
	now := time.Unix(1700000000, 0)

	sig := webhookauth.Sign(secret, ts, body)
	assert.Contains(t, sig, "sha256=")
	require.NoError(t, webhookauth.Verify(secret, ts, sig, body, now, webhookauth.DefaultMaxSkew))
}

func TestVerify_rejects_tampered_body(t *testing.T) {
	secret := []byte("s")
	ts := "1700000000"
	now := time.Unix(1700000000, 0)
	sig := webhookauth.Sign(secret, ts, []byte("approved:true"))

	err := webhookauth.Verify(secret, ts, sig, []byte("approved:false"), now, webhookauth.DefaultMaxSkew)
	assert.Error(t, err, "a modified body must fail verification")
}

func TestVerify_rejects_wrong_secret(t *testing.T) {
	ts := "1700000000"
	now := time.Unix(1700000000, 0)
	body := []byte("x")
	sig := webhookauth.Sign([]byte("right"), ts, body)

	assert.Error(t, webhookauth.Verify([]byte("wrong"), ts, sig, body, now, webhookauth.DefaultMaxSkew))
}

func TestVerify_rejects_missing_headers(t *testing.T) {
	now := time.Unix(1700000000, 0)
	assert.Error(t, webhookauth.Verify([]byte("s"), "", "", []byte("x"), now, webhookauth.DefaultMaxSkew))
}

func TestVerify_rejects_stale_timestamp(t *testing.T) {
	secret := []byte("s")
	ts := "1700000000"
	body := []byte("x")
	sig := webhookauth.Sign(secret, ts, body)
	// now is 10 minutes after the signed timestamp → outside the 5-minute window.
	now := time.Unix(1700000000, 0).Add(10 * time.Minute)

	assert.Error(t, webhookauth.Verify(secret, ts, sig, body, now, webhookauth.DefaultMaxSkew),
		"a replayed/stale request must be rejected")
}
