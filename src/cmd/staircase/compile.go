package main

import (
	"encoding/json"
	"fmt"
	"github.com/b070nd/staircase-core/src/internal/plan"
	"os"
	"path/filepath"
	"strings"

	"github.com/b070nd/staircase-core/src/internal/domain"
	"github.com/b070nd/staircase-core/src/internal/engine"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var compileForce bool

var compileCmd = &cobra.Command{
	Use:   "compile <case-id>",
	Short: "Compile a Case into the plan staircase run executes",
	Args:  cobra.ExactArgs(1),
	RunE:  compileCaseHandler,
}

func init() {
	compileCmd.Flags().BoolVar(&compileForce, "force", false, "Overwrite an existing plan")
	rootCmd.AddCommand(compileCmd)
}

func compileCaseHandler(_ *cobra.Command, args []string) error {
	caseID, err := parseID("case-id", args[0])
	if err != nil {
		return err
	}

	wsDir := viper.GetString("STAIRCASE_DIR")

	// ── 1. Database ───────────────────────────────────────────────────────────
	db, err := persistence.InitDB(wsDir)
	if err != nil {
		return fmt.Errorf("db init: %w", err)
	}
	defer func() { _ = db.Close() }()
	store := persistence.NewStore(db)

	// ── 2. Load Case + Project ────────────────────────────────────────────────
	caseRec, err := store.GetCase(caseID)
	if err != nil || caseRec == nil {
		return fmt.Errorf("case %d not found", caseID)
	}
	project, err := store.GetProject(caseRec.ProjectID)
	if err != nil || project == nil {
		return fmt.Errorf("project %d not found", caseRec.ProjectID)
	}

	fmt.Printf("⚙️  Compiling case #%d  project=%q\n", caseID, project.Name)

	// ── 3. DAG Resolution (spec §4.1) ─────────────────────────────────────────
	allDeps, err := store.ListAllProjectDependencies()
	if err != nil {
		return fmt.Errorf("load deps: %w", err)
	}

	projectSet, err := resolveProjectSet(store, project.ID, allDeps)
	if err != nil {
		return fmt.Errorf("resolve project set: %w", err)
	}
	ordered, err := engine.TopoSort(projectSet, allDeps)
	if err != nil {
		return fmt.Errorf("dependency cycle: %w", err)
	}

	names := make([]string, len(ordered))
	for i, p := range ordered {
		names[i] = p.Name
	}
	fmt.Printf("   📦 Dependency order: %s\n", strings.Join(names, " → "))

	// ── 4. Repo Skeletonization + Semantic XML Packing (spec §4.1) ────────────
	var repoParts []string
	for _, p := range ordered {
		if p.SourcePath == "" {
			continue
		}
		repoMap, err := engine.RepoMap(p.SourcePath)
		if err != nil {
			return fmt.Errorf("repo map for %q: %w", p.Name, err)
		}
		repoParts = append(repoParts, fmt.Sprintf("<repo project=%q>\n%s</repo>", p.Name, repoMap))
	}
	repoContext := ""
	if len(repoParts) > 0 {
		repoContext = "<context>\n" + strings.Join(repoParts, "\n") + "\n</context>"
	}

	// ── 5. Token Budget Warning (spec §4.1) ───────────────────────────────────
	if warn := engine.WarnTokenBudget(caseRec.PrdJSON + repoContext); warn != "" {
		fmt.Printf("   %s\n", warn)
		fmt.Printf("   Agents will use read_file for lazy context fetching.\n")
	}

	// ── 6. Swarm Topology ─────────────────────────────────────────────────────
	topology, err := store.GetLatestTopology(project.ID)
	if err != nil {
		return fmt.Errorf("load topology: %w", err)
	}
	if topology == nil {
		return fmt.Errorf("no swarm topology registered for project %q", project.Name)
	}
	fmt.Printf("   🕸  Topology v%d  supervisor=%q  checkpoint=%s\n",
		topology.Version, topology.SupervisorName, topology.CheckpointType)

	agentNodes, err := store.ListAgentNodes(topology.ID)
	if err != nil {
		return fmt.Errorf("load agent nodes: %w", err)
	}
	edges, err := store.ListEdges(topology.ID)
	if err != nil {
		return fmt.Errorf("load edges: %w", err)
	}

	// ── 7. Build the plan ─────────────────────────────────────────────────────
	pl := plan.Plan{CaseID: caseID, TopologyVersion: topology.Version, Supervisor: topology.SupervisorName,
		PRD: caseRec.PrdJSON, RepoContext: repoContext}
	for _, n := range agentNodes {
		tools, _ := store.ListAgentTools(n.ID)
		toolNames := make([]string, len(tools))
		for i, t := range tools {
			toolNames[i] = t.ToolName
		}
		model := n.Model
		if model == "" {
			model = "claude-sonnet-4-6"
		}
		pl.Agents = append(pl.Agents, plan.Agent{Name: n.Name, Role: n.Role, Model: model, Tools: toolNames})
	}
	for _, e := range edges {
		pl.Edges = append(pl.Edges, plan.Edge{From: e.FromNode, To: e.ToNode, Condition: e.Condition})
	}
	stories, err := store.ListUserStoriesByCase(caseID)
	if err != nil {
		return fmt.Errorf("load stories: %w", err)
	}
	for _, st := range stories {
		ps := plan.Story{ID: st.ID, Text: st.Description}
		if st.CustomConfig != "" { // a story's scope: {"allow": [...], "max_files": N}
			if err := json.Unmarshal([]byte(st.CustomConfig), &ps); err != nil {
				return fmt.Errorf("story #%d scope: %w", st.ID, err)
			}
			ps.ID, ps.Text = st.ID, st.Description
		}
		pl.Stories = append(pl.Stories, ps)
	}
	if pl.BlueprintHash, _, err = store.CaseBlueprint(caseID); err != nil {
		return fmt.Errorf("load case binding: %w", err)
	}

	// ── 8. Write the plan (run checks its sha256 sidecar) ─────────────────────
	tmpDir := filepath.Join(wsDir, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return fmt.Errorf("mkdir tmp: %w", err)
	}
	outPath := filepath.Join(tmpDir, fmt.Sprintf("plan_case%d.json", caseID))
	if !compileForce {
		if _, err := os.Stat(outPath); err == nil {
			return fmt.Errorf("plan already exists: %s\n  → use --force to overwrite", outPath)
		}
	}
	if err := plan.Write(outPath, pl); err != nil {
		return fmt.Errorf("compile plan: %w", err)
	}

	fmt.Printf("   ✅ Generated: %s\n", outPath)
	fmt.Printf("   🚀 Run with:  staircase run %d\n", caseID)
	return nil
}

// resolveProjectSet returns a project and all its transitive dependencies as a flat list.
func resolveProjectSet(store *persistence.Store, rootID int64, allDeps []domain.ProjectDependency) ([]domain.Project, error) {
	visited := map[int64]bool{}
	var collect func(id int64) error
	collect = func(id int64) error {
		if visited[id] {
			return nil
		}
		visited[id] = true
		for _, dep := range allDeps {
			if dep.SourceProjectID == id {
				if err := collect(dep.TargetProjectID); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := collect(rootID); err != nil {
		return nil, err
	}

	result := make([]domain.Project, 0, len(visited))
	for id := range visited {
		p, err := store.GetProject(id)
		if err != nil || p == nil {
			continue
		}
		result = append(result, *p)
	}
	return result, nil
}
