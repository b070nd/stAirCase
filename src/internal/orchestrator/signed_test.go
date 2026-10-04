package orchestrator_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/plan"
	"github.com/b070nd/stAirCase/src/internal/sshsig"
	"github.com/b070nd/stAirCase/src/internal/testdeps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sshKey(t *testing.T, principal string) (key, pub string) {
	testdeps.Need(t, "ssh-keygen")
	key = filepath.Join(t.TempDir(), "id")
	out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", principal, "-f", key).CombinedOutput()
	require.NoError(t, err, string(out))
	b, err := os.ReadFile(key + ".pub")
	require.NoError(t, err)
	return key, principal + " " + string(b)
}

func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

// remoteDecider answers every pending proposal through the approval API, as
// a teammate's script would: it reads the decision_payload, signs what sign
// returns and posts the decision. approve is what it decides; signAs is the
// verb it puts in the signed text (a signature made for another decision).
type remoteDecider struct {
	port    int
	approve bool
	sign    func(text string) (signer, sig string) // nil: unsigned
	signAs  string
	replay  bool // sign the first proposal, then send that same signature for every later one
}

func (d remoteDecider) run(ctx context.Context, wg *sync.WaitGroup, seen *[]string, mu *sync.Mutex) {
	defer wg.Done()
	base := fmt.Sprintf("http://127.0.0.1:%d/v1/yields", d.port)
	call := func(method, url string, body []byte) []byte {
		req, _ := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer tok")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return b
	}
	var first []string // signer and signature of the first decision, for replay
	for ctx.Err() == nil {
		var ys []struct {
			ID      string `json:"id"`
			Payload string `json:"decision_payload"`
		}
		_ = json.Unmarshal(call(http.MethodGet, base, nil), &ys)
		for _, y := range ys {
			verb := "reject"
			if d.approve {
				verb = "approve"
			}
			body := map[string]string{"feedback": "by script"}
			if d.sign != nil {
				as := d.signAs
				if as == "" {
					as = verb
				}
				body["signer"], body["signature"] = d.sign(y.Payload + as)
				if d.replay && first != nil {
					body["signer"], body["signature"] = first[0], first[1]
				} else if d.replay {
					first = []string{body["signer"], body["signature"]}
				}
			}
			b, _ := json.Marshal(body)
			call(http.MethodPost, base+"/"+y.ID+"/"+verb, b)
			mu.Lock()
			*seen = append(*seen, y.Payload)
			mu.Unlock()
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// signedRun runs an agent that proposes one file, decided by d over the
// approval API, and returns the run, the recorded decisions and the
// certificate's view of them.
func signedRun(t *testing.T, opts orchestrator.RunOptions, signers string, d remoteDecider) (runtest.Result, []map[string]any, certificate.Predicate, []string) {
	return signedRunWith(t, func(ctx context.Context, env *orchestrator.AgentEnv) error {
		create(ctx, env, "GREETING.md")
		return nil
	}, opts, signers, d)
}

// signedRunWith is signedRun for an agent of your own.
func signedRunWith(t *testing.T, agent orchestrator.AgentFunc, opts orchestrator.RunOptions, signers string, d remoteDecider) (runtest.Result, []map[string]any, certificate.Predicate, []string) {
	d.port = freePort(t)
	opts.ApprovalPort, opts.ApprovalToken = d.port, "tok"
	var wg sync.WaitGroup
	var mu sync.Mutex
	var seen []string
	ctx, cancel := context.WithCancel(context.Background())
	wg.Add(1)
	go d.run(ctx, &wg, &seen, &mu)
	r := runtest.Run(t, runtest.Options{
		Setup: func(_ *persistence.Store, ws string, _ int64) {
			require.NoError(t, crypto.GenerateSigningKey(ws))
			require.NoError(t, os.WriteFile(filepath.Join(ws, "policy.json"), []byte(`{"rules":[]}`), 0o600))
			if signers != "" {
				require.NoError(t, os.WriteFile(filepath.Join(ws, "allowed_signers"), []byte(signers), 0o644))
			}
		},
		Run:   opts,
		Agent: agent})
	cancel()
	wg.Wait()
	var decided []map[string]any
	for _, e := range r.Events {
		if e.EventType == "yield_decided" {
			var m map[string]any
			require.NoError(t, json.Unmarshal([]byte(e.Payload), &m))
			decided = append(decided, m)
		}
	}
	var p certificate.Predicate
	if b, err := os.ReadFile(filepath.Join(r.WsDir, "audit", "run-1.certificate.json")); err == nil {
		var env certificate.Envelope
		require.NoError(t, json.Unmarshal(b, &env))
		pub, _ := crypto.LoadSigningPublicKey(r.WsDir)
		s, err := certificate.Open(env, pub)
		require.NoError(t, err)
		p = s.Predicate
	}
	mu.Lock()
	defer mu.Unlock()
	return r, decided, p, seen
}

func signer(t *testing.T, key, principal string) func(string) (string, string) {
	return func(text string) (string, string) {
		sig, err := sshsig.Sign(key, "staircase-decision", []byte(text))
		require.NoError(t, err)
		return principal, base64.StdEncoding.EncodeToString(sig)
	}
}

// TestSignedApprovals: a decision signed by a trusted person is recorded
// with who and which key, and counted in the certificate; a signature that
// does not fit the decision (made for another verb or by an untrusted key)
// never approves anything; with --require-signed-approvals an unsigned or
// untrusted decision is refused; and stAirCase can sign the decision itself.
func TestSignedApprovals(t *testing.T) {
	aliceKey, aliceLine := sshKey(t, "alice@example.com")
	malloryKey, _ := sshKey(t, "mallory@example.com")

	t.Run("a trusted signer", func(t *testing.T) {
		r, decided, p, seen := signedRun(t, orchestrator.RunOptions{}, aliceLine,
			remoteDecider{approve: true, sign: signer(t, aliceKey, "alice@example.com")})
		require.NoError(t, r.Err)
		require.NotEmpty(t, seen)
		assert.True(t, strings.HasPrefix(seen[0], "staircase-decision-v1\nrun=1\nnonce="), seen[0])
		first := decided[0]
		assert.Equal(t, true, first["approved"])
		assert.Equal(t, "alice@example.com", first["signed_by"])
		assert.Equal(t, true, first["trusted"])
		assert.Regexp(t, `^SHA256:`, first["key"])
		require.NotNil(t, p.Signed, "the certificate counts signed decisions")
		assert.Equal(t, 1, p.Signed.Decisions)
		assert.Equal(t, []string{"alice@example.com"}, p.Signed.Signers)
	})

	t.Run("a signature made for the other decision approves nothing", func(t *testing.T) {
		r, decided, _, _ := signedRun(t, orchestrator.RunOptions{}, aliceLine,
			remoteDecider{approve: true, signAs: "reject", sign: signer(t, aliceKey, "alice@example.com")})
		require.NoError(t, r.Err)
		assert.Empty(t, r.Run.GitCommitHash, "nothing was approved")
		assert.Equal(t, false, decided[0]["approved"])
		assert.Contains(t, decided[0]["feedback"], "signature")
	})

	t.Run("and the other way round", func(t *testing.T) {
		r, decided, p, _ := signedRun(t, orchestrator.RunOptions{}, aliceLine,
			remoteDecider{approve: false, signAs: "approve", sign: signer(t, aliceKey, "alice@example.com")})
		require.NoError(t, r.Err)
		assert.Contains(t, decided[0], "signature_refused", "an approval's signature is not a rejection's")
		assert.NotContains(t, decided[0], "signed_by")
		assert.Nil(t, p.Signed)
	})

	t.Run("requiring signatures needs to know the trusted signers", func(t *testing.T) {
		r, _, _, _ := signedRun(t, orchestrator.RunOptions{RequireSignedApprovals: true}, "", remoteDecider{approve: true})
		require.Error(t, r.Err)
		assert.Contains(t, r.Err.Error(), "allowed_signers")
	})

	t.Run("an untrusted key is recorded as such, and refused when signatures are required", func(t *testing.T) {
		r, decided, p, _ := signedRun(t, orchestrator.RunOptions{}, aliceLine,
			remoteDecider{approve: true, sign: signer(t, malloryKey, "alice@example.com")}) // claims to be alice
		require.NoError(t, r.Err)
		assert.Equal(t, false, decided[0]["trusted"])
		assert.Equal(t, 1, p.Signed.Decisions)
		assert.Empty(t, p.Signed.Signers, "an untrusted key names no one")

		r, decided, _, _ = signedRun(t, orchestrator.RunOptions{RequireSignedApprovals: true}, aliceLine,
			remoteDecider{approve: true, sign: signer(t, malloryKey, "alice@example.com")})
		require.NoError(t, r.Err)
		assert.Empty(t, r.Run.GitCommitHash, "nothing was approved")
		assert.Equal(t, false, decided[0]["approved"])
		assert.Contains(t, decided[0]["feedback"], "trusted")
	})

	t.Run("unsigned decisions", func(t *testing.T) {
		r, decided, p, _ := signedRun(t, orchestrator.RunOptions{}, aliceLine, remoteDecider{approve: true})
		require.NoError(t, r.Err, "signatures are optional by default")
		assert.NotContains(t, decided[0], "signed_by")
		assert.Nil(t, p.Signed)

		r, decided, _, _ = signedRun(t, orchestrator.RunOptions{RequireSignedApprovals: true}, aliceLine, remoteDecider{approve: true})
		require.NoError(t, r.Err)
		assert.Empty(t, r.Run.GitCommitHash, "nothing was approved")
		assert.Equal(t, false, decided[0]["approved"])
		assert.Contains(t, decided[0]["feedback"], "signed")
	})

	t.Run("stAirCase signs the decision with your key", func(t *testing.T) {
		r, decided, p, _ := signedRun(t, orchestrator.RunOptions{SignKey: aliceKey, SignAs: "alice@example.com", RequireSignedApprovals: true},
			aliceLine, remoteDecider{approve: true})
		require.NoError(t, r.Err)
		assert.Equal(t, "alice@example.com", decided[0]["signed_by"])
		assert.Equal(t, true, decided[0]["trusted"])
		assert.Equal(t, []string{"alice@example.com"}, p.Signed.Signers)
	})
}

// TestSignedApprovals_a_signature_is_good_for_one_proposal: the signed text
// carries a fresh nonce, so a signature captured for one proposal does not
// decide another, not even one with an identical request (F97).
func TestSignedApprovals_a_signature_is_good_for_one_proposal(t *testing.T) {
	aliceKey, aliceLine := sshKey(t, "alice@example.com")
	twice := func(ctx context.Context, env *orchestrator.AgentEnv) error {
		create(ctx, env, "GREETING.md") // rejected ...
		create(ctx, env, "GREETING.md") // ... and proposed again, byte for byte
		return nil
	}
	_, decided, _, seen := signedRunWith(t, twice, orchestrator.RunOptions{RequireSignedApprovals: true}, aliceLine,
		remoteDecider{approve: false, replay: true, sign: signer(t, aliceKey, "alice@example.com")})
	require.GreaterOrEqual(t, len(decided), 2)
	require.GreaterOrEqual(t, len(seen), 2)
	assert.NotEqual(t, seen[0], seen[1], "each proposal is offered with its own text to sign")
	assert.Contains(t, decided[0], "signed_by", "the honest signature counts")
	assert.Contains(t, decided[1], "signature_refused", "the replayed one does not")
	assert.NotContains(t, decided[1], "signed_by")
}

// TestRun_the_initiator_signs_the_request: with --sign-approvals the person who
// starts a run signs who they are, the run, its base commit and its plan; the
// certificate carries that signature, and a key that cannot sign stops the run
// before the agent does anything (F96).
func TestRun_the_initiator_signs_the_request(t *testing.T) {
	aliceKey, _ := sshKey(t, "alice@example.com")
	plan := &plan.Plan{Harness: "claude-code", Digest: "p1an"}
	r, commit, s := certified(t, writeFile("health.txt", "ok\n"),
		orchestrator.RunOptions{Plan: plan, SignKey: aliceKey, SignAs: "alice@example.com"}, nil)
	require.NotNil(t, s.Predicate.Initiator, "the certificate names its initiator")
	assert.Equal(t, "alice@example.com", s.Predicate.Initiator.Principal)
	sig, err := base64.StdEncoding.DecodeString(s.Predicate.Initiator.Signature)
	require.NoError(t, err)
	fp, err := sshsig.Check(certificate.InitiatorNamespace, certificate.InitiatorText(s.Predicate, "alice@example.com"), sig)
	require.NoError(t, err, "the signature is over the text the verifier rebuilds")
	assert.Regexp(t, `^SHA256:`, fp)
	assert.Contains(t, r.Types(), "initiator_signed")
	_ = commit

	// No key, no initiator: the git email stays an unauthenticated claim.
	_, _, s = certified(t, writeFile("health.txt", "ok\n"), orchestrator.RunOptions{Plan: plan}, nil)
	assert.Nil(t, s.Predicate.Initiator)

	// A key that cannot sign: the run stops before the agent runs.
	ran := false
	agent := orchestrator.AgentFunc(func(context.Context, *orchestrator.AgentEnv) error { ran = true; return nil })
	res := runtest.Run(t, runtest.Options{Agent: agent, Run: orchestrator.RunOptions{Plan: plan, SignKey: filepath.Join(t.TempDir(), "missing"), SignAs: "alice@example.com"}})
	require.Error(t, res.Err)
	assert.Contains(t, res.Err.Error(), "initiator")
	assert.False(t, ran, "the agent never ran")
}
