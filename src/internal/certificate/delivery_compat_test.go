package certificate_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// previousPredicate is the predicate of the release before the optional field delivery existed, frozen here: it has no Delivery.
type previousPredicate struct {
	Run        int64               `json:"run"`
	BaseCommit string              `json:"baseCommit"`
	PlanDigest string              `json:"planDigest,omitempty"`
	Agents     []string            `json:"agents"`
	Decisions  map[string]int      `json:"decisions"`
	ChainHead  string              `json:"chainHead"`
	CAL        int                 `json:"cal"`
	Notes      []string            `json:"notes,omitempty"`
	Policy     string              `json:"policy,omitempty"`
	Checks     []certificate.Check `json:"checks,omitempty"`
}

// previousVerify is the previous release's verifier, written from the specification's verification steps (certificate-v1.md,
// section 5: payload type, signature, statement, subject, level, required level, checks) and reading the predicate into its own
// struct, which does not know the field delivery. It is the reader of a verifier in another organisation that was never updated.
func previousVerify(env certificate.Envelope, pub ed25519.PublicKey, commit string, minCAL int) (int, error) {
	if env.PayloadType != "application/vnd.in-toto+json" {
		return 0, errors.New("payload type")
	}
	payload, err := base64.StdEncoding.DecodeString(env.Payload)
	if err != nil {
		return 0, err
	}
	signed := false
	for _, s := range env.Signatures {
		sig, err := base64.StdEncoding.DecodeString(s.Sig)
		if err == nil && ed25519.Verify(pub, certificate.PAE(env.PayloadType, payload), sig) {
			signed = true
		}
	}
	if !signed {
		return 0, errors.New("signature")
	}
	var st struct {
		Type          string `json:"_type"`
		PredicateType string `json:"predicateType"`
		Subject       []struct {
			Name   string            `json:"name"`
			Digest map[string]string `json:"digest"`
		} `json:"subject"`
		Predicate previousPredicate `json:"predicate"`
	}
	dec := json.NewDecoder(bytes.NewReader(payload)) // a verifier SHOULD ignore fields it does not know: no DisallowUnknownFields
	if err := dec.Decode(&st); err != nil {
		return 0, err
	}
	if st.Type != "https://in-toto.io/Statement/v1" || st.PredicateType != certificate.PredicateType || len(st.Subject) != 1 {
		return 0, errors.New("not a change certificate")
	}
	if st.Subject[0].Digest["gitCommit"] != commit {
		return 0, errors.New("is about another commit")
	}
	if st.Predicate.CAL < 1 || st.Predicate.CAL > 3 {
		return 0, errors.New("assurance level")
	}
	if st.Predicate.CAL < minCAL {
		return 0, errors.New("below the required level")
	}
	for _, c := range st.Predicate.Checks {
		if c.ExitCode != 0 {
			return 0, errors.New("failed")
		}
	}
	return st.Predicate.CAL, nil
}

// TestPreviousVerifier_ignores_the_delivery_field (ADR 0012, section 4; compatibility page: new optional fields may be added and
// verifiers ignore unknown ones): a certificate that says delivery: policy, signed with the rest, is accepted by the previous
// release's verifier at the level it computes, and is the plain certificate plus that one field: the predicate type is still version 1
// and the conformance vectors (which have no such field) are unchanged.
func TestPreviousVerifier_ignores_the_delivery_field(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	s := statement()
	s.Predicate.Checks = []certificate.Check{{Command: "go test ./...", ExitCode: 0, Sandboxed: true, OutputSHA256: "aa"}}
	s.Predicate.Delivery = "policy"
	env, err := certificate.Sign(s, priv)
	require.NoError(t, err)

	payload, err := base64.StdEncoding.DecodeString(env.Payload)
	require.NoError(t, err)
	require.Contains(t, string(payload), `"delivery":"policy"`, "the field is in the signed payload, so the old verifier really meets it")
	assert.Contains(t, string(payload), "#v1", "the predicate type stays version 1")

	level, err := previousVerify(env, pub, s.Commit(), 3)
	require.NoError(t, err, "the previous release's verifier accepts a certificate with a field it does not know")
	assert.Equal(t, 3, level, "the level is the one computed: delivery by policy is not a cap")

	// the current verifier decides the same, and a verifier's own policy can read the field
	got, err := certificate.Open(env, pub)
	require.NoError(t, err)
	require.NoError(t, got.Accept(s.Commit(), got.Predicate.CAL, 3))
	assert.Equal(t, "policy", got.Predicate.Delivery)

	// the certificate is the plain one plus the field and nothing else
	s.Predicate.Delivery = ""
	plainEnv, err := certificate.Sign(s, priv)
	require.NoError(t, err)
	plain, err := base64.StdEncoding.DecodeString(plainEnv.Payload)
	require.NoError(t, err)
	assert.Equal(t, string(plain), string(bytes.Replace(payload, []byte(`,"delivery":"policy"`), nil, 1)))
	_, err = previousVerify(plainEnv, pub, s.Commit(), 3)
	require.NoError(t, err)
}
