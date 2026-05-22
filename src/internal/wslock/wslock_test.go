//go:build !windows

package wslock_test

import (
	"os"
	"testing"

	"github.com/b070nd/staircase-core/src/internal/wslock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openTmpFile creates a temporary file and returns it open.  The caller must
// close it.  t.TempDir() handles removal.
func openTmpFile(t *testing.T) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "wslock-test-*")
	require.NoError(t, err)
	return f
}

// TestLock_exclusive_blocks_shared verifies that a process holding an
// exclusive lock prevents a second exclusive-or-shared acquisition on the
// same file (simulates: rotate holds exclusive, secret-set tries shared).
func TestLock_exclusive_blocks_shared(t *testing.T) {
	f1 := openTmpFile(t)
	defer func() { _ = f1.Close() }()

	f2, err := os.Open(f1.Name())
	require.NoError(t, err)
	defer func() { _ = f2.Close() }()

	// Acquire exclusive lock via f1.
	require.NoError(t, wslock.LockExclusive(f1.Fd()), "first exclusive lock must succeed")

	// Shared lock via a second fd on the same file must fail (LOCK_NB).
	err = wslock.LockShared(f2.Fd())
	assert.Error(t, err, "shared lock must be blocked while exclusive lock is held")

	// Exclusive lock via second fd must also fail.
	err = wslock.LockExclusive(f2.Fd())
	assert.Error(t, err, "second exclusive lock must be blocked while first is held")

	// Release and confirm shared lock now succeeds.
	require.NoError(t, wslock.Unlock(f1.Fd()))
	assert.NoError(t, wslock.LockShared(f2.Fd()), "shared lock must succeed after exclusive release")
}

// TestLock_shared_blocks_exclusive verifies that a process holding a shared
// lock prevents an exclusive acquisition on the same file (simulates:
// secret-set holds shared, rotate tries exclusive).
func TestLock_shared_blocks_exclusive(t *testing.T) {
	f1 := openTmpFile(t)
	defer func() { _ = f1.Close() }()

	f2, err := os.Open(f1.Name())
	require.NoError(t, err)
	defer func() { _ = f2.Close() }()

	// Acquire shared lock via f1.
	require.NoError(t, wslock.LockShared(f1.Fd()), "shared lock must succeed")

	// Exclusive lock via a second fd on the same file must fail (LOCK_NB).
	err = wslock.LockExclusive(f2.Fd())
	assert.Error(t, err, "exclusive lock must be blocked while shared lock is held")

	// Release and confirm exclusive lock now succeeds.
	require.NoError(t, wslock.Unlock(f1.Fd()))
	assert.NoError(t, wslock.LockExclusive(f2.Fd()), "exclusive lock must succeed after shared release")
}

// TestLock_multiple_shared_coexist verifies that two shared locks on the same
// file do not block each other (concurrent reads are safe).
func TestLock_multiple_shared_coexist(t *testing.T) {
	f1 := openTmpFile(t)
	defer func() { _ = f1.Close() }()

	f2, err := os.Open(f1.Name())
	require.NoError(t, err)
	defer func() { _ = f2.Close() }()

	require.NoError(t, wslock.LockShared(f1.Fd()), "first shared lock must succeed")
	assert.NoError(t, wslock.LockShared(f2.Fd()), "second shared lock must coexist with first")
}
