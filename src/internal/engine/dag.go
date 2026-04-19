package engine

import (
	"fmt"

	"github.com/b070nd/staircase-core/src/internal/domain"
)

// TopoSort returns projects in dependency order (dependencies before dependents)
// using Kahn's algorithm. Hard-fails with a descriptive error on cycles.
//
// Edge semantics: ProjectDependency{Source, Target} means "Source depends on Target",
// so Target must appear before Source in the output order.
func TopoSort(projects []domain.Project, deps []domain.ProjectDependency) ([]domain.Project, error) {
	byID := make(map[int64]domain.Project, len(projects))
	inDegree := make(map[int64]int, len(projects))
	for _, p := range projects {
		byID[p.ID] = p
		inDegree[p.ID] = 0
	}

	// adjacency[target] → list of sources that depend on it.
	// When target is processed, each source has its in-degree decremented.
	adjacency := make(map[int64][]int64)
	for _, dep := range deps {
		if _, ok := inDegree[dep.SourceProjectID]; !ok {
			continue // source outside the current set — ignore
		}
		if _, ok := byID[dep.TargetProjectID]; !ok {
			continue // target outside the current set — ignore external dep
		}
		inDegree[dep.SourceProjectID]++
		adjacency[dep.TargetProjectID] = append(adjacency[dep.TargetProjectID], dep.SourceProjectID)
	}

	// Seed the queue with zero-dependency projects (stable order by ID).
	queue := make([]int64, 0, len(projects))
	for _, p := range projects {
		if inDegree[p.ID] == 0 {
			queue = append(queue, p.ID)
		}
	}

	sorted := make([]domain.Project, 0, len(projects))
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		sorted = append(sorted, byID[id])

		for _, dependentID := range adjacency[id] {
			inDegree[dependentID]--
			if inDegree[dependentID] == 0 {
				queue = append(queue, dependentID)
			}
		}
	}

	if len(sorted) != len(projects) {
		// Find which projects are still stuck (part of a cycle) for diagnostics.
		var cycled []string
		for id, deg := range inDegree {
			if deg > 0 {
				if p, ok := byID[id]; ok {
					cycled = append(cycled, fmt.Sprintf("%q (id=%d)", p.Name, p.ID))
				}
			}
		}
		return nil, fmt.Errorf("cycle detected in project dependency graph — involved projects: %v", cycled)
	}

	return sorted, nil
}
