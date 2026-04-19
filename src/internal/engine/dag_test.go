package engine_test

import (
	"strings"
	"testing"

	"github.com/b070nd/staircase-core/src/internal/domain"
	"github.com/b070nd/staircase-core/src/internal/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

func proj(id int64, name string) domain.Project {
	return domain.Project{ID: id, Name: name}
}

func dep(src, tgt int64) domain.ProjectDependency {
	return domain.ProjectDependency{SourceProjectID: src, TargetProjectID: tgt}
}

// positions returns a map of project ID → index in the sorted slice.
func positions(sorted []domain.Project) map[int64]int {
	m := make(map[int64]int, len(sorted))
	for i, p := range sorted {
		m[p.ID] = i
	}
	return m
}

// ─── tests ────────────────────────────────────────────────────────────────────

func TestTopoSort_empty(t *testing.T) {
	sorted, err := engine.TopoSort(nil, nil)
	require.NoError(t, err)
	assert.Empty(t, sorted)
}

func TestTopoSort_single_no_deps(t *testing.T) {
	sorted, err := engine.TopoSort([]domain.Project{proj(1, "A")}, nil)
	require.NoError(t, err)
	require.Len(t, sorted, 1)
	assert.Equal(t, int64(1), sorted[0].ID)
}

func TestTopoSort_linear_chain(t *testing.T) {
	// A depends on B depends on C → C must come before B, B before A
	projects := []domain.Project{proj(1, "A"), proj(2, "B"), proj(3, "C")}
	deps := []domain.ProjectDependency{dep(1, 2), dep(2, 3)}

	sorted, err := engine.TopoSort(projects, deps)
	require.NoError(t, err)
	require.Len(t, sorted, 3)

	pos := positions(sorted)
	assert.Less(t, pos[3], pos[2], "C must come before B")
	assert.Less(t, pos[2], pos[1], "B must come before A")
}

func TestTopoSort_diamond(t *testing.T) {
	// D depends on B and C; B and C each depend on A → A first, D last
	projects := []domain.Project{proj(1, "A"), proj(2, "B"), proj(3, "C"), proj(4, "D")}
	deps := []domain.ProjectDependency{dep(2, 1), dep(3, 1), dep(4, 2), dep(4, 3)}

	sorted, err := engine.TopoSort(projects, deps)
	require.NoError(t, err)
	require.Len(t, sorted, 4)

	pos := positions(sorted)
	assert.Equal(t, 0, pos[1], "A must be first (no dependencies)")
	assert.Equal(t, 3, pos[4], "D must be last (depends on all others)")
}

func TestTopoSort_independent_projects_all_returned(t *testing.T) {
	projects := []domain.Project{proj(1, "A"), proj(2, "B"), proj(3, "C")}

	sorted, err := engine.TopoSort(projects, nil)
	require.NoError(t, err)
	assert.Len(t, sorted, 3)
}

func TestTopoSort_cycle_returns_error(t *testing.T) {
	projects := []domain.Project{proj(1, "A"), proj(2, "B")}
	deps := []domain.ProjectDependency{dep(1, 2), dep(2, 1)}

	_, err := engine.TopoSort(projects, deps)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle")
}

func TestTopoSort_self_loop_returns_error(t *testing.T) {
	projects := []domain.Project{proj(1, "A")}
	deps := []domain.ProjectDependency{dep(1, 1)}

	_, err := engine.TopoSort(projects, deps)
	require.Error(t, err)
}

func TestTopoSort_cycle_error_names_involved_projects(t *testing.T) {
	projects := []domain.Project{proj(1, "Alpha"), proj(2, "Beta")}
	deps := []domain.ProjectDependency{dep(1, 2), dep(2, 1)}

	_, err := engine.TopoSort(projects, deps)
	require.Error(t, err)
	// At least one of the project names should appear in the diagnostic message.
	hasName := strings.Contains(err.Error(), "Alpha") || strings.Contains(err.Error(), "Beta")
	assert.True(t, hasName, "error should name the projects involved in the cycle")
}

func TestTopoSort_dep_outside_set_is_silently_ignored(t *testing.T) {
	// Project 1 depends on project 99 which is not in the projects slice.
	// The dep should be ignored, not cause an error.
	projects := []domain.Project{proj(1, "A")}
	deps := []domain.ProjectDependency{dep(1, 99)}

	sorted, err := engine.TopoSort(projects, deps)
	require.NoError(t, err)
	assert.Len(t, sorted, 1)
}

func TestTopoSort_output_length_matches_input(t *testing.T) {
	projects := []domain.Project{proj(1, "A"), proj(2, "B"), proj(3, "C"), proj(4, "D")}
	deps := []domain.ProjectDependency{dep(2, 1), dep(3, 1)}

	sorted, err := engine.TopoSort(projects, deps)
	require.NoError(t, err)
	assert.Len(t, sorted, len(projects))
}

func TestTopoSort_three_node_cycle(t *testing.T) {
	// A → B → C → A
	projects := []domain.Project{proj(1, "A"), proj(2, "B"), proj(3, "C")}
	deps := []domain.ProjectDependency{dep(1, 2), dep(2, 3), dep(3, 1)}

	_, err := engine.TopoSort(projects, deps)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle")
}
