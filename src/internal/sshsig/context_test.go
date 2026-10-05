//go:build !windows

package sshsig_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/b070nd/stAirCase/src/internal/sshsig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSignContext_does_not_outlive_its_context: an ssh-keygen that waits (for a
// passphrase, a touch, an agent) is killed when the run's context ends; a run
// cannot be held past its limit by the signing of one decision.
func TestSignContext_does_not_outlive_its_context(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("needs a POSIX shell")
	}
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ssh-keygen"), []byte("#!/bin/sh\necho $$ > "+pidFile+"\nexec sleep 60\n"), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { // the run's limit passes once ssh-keygen is up and waiting
		for i := 0; i < 600; i++ {
			if b, err := os.ReadFile(pidFile); err == nil && strings.Contains(string(b), "\n") {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	start := time.Now()
	_, err := sshsig.SignContext(ctx, "key", "ns", []byte("payload"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cut short")
	assert.Less(t, time.Since(start), 10*time.Second)

	b, rerr := os.ReadFile(pidFile)
	require.NoError(t, rerr)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	require.NotZero(t, pid)
	assert.Eventually(t, func() bool { return syscall.Kill(pid, 0) != nil }, 3*time.Second, 20*time.Millisecond, "ssh-keygen was left running")
}
