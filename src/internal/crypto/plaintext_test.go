package crypto_test

import (
	"fmt"
	"testing"

	"github.com/b070nd/staircase-core/src/internal/crypto"
	"github.com/stretchr/testify/assert"
)

// TestScrubBytes verifies that ScrubBytes replaces each secret occurrence with
// "<REDACTED>" and leaves unrelated content untouched (CHECK 4.4.3 / 7.4.2).
func TestScrubBytes(t *testing.T) {
	payload := []byte(`{"type":"secret_request","value":"my-secret","tag":"safe"}`)

	// Single secret replaced.
	out := crypto.ScrubBytes(payload, []string{"my-secret"})
	assert.NotContains(t, string(out), "my-secret")
	assert.Contains(t, string(out), "<REDACTED>")
	assert.Contains(t, string(out), "safe", "unrelated content must be preserved")

	// nil secrets: data returned unchanged.
	unchanged := crypto.ScrubBytes(payload, nil)
	assert.Equal(t, payload, unchanged)

	// Empty secret string is skipped (no replacement of empty matches).
	noChange := crypto.ScrubBytes([]byte("hello"), []string{""})
	assert.Equal(t, []byte("hello"), noChange)

	// Multiple secrets each replaced.
	multi := crypto.ScrubBytes([]byte("a=foo b=bar c=baz"), []string{"foo", "bar"})
	assert.NotContains(t, string(multi), "foo")
	assert.NotContains(t, string(multi), "bar")
	assert.Contains(t, string(multi), "baz")
}

// TestPlaintextRedaction verifies that fmt.Sprintf("%v", pt) returns
// "<redacted>" and never the underlying value (CHECK 4.2.2).
func TestPlaintextRedaction(t *testing.T) {
	pt := crypto.NewPlaintext("super-secret-value")

	// String() and %v must return the redacted sentinel.
	assert.Equal(t, "<redacted>", pt.String())
	assert.Equal(t, "<redacted>", fmt.Sprintf("%v", pt))
	assert.Equal(t, "<redacted>", fmt.Sprintf("%s", pt))

	// Value() still returns the underlying string.
	assert.Equal(t, "super-secret-value", pt.Value())

	// After Zero(), Value() returns empty string.
	pt.Zero()
	assert.Equal(t, "", pt.Value())

	// Zero on an already-zeroed Plaintext is a no-op (no panic).
	pt.Zero()
	assert.Equal(t, "", pt.Value())
}
