// Package certificate is the change certificate (docs/adr/0002): a small
// in-toto statement about one commit, signed in a DSSE envelope, that says
// how the change was made and decided. It holds digests only, never content.
package certificate

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// PredicateType names this predicate and its version.
const PredicateType = "https://github.com/b070nd/stAirCase/blob/master/docs/adr/0002-change-certificate.md#v1"

const (
	statementType = "https://in-toto.io/Statement/v1"
	payloadType   = "application/vnd.in-toto+json"
)

// Statement is an in-toto Statement v1 about one commit.
type Statement struct {
	Type          string    `json:"_type"`
	Subject       []Subject `json:"subject"`
	PredicateType string    `json:"predicateType"`
	Predicate     Predicate `json:"predicate"`
}

// Subject is the commit the statement is about. The name is generic so a
// private repository's name does not leak.
type Subject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

// Predicate is how the change was made and decided.
type Predicate struct {
	Run        int64          `json:"run"`
	BaseCommit string         `json:"baseCommit"`
	PlanDigest string         `json:"planDigest,omitempty"` // the compiled plan: task, stories, scope, agents
	Blueprint  string         `json:"blueprint,omitempty"`
	Agents     []string       `json:"agents"`          // the harness, or the models of the built-in agents
	Decisions  map[string]int `json:"decisions"`       // how many proposals each source decided
	ChainHead  string         `json:"chainHead"`       // the audit chain's last hash when the commit was made
	CAL        int            `json:"cal"`             // the change assurance level reached (ADR 0001)
	Notes      []string       `json:"notes,omitempty"` // why the level is not higher
	// RequestedBy is the git identity (user.email) the run was made under:
	// a second person's signature must come from someone else (CAL 4).
	RequestedBy string `json:"requestedBy,omitempty"`
	// Checks ran on a clean checkout of the commit after it was made.
	Checks []Check `json:"checks,omitempty"`
}

// Check is one command run on the commit, such as its tests. It records the
// output's digest, not the output.
type Check struct {
	Command      string `json:"command"`
	ExitCode     int    `json:"exitCode"` // -1: it could not run
	Sandboxed    bool   `json:"sandboxed"`
	OutputSHA256 string `json:"outputSha256"`
}

// New is a statement about commit.
func New(commit string, p Predicate) Statement {
	return Statement{Type: statementType, PredicateType: PredicateType, Predicate: p,
		Subject: []Subject{{Name: "commit", Digest: map[string]string{"gitCommit": commit}}}}
}

// Commit is the commit the statement is about.
func (s Statement) Commit() string {
	if len(s.Subject) == 0 {
		return ""
	}
	return s.Subject[0].Digest["gitCommit"]
}

// Envelope is a DSSE envelope.
type Envelope struct {
	PayloadType string      `json:"payloadType"`
	Payload     string      `json:"payload"` // base64
	Signatures  []Signature `json:"signatures"`
}

// Signature is one DSSE signature.
type Signature struct {
	KeyID string `json:"keyid"`
	Sig   string `json:"sig"` // base64
}

// SSHSignature marks a person's signature in an envelope: its keyid is this
// prefix and the signer's principal, its sig the base64 of an armored SSH
// signature (ssh-keygen -Y sign) over the envelope's PAE in SSHNamespace.
const (
	SSHSignature = "sshsig:"
	SSHNamespace = "staircase-certificate"
)

// PAE is the DSSE pre-authentication encoding: the bytes that are signed.
func PAE(payloadType string, payload []byte) []byte {
	return fmt.Appendf(nil, "DSSEv1 %d %s %d %s", len(payloadType), payloadType, len(payload), payload)
}

// KeyID names a public key: the hex SHA-256 of its bytes.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])
}

// Sign signs s with priv.
func Sign(s Statement, priv ed25519.PrivateKey) (Envelope, error) {
	payload, err := json.Marshal(s)
	if err != nil {
		return Envelope{}, err
	}
	sig := ed25519.Sign(priv, PAE(payloadType, payload))
	return Envelope{PayloadType: payloadType, Payload: base64.StdEncoding.EncodeToString(payload),
		Signatures: []Signature{{KeyID: KeyID(priv.Public().(ed25519.PublicKey)), Sig: base64.StdEncoding.EncodeToString(sig)}}}, nil
}

// Open verifies env against pub and returns its statement. It refuses an
// envelope that is not a change certificate or not signed by pub.
func Open(env Envelope, pub ed25519.PublicKey) (Statement, error) {
	var s Statement
	if env.PayloadType != payloadType {
		return s, fmt.Errorf("not an in-toto statement (payload type %q)", env.PayloadType)
	}
	payload, err := base64.StdEncoding.DecodeString(env.Payload)
	if err != nil {
		return s, fmt.Errorf("payload: %w", err)
	}
	signed := false
	for _, sg := range env.Signatures {
		sig, err := base64.StdEncoding.DecodeString(sg.Sig)
		if err == nil && ed25519.Verify(pub, PAE(env.PayloadType, payload), sig) {
			signed = true
			break
		}
	}
	if !signed {
		return s, errors.New("no valid signature by the trusted key")
	}
	if err := json.Unmarshal(payload, &s); err != nil {
		return s, fmt.Errorf("statement: %w", err)
	}
	if s.Type != statementType || s.PredicateType != PredicateType || s.Commit() == "" {
		return s, fmt.Errorf("not a change certificate (%s, %s)", s.Type, s.PredicateType)
	}
	return s, nil
}

// Accept decides whether an opened certificate lets commit through: it must
// be about exactly that commit, reach minCAL at level (its CAL, or 4 with a
// trusted second person's signature), and record no failed check.
func (s Statement) Accept(commit string, level, minCAL int) error {
	if s.Commit() != commit {
		return fmt.Errorf("the certificate is about commit %.12s, not %.12s", s.Commit(), commit)
	}
	p := s.Predicate
	if level < minCAL {
		why := ""
		if len(p.Notes) > 0 {
			why = ": " + strings.Join(p.Notes, "; ")
		}
		return fmt.Errorf("commit %.12s reached CAL %d, below the required %d%s", commit, level, minCAL, why)
	}
	for _, c := range p.Checks {
		if c.ExitCode != 0 {
			return fmt.Errorf("commit %.12s: check %q failed (exit %d)", commit, c.Command, c.ExitCode)
		}
	}
	return nil
}
