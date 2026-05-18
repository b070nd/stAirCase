package crypto_test

import (
	"fmt"
	"testing"

	"github.com/b070nd/staircase-core/src/internal/crypto"
	"github.com/stretchr/testify/assert"
)

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
