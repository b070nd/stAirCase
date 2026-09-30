package vsa_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/vsa"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func input() vsa.Input {
	return vsa.Input{
		Commit: "4b825dc642cb6eb9a060e54bf8d69288fbee4904", Tree: "9fceb02d0ae598e95dc970b74767f19372d61af8",
		ResourceURI: "git+https://github.com/acme/shop", VerifierID: "https://github.com/b070nd/stAirCase",
		VerifierVersion: "v0.7.0", Time: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		Params:      vsa.Parameters{MinCAL: 3, All: true, Keys: []string{"aa", "bb"}},
		CAL:         3,
		Certificate: []byte(`{"payloadType":"x"}`),
	}
}

// TestBuild_follows_the_slsa_verification_summary_format: the statement has the
// fields SLSA's VSA v1 requires, says PASSED, names what was verified with
// levels of its own, and claims no SLSA source level.
func TestBuild_follows_the_slsa_verification_summary_format(t *testing.T) {
	st := vsa.Build(input())
	b, err := json.Marshal(st)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))

	assert.Equal(t, "https://in-toto.io/Statement/v1", m["_type"])
	assert.Equal(t, "https://slsa.dev/verification_summary/v1", m["predicateType"])
	subject := m["subject"].([]any)[0].(map[string]any)
	assert.Equal(t, map[string]any{"gitCommit": "4b825dc642cb6eb9a060e54bf8d69288fbee4904", "gitTree": "9fceb02d0ae598e95dc970b74767f19372d61af8"}, subject["digest"])

	p := m["predicate"].(map[string]any)
	assert.Equal(t, "PASSED", p["verificationResult"])
	assert.Equal(t, "git+https://github.com/acme/shop", p["resourceUri"])
	assert.Equal(t, "2026-10-01T12:00:00Z", p["timeVerified"])
	assert.Equal(t, "1.2", p["slsaVersion"])
	verifier := p["verifier"].(map[string]any)
	assert.Equal(t, "https://github.com/b070nd/stAirCase", verifier["id"])
	assert.Equal(t, map[string]any{"staircase": "v0.7.0"}, verifier["version"])
	policy := p["policy"].(map[string]any)
	assert.NotEmpty(t, policy["uri"])
	digest := policy["digest"].(map[string]any)["sha256"].(string)
	params, _ := json.Marshal(input().Params)
	sum := sha256.Sum256(params)
	assert.Equal(t, hex.EncodeToString(sum[:]), digest, "the policy digest is of the parameters the verification ran with")
	assert.Equal(t, input().Params.Keys, []string{"aa", "bb"})
	assert.Contains(t, p, "https://github.com/b070nd/stAirCase/vsa-parameters/v1", "the parameters are visible, under a URI-named extension field")

	inputs := p["inputAttestations"].([]any)
	require.Len(t, inputs, 1)
	cert := sha256.Sum256(input().Certificate)
	assert.Equal(t, hex.EncodeToString(cert[:]), inputs[0].(map[string]any)["digest"].(map[string]any)["sha256"])
	assert.NotContains(t, p, "dependencyLevels", "no claim about dependencies")

	// Levels: our own, never a SLSA level, never the two-party property (CAL 4 is not SLSA's).
	levels := st.Predicate.VerifiedLevels
	assert.Contains(t, levels, "STAIRCASE_CAL_3")
	assert.Contains(t, levels, "SLSA_SOURCE_LEVEL_UNEVALUATED", "an explicit no-claim")
	for _, l := range levels {
		if strings.HasPrefix(l, "SLSA_") {
			assert.Equal(t, "SLSA_SOURCE_LEVEL_UNEVALUATED", l, "custom levels must not start with SLSA_, and we claim no SLSA level")
		}
		assert.NotEqual(t, "SLSA_SOURCE_TWO_PARTY_REVIEWED", l)
	}
}

func TestBuild_levels_say_what_was_verified(t *testing.T) {
	in := input()
	in.CAL, in.Rebuilt, in.ChecksPassed, in.InitiatorAuthenticated = 4, true, true, true
	in.Ledger = []byte("ledger")
	levels := vsa.Build(in).Predicate.VerifiedLevels
	assert.ElementsMatch(t, []string{"SLSA_SOURCE_LEVEL_UNEVALUATED", "STAIRCASE_CAL_4", "STAIRCASE_REBUILT", "STAIRCASE_CHECKS_PASSED", "STAIRCASE_INITIATOR_AUTHENTICATED"}, levels)
	assert.Len(t, vsa.Build(in).Predicate.InputAttestations, 2, "the certificate and the ledger")

	in = input()
	assert.ElementsMatch(t, []string{"SLSA_SOURCE_LEVEL_UNEVALUATED", "STAIRCASE_CAL_3"}, vsa.Build(in).Predicate.VerifiedLevels)
}

// TestSign_makes_a_dsse_envelope: the statement is signed as in-toto DSSE with
// the verifier's key and opens with it, and not with another key.
func TestSign_makes_a_dsse_envelope(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	env, err := vsa.Sign(vsa.Build(input()), priv)
	require.NoError(t, err)
	assert.Equal(t, "application/vnd.in-toto+json", env.PayloadType)
	require.Len(t, env.Signatures, 1)
	assert.Equal(t, certificate.KeyID(pub), env.Signatures[0].KeyID)
	payload, err := base64.StdEncoding.DecodeString(env.Payload)
	require.NoError(t, err)
	sig, _ := base64.StdEncoding.DecodeString(env.Signatures[0].Sig)
	assert.True(t, ed25519.Verify(pub, certificate.PAE(env.PayloadType, payload), sig))
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	assert.False(t, ed25519.Verify(other, certificate.PAE(env.PayloadType, payload), sig))
}

func TestResourceURI(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:acme/shop.git":              "git+https://github.com/acme/shop",
		"https://github.com/acme/shop.git":          "git+https://github.com/acme/shop",
		"https://github.com/acme/shop":              "git+https://github.com/acme/shop",
		"ssh://git@gitlab.example.com/team/app.git": "git+https://gitlab.example.com/team/app",
		"git+https://github.com/acme/shop":          "git+https://github.com/acme/shop",
	} {
		got, err := vsa.ResourceURI(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, bad := range []string{"", "/home/me/shop", "../shop", "https://user:secret@github.com/acme/shop"} {
		_, err := vsa.ResourceURI(bad)
		assert.Error(t, err, "%q: a local path is no repository URI, and credentials never go in", bad)
	}
}
