// Package vsa builds SLSA Verification Summary Attestations (VSA v1, SLSA
// v1.2) for commits that `staircase verify` accepted: what was verified, by
// whom, under which policy, from which attestations. The levels it names are
// stAirCase's own (STAIRCASE_*), never a SLSA source level: a change assurance
// level is not one (docs/standards.md), and SLSA reserves SLSA_* for its own.
package vsa

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/b070nd/stAirCase/src/internal/certificate"
)

// PredicateType is the VSA v1 predicate type.
const PredicateType = "https://slsa.dev/verification_summary/v1"

// ParametersField names the extension field that carries the parameters of
// the verification (VSA extension fields are named by URIs).
const ParametersField = "https://github.com/b070nd/stAirCase/vsa-parameters/v1"

// DefaultPolicyURI says what the policy digest covers.
const DefaultPolicyURI = "https://github.com/b070nd/stAirCase/blob/master/docs/spec/vsa-v1.md#policy"

// Statement is an in-toto Statement v1 with a VSA predicate.
type Statement struct {
	Type          string    `json:"_type"`
	Subject       []Subject `json:"subject"`
	PredicateType string    `json:"predicateType"`
	Predicate     Predicate `json:"predicate"`
}

// Subject is the verified revision.
type Subject struct {
	Digest map[string]string `json:"digest"`
}

// Predicate is the VSA. Parameters is marshalled under ParametersField.
type Predicate struct {
	Verifier           Verifier     `json:"verifier"`
	TimeVerified       string       `json:"timeVerified,omitempty"`
	ResourceURI        string       `json:"resourceUri"`
	Policy             Descriptor   `json:"policy"`
	InputAttestations  []Descriptor `json:"inputAttestations,omitempty"`
	VerificationResult string       `json:"verificationResult"`
	VerifiedLevels     []string     `json:"verifiedLevels"`
	SLSAVersion        string       `json:"slsaVersion,omitempty"`
	Parameters         Parameters   `json:"-"`
}

// MarshalJSON adds the parameters as the URI-named extension field.
func (p Predicate) MarshalJSON() ([]byte, error) {
	type plain Predicate
	b, err := json.Marshal(plain(p))
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m[ParametersField], err = json.Marshal(p.Parameters); err != nil {
		return nil, err
	}
	return json.Marshal(m)
}

// Verifier identifies who verified.
type Verifier struct {
	ID      string            `json:"id"`
	Version map[string]string `json:"version,omitempty"`
}

// Descriptor is an in-toto ResourceDescriptor.
type Descriptor struct {
	Name   string            `json:"name,omitempty"`
	URI    string            `json:"uri,omitempty"`
	Digest map[string]string `json:"digest,omitempty"`
}

// Parameters are what the verification ran with; the policy digest is the
// SHA-256 of their JSON, so two VSAs with the same digest were made under the
// same rules.
type Parameters struct {
	MinCAL           int      `json:"minCal"`
	All              bool     `json:"all"`
	Rebuild          bool     `json:"rebuild"`
	RequireInitiator bool     `json:"requireInitiator"`
	Keys             []string `json:"keys"`    // the trusted producer keys (their IDs), sorted
	Signers          string   `json:"signers"` // SHA-256 of the allowed_signers file, or ""
}

// Input is what a successful verification knows.
type Input struct {
	Commit, Tree           string
	ResourceURI            string
	VerifierID             string
	VerifierVersion        string
	Time                   time.Time
	PolicyURI              string // default DefaultPolicyURI
	Params                 Parameters
	CAL                    int
	Rebuilt                bool
	ChecksPassed           bool
	InitiatorAuthenticated bool
	Certificate, Ledger    []byte // the attestations the verification used, as read
}

func digest(b []byte) map[string]string {
	sum := sha256.Sum256(b)
	return map[string]string{"sha256": hex.EncodeToString(sum[:])}
}

// Build is the VSA for a commit that passed verification.
func Build(in Input) Statement {
	params, _ := json.Marshal(in.Params)
	policy := in.PolicyURI
	if policy == "" {
		policy = DefaultPolicyURI
	}
	levels := []string{"SLSA_SOURCE_LEVEL_UNEVALUATED", fmt.Sprintf("STAIRCASE_CAL_%d", in.CAL)}
	if in.Rebuilt {
		levels = append(levels, "STAIRCASE_REBUILT")
	}
	if in.ChecksPassed {
		levels = append(levels, "STAIRCASE_CHECKS_PASSED")
	}
	if in.InitiatorAuthenticated {
		levels = append(levels, "STAIRCASE_INITIATOR_AUTHENTICATED")
	}
	inputs := []Descriptor{{Name: "refs/notes/staircase", Digest: digest(in.Certificate)}}
	if len(in.Ledger) > 0 {
		inputs = append(inputs, Descriptor{Name: "refs/notes/staircase-ledger", Digest: digest(in.Ledger)})
	}
	version := map[string]string{"staircase": in.VerifierVersion}
	if in.VerifierVersion == "" {
		version = nil
	}
	return Statement{
		Type:          "https://in-toto.io/Statement/v1",
		PredicateType: PredicateType,
		Subject:       []Subject{{Digest: map[string]string{"gitCommit": in.Commit, "gitTree": in.Tree}}},
		Predicate: Predicate{
			Verifier:           Verifier{ID: in.VerifierID, Version: version},
			TimeVerified:       in.Time.UTC().Format(time.RFC3339),
			ResourceURI:        in.ResourceURI,
			Policy:             Descriptor{URI: policy, Digest: digest(params)},
			InputAttestations:  inputs,
			VerificationResult: "PASSED",
			VerifiedLevels:     levels,
			SLSAVersion:        "1.2",
			Parameters:         in.Params,
		},
	}
}

// Sign signs s as in-toto DSSE with the verifier's key.
func Sign(s Statement, priv ed25519.PrivateKey) (certificate.Envelope, error) {
	payload, err := json.Marshal(s)
	if err != nil {
		return certificate.Envelope{}, err
	}
	return certificate.SignPayload(payload, priv), nil
}

// ResourceURI turns a git remote into the repository URI a source VSA uses
// (git+https://host/org/repo). A local path is no repository URI, and a URL that
// carries a password is refused rather than copied into an attestation.
func ResourceURI(remote string) (string, error) {
	r := strings.TrimSpace(remote)
	if r == "" {
		return "", errors.New("no repository URI: the repository has no remote (set one, or pass --resource-uri)")
	}
	r = strings.TrimPrefix(r, "git+")
	switch {
	case strings.HasPrefix(r, "git@") && strings.Contains(r, ":") && !strings.Contains(r, "://"): // scp-like
		host, path, _ := strings.Cut(strings.TrimPrefix(r, "git@"), ":")
		r = "https://" + host + "/" + path
	case !strings.Contains(r, "://"):
		return "", fmt.Errorf("%q is not a repository URL (pass --resource-uri)", remote)
	}
	u, err := url.Parse(r)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%q is not a repository URL (pass --resource-uri)", remote)
	}
	if _, has := u.User.Password(); has {
		return "", errors.New("the remote URL carries a password; it is not copied into an attestation (pass --resource-uri)")
	}
	path := strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
	if path == "" {
		return "", fmt.Errorf("%q names no repository (pass --resource-uri)", remote)
	}
	return "git+https://" + u.Hostname() + "/" + path, nil
}
