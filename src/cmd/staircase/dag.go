package main

import (
	"fmt"
	"strings"

	"github.com/b070nd/staircase-core/src/internal/domain"
	"github.com/spf13/cobra"
)

var dagCmd = &cobra.Command{
	Use:   "dag",
	Short: "Manage and visualise the project dependency DAG",
}

var dagVizCmd = &cobra.Command{
	Use:   "viz [project-id]",
	Short: "Output the project dependency graph in DOT format",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		allProjects, err := store.ListAllProjects()
		if err != nil {
			return fmt.Errorf("load projects: %w", err)
		}
		allDeps, err := store.ListAllProjectDependencies()
		if err != nil {
			return fmt.Errorf("load dependencies: %w", err)
		}

		if len(args) == 1 {
			rootID, err := parseID("project-id", args[0])
			if err != nil {
				return err
			}
			allProjects, allDeps = filterSubgraph(rootID, allProjects, allDeps)
		}

		names := make(map[int64]string, len(allProjects))
		for _, p := range allProjects {
			names[p.ID] = p.Name
		}

		var sb strings.Builder
		sb.WriteString("digraph staircase {\n")
		sb.WriteString("  rankdir=LR;\n")
		sb.WriteString("  node [shape=box, style=filled, fillcolor=lightblue];\n")
		for _, p := range allProjects {
			fmt.Fprintf(&sb, "  %q [label=%q];\n", p.Name, fmt.Sprintf("#%d %s", p.ID, p.Name))
		}
		for _, d := range allDeps {
			src, srcOK := names[d.SourceProjectID]
			tgt, tgtOK := names[d.TargetProjectID]
			if srcOK && tgtOK {
				fmt.Fprintf(&sb, "  %q -> %q;\n", src, tgt)
			}
		}
		sb.WriteString("}\n")
		fmt.Print(sb.String())
		return nil
	},
}

// filterSubgraph returns the subset of projects and deps reachable from rootID
// (root + all transitive dependencies).
func filterSubgraph(rootID int64, projects []domain.Project, deps []domain.ProjectDependency) ([]domain.Project, []domain.ProjectDependency) {
	// Collect reachable IDs via BFS following dep.SourceProjectID → dep.TargetProjectID edges.
	reachable := map[int64]bool{rootID: true}
	changed := true
	for changed {
		changed = false
		for _, d := range deps {
			if reachable[d.SourceProjectID] && !reachable[d.TargetProjectID] {
				reachable[d.TargetProjectID] = true
				changed = true
			}
		}
	}
	var ps []domain.Project
	for _, p := range projects {
		if reachable[p.ID] {
			ps = append(ps, p)
		}
	}
	var ds []domain.ProjectDependency
	for _, d := range deps {
		if reachable[d.SourceProjectID] && reachable[d.TargetProjectID] {
			ds = append(ds, d)
		}
	}
	return ps, ds
}

func init() {
	dagCmd.AddCommand(dagVizCmd)
	rootCmd.AddCommand(dagCmd)
}
