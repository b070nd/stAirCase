package orchestrator

import (
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
// A signature covers the run, the exact request the person saw and the
// decision (approve or reject), so it cannot be moved to another decision.
type decisionSigning struct {
	runID   int64
	require bool   // an unsigned or untrusted human decision is refused
	signKey string // sign each human decision with this key
	signAs  string // the principal it is signed as
	signers string // allowed_signers file: who counts as a trusted person
}

// payload is what is signed, up to the decision: "approve" or "reject" follows.
func (g *decisionSigning) payload(req domain.YieldRequest) string {
	b, _ := json.Marshal(req)
	return fmt.Sprintf("staircase-decision-v1\nrun=%d\nrequest=%s\ndecision=", g.runID, sha256Hex(b))
}

// check settles a human decision: an invalid signature never approves
// anything, and with require an unsigned or untrusted one does not either. It
// returns the decision and what to record on the audit chain about the signature.
func (g *decisionSigning) check(req domain.YieldRequest, resp domain.YieldResponse) (domain.YieldResponse, map[string]any) {
	refuse := func(why string) (domain.YieldResponse, map[string]any) {
		return domain.Decide(false, why), map[string]any{"signature_refused": why}
	}
	verb := "reject"
	if resp.Approved {
		verb = "approve"
	}
	text := g.payload(req) + verb
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
