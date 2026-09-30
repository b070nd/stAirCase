package orchestrator

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/sshsig"
)

// decisionNamespace is the SSH signature namespace of a signed decision.
const decisionNamespace = "staircase-decision"

// decisionSigning checks the SSH signatures people put on their decisions,
// and signs them with the operator's own key when asked to (--sign-approvals).
// A signature covers the run, a fresh nonce for this one proposal, the exact
// request the person saw and the decision (approve or reject), so it cannot be
// moved to another decision, nor replayed on a later identical proposal.
type decisionSigning struct {
	runID   int64
	require bool   // an unsigned or untrusted human decision is refused
	signKey string // sign each human decision with this key
	signAs  string // the principal it is signed as
	signers string // allowed_signers file: who counts as a trusted person
}

// payload is what is signed, up to the decision: "approve" or "reject" follows.
// nonce is random for each proposal that is put to a person.
func (g *decisionSigning) payload(req domain.YieldRequest, nonce string) string {
	b, _ := json.Marshal(req)
	return fmt.Sprintf("staircase-decision-v1\nrun=%d\nnonce=%s\nrequest=%s\ndecision=", g.runID, nonce, sha256Hex(b))
}

// ask puts req to a person through put, which is given the text to sign, and
// settles the answer with check. Every call uses a fresh nonce.
func (g *decisionSigning) ask(req domain.YieldRequest, put func(domain.YieldRequest, string) domain.YieldResponse) (domain.YieldResponse, map[string]any) {
	if g == nil { // nothing signs or checks: the answer stands as given
		return put(req, ""), nil
	}
	prefix := g.payload(req, rand.Text())
	return g.check(prefix, put(req, prefix))
}

// check settles a human decision made on the text prefix+verb: an invalid
// signature never approves anything, and with require an unsigned or untrusted
// one does not either. It returns the decision and what to record on the audit
// chain about the signature.
func (g *decisionSigning) check(prefix string, resp domain.YieldResponse) (domain.YieldResponse, map[string]any) {
	refuse := func(why string) (domain.YieldResponse, map[string]any) {
		return domain.Decide(false, why), map[string]any{"signature_refused": why}
	}
	verb := "reject"
	if resp.Approved {
		verb = "approve"
	}
	text := prefix + verb
	if g.signKey != "" && resp.Signature == "" {
		sig, err := sshsig.Sign(g.signKey, decisionNamespace, []byte(text))
		if err != nil {
			return refuse("the decision could not be signed: " + err.Error())
		}
		resp.Signer, resp.Signature = g.signAs, base64.StdEncoding.EncodeToString(sig)
	}
	if resp.Signature == "" {
		if g.require {
			return refuse("a signed decision is required: sign the decision_payload of the request with your SSH key")
		}
		return resp, nil
	}
	sig, err := base64.StdEncoding.DecodeString(resp.Signature)
	if err != nil {
		return refuse("the decision's signature is not valid base64")
	}
	fp, err := sshsig.Check(decisionNamespace, []byte(text), sig)
	if err != nil {
		return refuse("the decision's signature is not valid: " + err.Error())
	}
	trusted := g.signers != "" && resp.Signer != "" &&
		sshsig.Verify(g.signers, resp.Signer, decisionNamespace, []byte(text), sig) == nil
	if g.require && !trusted {
		return refuse(fmt.Sprintf("%q is not a trusted signer: the key %s is not listed for them in allowed_signers", resp.Signer, fp))
	}
	return resp, map[string]any{"signed_by": resp.Signer, "key": fp, "trusted": trusted, "signed_text": text, "signature": resp.Signature}
}
