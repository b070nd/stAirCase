package orchestrator_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewTerms_normalizes_once: away is approving on evidence, which is approving the agreed task; nothing else changes.
func TestNewTerms_normalizes_once(t *testing.T) {
	agreed, checks := "dev@example.com", []string{"go test ./..."}
	for name, tc := range map[string]struct {
		in, want orchestrator.Terms
	}{
		"away": {orchestrator.Terms{Agreed: agreed, Away: true, Checks: checks},
			orchestrator.Terms{Agreed: agreed, Away: true, ApproveOnEvidence: true, ApproveInScope: true, Checks: checks}},
		"on evidence": {orchestrator.Terms{Agreed: agreed, ApproveOnEvidence: true, ValidatorModels: []string{"m"}},
			orchestrator.Terms{Agreed: agreed, ApproveOnEvidence: true, ApproveInScope: true, ValidatorModels: []string{"m"}}},
		"in scope":         {orchestrator.Terms{Agreed: agreed, ApproveInScope: true}, orchestrator.Terms{Agreed: agreed, ApproveInScope: true}},
		"a person decides": {orchestrator.Terms{}, orchestrator.Terms{}},
		"a docker sandbox": {orchestrator.Terms{Sandbox: "docker:image=golang:1.26"}, orchestrator.Terms{Sandbox: "docker:image=golang:1.26"}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := orchestrator.NewTerms(tc.in, "")
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			again, err := orchestrator.NewTerms(got, "")
			require.NoError(t, err)
			assert.Equal(t, got, again, "normalizing twice changes nothing")
		})
	}
}

// TestNewTerms_refuses_what_no_run_can_decide_under: every refusal that the terms alone decide, with what it names.
func TestNewTerms_refuses_what_no_run_can_decide_under(t *testing.T) {
	t.Cleanup(orchestrator.SetNoNetworkModeForTest(func(mode string) bool { return mode == sandbox.Required }))
	agreed, checks := "dev@example.com", []string{"go test ./..."}
	away := func(f func(*orchestrator.Terms)) orchestrator.Terms {
		tm := orchestrator.Terms{Agreed: agreed, Away: true, Checks: checks, AllowShellExec: true, Sandbox: sandbox.Required}
		f(&tm)
		return tm
	}
	for name, tc := range map[string]struct {
		terms   orchestrator.Terms
		harness string
		why     string
	}{
		"away without a check":          {away(func(t *orchestrator.Terms) { t.Checks = nil }), "", "--check"},
		"away with signed decisions":    {away(func(t *orchestrator.Terms) { t.RequireSignedApprovals = true }), "", "signed"},
		"on evidence without evidence":  {orchestrator.Terms{Agreed: agreed, ApproveOnEvidence: true}, "", "evidence to decide on"},
		"in scope with no agreed task":  {orchestrator.Terms{ApproveInScope: true}, "", "nobody agreed"},
		"on evidence, no agreed task":   {orchestrator.Terms{ApproveOnEvidence: true, Checks: checks}, "", "nobody agreed"},
		"an unknown sandbox":            {orchestrator.Terms{Sandbox: "vm"}, "", "--sandbox"},
		"a tool without away":           {orchestrator.Terms{AwayTools: []string{"mcp__a__b"}}, "", "add --away"},
		"a tool by pattern":             {away(func(t *orchestrator.Terms) { t.AwayTools = []string{"mcp__a__*"} }), "", "exactly"},
		"a command without away":        {orchestrator.Terms{AwayCommands: []string{"make"}, AllowShellExec: true, Sandbox: sandbox.Required}, "", "add --away"},
		"a command, commands off":       {away(func(t *orchestrator.Terms) { t.AwayCommands, t.AllowShellExec = []string{"make"}, false }), "", "--allow-shell-exec"},
		"a command, network":            {away(func(t *orchestrator.Terms) { t.AwayCommands, t.Sandbox = []string{"make"}, sandbox.Auto }), "", "without network"},
		"a command, unsandboxed agent":  {away(func(t *orchestrator.Terms) { t.AwayCommands = []string{"make"} }), "gemini", "gemini"},
		"a composed command":            {away(func(t *orchestrator.Terms) { t.AwayCommands = []string{"make && make install"} }), "", "whole command"},
		"a command with a second line":  {away(func(t *orchestrator.Terms) { t.AwayCommands = []string{"make\nrm -rf x"} }), "", "whole command"},
		"a command with outer space":    {away(func(t *orchestrator.Terms) { t.AwayCommands = []string{" make"} }), "", "whole command"},
		"a declared command is allowed": {away(func(t *orchestrator.Terms) { t.AwayCommands = []string{"make"} }), "claude-code", ""},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := orchestrator.NewTerms(tc.terms, tc.harness)
			if tc.why == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.why)
		})
	}
}

// TestTerms_refused_before_the_run_exists (Q4): terms no run can decide under are refused before anything is made: no run
// record, no run branch, no worktree.
func TestTerms_refused_before_the_run_exists(t *testing.T) {
	r := runtest.Run(t, runtest.Options{
		Setup: inSrc(&operator{approve: true}),
		Run:   orchestrator.RunOptions{Terms: orchestrator.Terms{Away: true, Agreed: "dev@example.com"}}, // away, but no check to approve on
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error { return nil })})
	require.ErrorContains(t, r.Err, "--check")
	var runs int
	require.NoError(t, r.DB.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&runs))
	assert.Zero(t, runs, "no run record")
	assert.Equal(t, "main", strings.TrimSpace(git(t, r.Repo, "branch", "--format=%(refname:short)")), "no run branch")
	_, err := os.Stat(filepath.Join(r.WsDir, "worktrees"))
	assert.True(t, os.IsNotExist(err), "no worktree")
}
