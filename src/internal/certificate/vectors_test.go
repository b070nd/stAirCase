package certificate

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// vector is one conformance case (docs/spec/vectors): verify envelope
// against publicKey for commit, requiring minCal; the result must be valid
// (at level cal) or a refusal whose reason contains error.
type vector struct {
	Description string   `json:"description"`
	PublicKey   string   `json:"publicKey"` // base64 of the raw 32-byte Ed25519 key
	Commit      string   `json:"commit"`
	MinCAL      int      `json:"minCal"`
	Envelope    Envelope `json:"envelope"`
	Valid       bool     `json:"valid"`
	CAL         int      `json:"cal,omitempty"`
	Error       string   `json:"error,omitempty"`
}

const vectorCommit = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// vectors builds the conformance cases. Ed25519 and Go's JSON encoding are
// deterministic, so the files never change unless the format does.
func vectors(t *testing.T) map[string]vector {
	priv := ed25519.NewKeyFromSeed([]byte("staircase conformance test key!!"))
	other := ed25519.NewKeyFromSeed([]byte("some other key, not the trusted!"))
	pub := base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey))
	pred := Predicate{Run: 7, BaseCommit: "9fceb02d0ae598e95dc970b74767f19372d61af8", PlanDigest: "5d41402abc4b2a76b9719d911017c592",
		Agents: []string{"Claude Code"}, Decisions: map[string]int{"operator": 2, "policy": 1},
		ChainHead: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", CAL: 3, RequestedBy: "dev@example.com",
		Checks: []Check{{Command: "go test ./...", ExitCode: 0, Sandboxed: true, OutputSHA256: "2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae"}}}
	sign := func(p Predicate, commit string, key ed25519.PrivateKey) Envelope {
		env, err := Sign(New(commit, p), key)
		require.NoError(t, err)
		return env
	}
	valid := sign(pred, vectorCommit, priv)
	cal2 := pred
	cal2.CAL, cal2.Notes = 2, []string{"commands changed files that were reviewed after the fact"}
	failed := pred
	failed.Checks = []Check{{Command: "go test ./...", ExitCode: 1, Sandboxed: true, OutputSHA256: pred.Checks[0].OutputSHA256}}

	tampered := valid
	payload, _ := base64.StdEncoding.DecodeString(valid.Payload)
	tampered.Payload = base64.StdEncoding.EncodeToString(bytes.Replace(payload, []byte(`"cal":3`), []byte(`"cal":4`), 1))
	extra := valid
	extra.Signatures = append([]Signature{{KeyID: KeyID(other.Public().(ed25519.PublicKey)), Sig: sign(pred, vectorCommit, other).Signatures[0].Sig}}, valid.Signatures...)
	unsigned := valid
	unsigned.Signatures = nil
	otherType := valid
	otherType.PayloadType = "application/json"
	st := New(vectorCommit, pred)
	st.PredicateType = "https://slsa.dev/provenance/v1"
	b, _ := json.Marshal(st)
	wrongPredicate := Envelope{PayloadType: payloadType, Payload: base64.StdEncoding.EncodeToString(b),
		Signatures: []Signature{{KeyID: KeyID(priv.Public().(ed25519.PublicKey)), Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, PAE(payloadType, b)))}}}

	v := func(desc string, env Envelope, commit string, minCAL int, ok bool, cal int, why string) vector {
		return vector{Description: desc, PublicKey: pub, Commit: commit, MinCAL: minCAL, Envelope: env, Valid: ok, CAL: cal, Error: why}
	}
	return map[string]vector{
		"01-valid-cal3":            v("A valid CAL 3 certificate with a passing check.", valid, vectorCommit, 3, true, 3, ""),
		"02-valid-extra-signature": v("Valid: one of several signatures is by the trusted key.", extra, vectorCommit, 0, true, 3, ""),
		"03-cal2-below-required":   v("A valid CAL 2 certificate is refused when CAL 3 is required.", sign(cal2, vectorCommit, priv), vectorCommit, 3, false, 0, "below the required 3"),
		"04-cal2-accepted":         v("The same CAL 2 certificate is accepted when nothing higher is required.", sign(cal2, vectorCommit, priv), vectorCommit, 0, true, 2, ""),
		"05-failed-check":          v("A validly signed certificate whose check failed is refused.", sign(failed, vectorCommit, priv), vectorCommit, 0, false, 0, "failed"),
		"06-other-commit":          v("A certificate about another commit is refused.", valid, "1111111111111111111111111111111111111111", 0, false, 0, "is about commit"),
		"07-tampered-payload":      v("The payload was changed after signing.", tampered, vectorCommit, 0, false, 0, "signature"),
		"08-signed-by-another-key": v("Signed, but not by the trusted key.", sign(pred, vectorCommit, other), vectorCommit, 0, false, 0, "signature"),
		"09-unsigned":              v("No signature at all.", unsigned, vectorCommit, 0, false, 0, "signature"),
		"10-not-in-toto":           v("The envelope's payload type is not in-toto.", otherType, vectorCommit, 0, false, 0, "payload type"),
		"11-other-predicate-type":  v("A signed in-toto statement, but not a change certificate.", wrongPredicate, vectorCommit, 0, false, 0, "not a change certificate"),
	}
}

// TestConformanceVectors keeps docs/spec/vectors equal to what this package
// produces, and checks that it decides each one as the vector says.
// UPDATE_DOCS=1 rewrites the files.
func TestConformanceVectors(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "docs", "spec", "vectors")
	for name, v := range vectors(t) {
		t.Run(name, func(t *testing.T) {
			want, err := json.MarshalIndent(v, "", "  ")
			require.NoError(t, err)
			want = append(want, '\n')
			path := filepath.Join(dir, name+".json")
			if os.Getenv("UPDATE_DOCS") != "" {
				require.NoError(t, os.MkdirAll(dir, 0o755))
				require.NoError(t, os.WriteFile(path, want, 0o644))
			}
			got, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, string(want), string(got), "%s is out of date: UPDATE_DOCS=1 go test ./src/internal/certificate", path)

			pub, err := base64.StdEncoding.DecodeString(v.PublicKey)
			require.NoError(t, err)
			s, err := Open(v.Envelope, pub)
			if err == nil {
				err = s.Accept(v.Commit, s.Predicate.CAL, v.MinCAL)
			}
			if v.Valid {
				require.NoError(t, err)
				assert.Equal(t, v.CAL, s.Predicate.CAL)
			} else {
				require.Error(t, err)
				assert.True(t, strings.Contains(err.Error(), v.Error), "%q does not say %q", err, v.Error)
			}
		})
	}
}
