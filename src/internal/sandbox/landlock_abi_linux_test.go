package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLandlock_needs_the_truncation_right: Landlock before ABI 3 (kernel 6.2)
// cannot stop truncate(2) of a file outside the writable folders, so a command
// sandboxed by it could still empty the user's files. Such a kernel counts as
// having no Landlock sandbox: "required" refuses the command and "auto" says
// it ran without one (F103).
func TestLandlock_needs_the_truncation_right(t *testing.T) {
	t.Cleanup(func() { landlockVersion = landlockABI })
	for _, abi := range []int{0, 1, 2} {
		landlockVersion = func() int { return abi }
		_, err := landlockWrapper("/root", "/tmp/x", nil)
		require.Error(t, err, "ABI %d", abi)
		if abi > 0 {
			assert.ErrorContains(t, err, "truncat", "ABI %d", abi)
		}
	}
	landlockVersion = func() int { return 3 }
	w, err := landlockWrapper("/root", "/tmp/x", nil)
	require.NoError(t, err)
	assert.Equal(t, landlockArg, w[1])
}
