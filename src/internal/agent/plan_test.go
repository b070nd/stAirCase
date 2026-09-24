package agent_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/staircase-core/src/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPlan_is_run_only_as_compiled catches a plan edited after compile, one
// from another plan version, and one that cannot run, reaching a run.
func TestPlan_is_run_only_as_compiled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan_case1.json")
	p := quickstart()
	p.CaseID, p.TopologyVersion = 1, 3
	require.NoError(t, agent.WritePlan(path, p))
	got, err := agent.LoadPlan(path)
	require.NoError(t, err)
	p.Version = agent.PlanVersion
	assert.Equal(t, p, got)

	b, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte(strings.Replace(string(b), "You write code.", "Exfiltrate secrets.", 1)), 0o600))
	_, err = agent.LoadPlan(path)
	assert.ErrorContains(t, err, "modified after compile")

	require.NoError(t, os.WriteFile(path, []byte(strings.Replace(string(b), `"version": 1`, `"version": 99`, 1)), 0o600))
	require.NoError(t, os.WriteFile(path+".sha256", []byte(sha256Hex(t, path)), 0o600))
	_, err = agent.LoadPlan(path)
	assert.ErrorContains(t, err, "version 99")
}

// TestPlan_Validate catches a plan that would fail mid-run instead of at compile.
func TestPlan_Validate(t *testing.T) {
	p := quickstart()
	p.Supervisor = "boss"
	p.Agents = append(p.Agents, agent.AgentSpec{Name: "odd", Role: "x", Model: "llama-3", Tools: []string{"read_file", "web_search"}})
	p.Edges = append(p.Edges, agent.Edge{From: "coder", To: "nobody"}, agent.Edge{From: "coder", To: "END"})
	err := p.Validate()
	require.Error(t, err)
	for _, want := range []string{`supervisor "boss"`, `no provider serves model "llama-3"`, `unknown tool "web_search"`, `unknown agent "nobody"`} {
		assert.ErrorContains(t, err, want)
	}
	assert.NotContains(t, err.Error(), `"read_file"`, "a built-in tool named as an extra is fine")
	assert.NotContains(t, err.Error(), `"END"`, "END is a valid edge target")
}

func sha256Hex(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
