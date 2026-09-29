package orchestrator_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRun_registers_its_approval_server_for_the_hub: a run with an approval
// port is listed in the workspace's sessions while it runs, with its own key,
// and unlisted when it ends.
func TestRun_registers_its_approval_server_for_the_hub(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	var during []byte
	var wsDir string
	r := runtest.Run(t, runtest.Options{
		Setup: func(_ *persistence.Store, ws string, _ int64) { wsDir = ws },
		Run:   orchestrator.RunOptions{ApprovalPort: port, ApprovalToken: "session-key"},
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			files, _ := filepath.Glob(filepath.Join(wsDir, "sessions", "*.json"))
			if len(files) == 1 {
				during, _ = os.ReadFile(files[0])
			}
			return nil
		})})
	require.NoError(t, r.Err)
	assert.Contains(t, string(during), `"token":"session-key"`)
	assert.Contains(t, string(during), "127.0.0.1:")
	files, _ := filepath.Glob(filepath.Join(wsDir, "sessions", "*.json"))
	assert.Empty(t, files)
}
