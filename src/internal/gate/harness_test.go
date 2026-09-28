package gate_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/gate"
	"github.com/b070nd/stAirCase/src/internal/plan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunAll_harness_case_needs_no_topology_or_key: a case compiled for an
// agent harness (Claude Code) brings its own model and login, so the gates
// neither ask for a topology nor for a provider key.
func TestRunAll_harness_case_needs_no_topology_or_key(t *testing.T) {
	ctx, wsDir := newGateEnv(t)
	_, caseID := makeCase(t, ctx.Store)
	_, err := ctx.Store.CreateUserStory(caseID, "add a health endpoint")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(wsDir, "tmp"), 0o700))
	require.NoError(t, plan.Write(filepath.Join(wsDir, "tmp", fmt.Sprintf("plan_case%d.json", caseID)),
		plan.Plan{CaseID: caseID, Harness: "claude-code", PRD: "add a health endpoint"}))
	ctx.CaseID = caseID

	report := gate.RunAll(ctx)
	byName := map[string]gate.Result{}
	for _, r := range report.Gates {
		byName[r.Name] = r
		if strings.HasPrefix(r.Name, "topology.") {
			assert.Equal(t, gate.StatusSkip, r.Status, "%s: %s", r.Name, r.Message)
		}
	}
	keys := byName["secret.provider_keys"]
	assert.Equal(t, gate.StatusPass, keys.Status, keys.Message)
	assert.Contains(t, keys.Message, "claude-code")
	compiled := byName["runtime.plan_compiled"]
	assert.Equal(t, gate.StatusPass, compiled.Status, compiled.Message)
	assert.Contains(t, compiled.Message, "claude-code")
}
