// Package webhookauth provides HMAC-SHA256 request/response authentication for
// the webhook approval channel. Without it, anyone who can reach (or MITM) a
// project's webhook endpoint can forge a human approval and bypass HITL.
//
// Wire format (both directions):
//
//	X-Staircase-Timestamp: <unix seconds>
//	X-Staircase-Signature: sha256=<hex HMAC-SHA256(secret, timestamp + "." + body)>
//
// The orchestrator signs the outbound yield request and verifies the signature
// on the approver's response. A matching secret on both sides proves the
// response came from the configured approver and was not tampered with; the
// timestamp bounds replay.
package webhookauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// HeaderTimestamp carries the unix-seconds timestamp covered by the signature.
	HeaderTimestamp = "X-Staircase-Timestamp"
	// HeaderSignature carries "sha256=<hex>".
	HeaderSignature = "X-Staircase-Signature"
	// SecretKeyName is the reserved, project-scoped secret key under which the
	// per-project webhook HMAC secret is stored in the encrypted secret store.
	SecretKeyName = "__webhook_hmac_secret__"
	// DefaultMaxSkew is the maximum accepted clock skew / replay window.
	DefaultMaxSkew = 5 * time.Minute
)

// Sign returns the signature header value for body at the given timestamp.
func Sign(secret []byte, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Verify checks that sigHeader is a valid signature of body at timestampHeader,
// using a constant-time comparison, and that the timestamp is within maxSkew of
// now. Returns nil when valid, a descriptive error otherwise.
func Verify(secret []byte, timestampHeader, sigHeader string, body []byte, now time.Time, maxSkew time.Duration) error {
	if timestampHeader == "" || sigHeader == "" {
		return fmt.Errorf("missing %s/%s header", HeaderTimestamp, HeaderSignature)
	}
	ts, err := strconv.ParseInt(timestampHeader, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid timestamp header: %w", err)
	}
	skew := now.Sub(time.Unix(ts, 0))
	if skew < 0 {
		skew = -skew
	}
	if skew > maxSkew {
		return fmt.Errorf("timestamp outside accepted window (skew %s > %s)", skew, maxSkew)
	}
	want := Sign(secret, timestampHeader, body)
	if !hmac.Equal([]byte(strings.TrimSpace(sigHeader)), []byte(want)) {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}
