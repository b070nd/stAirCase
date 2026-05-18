package main

import (
	"testing"

	"github.com/b070nd/staircase-core/src/internal/domain"
	"github.com/b070nd/staircase-core/src/internal/gate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseID_valid_integer(t *testing.T) {
	id, err := parseID("case", "42")
	require.NoError(t, err)
	assert.Equal(t, int64(42), id)
}

func TestParseID_invalid_string_returns_error(t *testing.T) {
	_, err := parseID("case", "abc")
	assert.ErrorContains(t, err, "invalid case")
	assert.ErrorContains(t, err, "abc")
}

func TestTrimNL_strips_trailing_newline(t *testing.T) {
	assert.Equal(t, "hello", trimNL([]byte("hello\n")))
}

func TestTrimNL_strips_crlf(t *testing.T) {
	assert.Equal(t, "hello", trimNL([]byte("hello\r\n")))
}

func TestTrimNL_no_newline_unchanged(t *testing.T) {
	assert.Equal(t, "hello", trimNL([]byte("hello")))
}

func TestTrimNL_empty_input(t *testing.T) {
	assert.Equal(t, "", trimNL([]byte{}))
}

func TestFilterSubgraph_returns_root_and_reachable_deps(t *testing.T) {
	projects := []domain.Project{{ID: 1}, {ID: 2}, {ID: 3}}
	deps := []domain.ProjectDependency{
		{SourceProjectID: 1, TargetProjectID: 2},
	}
	ps, ds := filterSubgraph(1, projects, deps)
	require.Len(t, ps, 2)
	require.Len(t, ds, 1)
	assert.Equal(t, int64(3), projects[2].ID) // project 3 unreachable — not in result
}

func TestFilterSubgraph_single_node_no_deps(t *testing.T) {
	ps, ds := filterSubgraph(7, []domain.Project{{ID: 7}}, nil)
	assert.Len(t, ps, 1)
	assert.Empty(t, ds)
}

func TestStatusIcon_all_branches(t *testing.T) {
	assert.Contains(t, statusIcon(gate.StatusPass), "✅")
	assert.Contains(t, statusIcon(gate.StatusWarn), "⚠")
	assert.Contains(t, statusIcon(gate.StatusFail), "❌")
	assert.NotEmpty(t, statusIcon(gate.Status("unknown"))) // default branch
}
