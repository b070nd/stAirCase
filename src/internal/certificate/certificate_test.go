package certificate_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPAE: the bytes DSSE signs, as in the DSSE specification's example.
func TestPAE(t *testing.T) {
	assert.Equal(t, "DSSEv1 29 http://example.com/HelloWorld 11 hello world",
		string(certificate.PAE("http://example.com/HelloWorld", []byte("hello world"))))
}

func statement() certificate.Statement {
	return certificate.New("0123456789abcdef0123456789abcdef01234567", certificate.Predicate{
		Run: 7, BaseCommit: "fedcba9876543210fedcba9876543210fedcba98", Agents: []string{"Claude Code"},
		ChainHead: "c0ffee", Decisions: map[string]int{"operator": 2}, CAL: 3})
}

// TestSignAndOpen: a signed certificate opens with the signer's public key
// and names the commit; any change to it, or another key, is refused.
func TestSignAndOpen(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	env, err := certificate.Sign(statement(), priv)
	require.NoError(t, err)

	got, err := certificate.Open(env, pub)
	require.NoError(t, err)
	assert.Equal(t, "0123456789abcdef0123456789abcdef01234567", got.Commit())
	assert.Equal(t, 3, got.Predicate.CAL)
	assert.Equal(t, certificate.PredicateType, got.PredicateType)

	other, _, _ := ed25519.GenerateKey(rand.Reader)
	_, err = certificate.Open(env, other)
	assert.ErrorContains(t, err, "signature")

	payload, _ := base64.StdEncoding.DecodeString(env.Payload)
	var s map[string]any
	require.NoError(t, json.Unmarshal(payload, &s))
	s["predicate"].(map[string]any)["cal"] = 4 // claim a higher level
	forged, _ := json.Marshal(s)
	env.Payload = base64.StdEncoding.EncodeToString(forged)
	_, err = certificate.Open(env, pub)
	assert.ErrorContains(t, err, "signature")
}

// TestOpen_refuses_other_payloads: only change certificates open.
func TestOpen_refuses_other_payloads(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	env, err := certificate.Sign(statement(), priv)
	require.NoError(t, err)
	env.PayloadType = "application/octet-stream"
	_, err = certificate.Open(env, pub)
	assert.Error(t, err)
}

// TestOpen_refuses_an_unearnable_level: a producer signs CAL 1 to 3 only; CAL 4
// is established by a verifier from an independent person's signature, so a
// freshly signed "cal":4 (or 0) is a false claim, not a certificate.
func TestOpen_refuses_an_unearnable_level(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	for _, cal := range []int{-1, 0, 4, 5} {
		s := statement()
		s.Predicate.CAL = cal
		env, err := certificate.Sign(s, priv)
		require.NoError(t, err)
		_, err = certificate.Open(env, pub)
		assert.ErrorContains(t, err, "assurance level", "cal %d", cal)
	}
	for _, cal := range []int{1, 2, 3} {
		s := statement()
		s.Predicate.CAL = cal
		env, _ := certificate.Sign(s, priv)
		_, err := certificate.Open(env, pub)
		assert.NoError(t, err, "cal %d", cal)
	}
}
