package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var topologyCmd = &cobra.Command{
	Use:   "topology",
	Short: "Manage swarm topologies (agents, edges, tools)",
}

// ── register ──────────────────────────────────────────────────────────────────

var (
	topoCheckpoint string
	topoRuntime    string
)

var topoRegisterCmd = &cobra.Command{
	Use:   "register <project-id> <supervisor-name>",
	Short: "Create a new versioned swarm topology for a project",
	Args:  cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		projectID, err := parseID("project-id", args[0])
		if err != nil {
			return err
		}
		if p, _ := store.GetProject(projectID); p == nil {
			return fmt.Errorf("project #%d not found", projectID)
		}

		t, err := store.CreateSwarmTopology(projectID, args[1], topoCheckpoint, topoRuntime)
		if err != nil {
			return fmt.Errorf("create topology: %w", err)
		}
		fmt.Printf("✅ Topology #%d v%d created  supervisor=%q  checkpoint=%s  runtime=%s\n",
			t.ID, t.Version, t.SupervisorName, t.CheckpointType, t.RuntimeType)
		fmt.Printf("   Add agents: staircase topology agent add %d <name> <role>\n", t.ID)
		return nil
	},
}

// ── agent ─────────────────────────────────────────────────────────────────────

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Manage agent nodes in a topology",
}

var (
	agentModel string
)

var agentAddCmd = &cobra.Command{
	Use:   "add <topology-id> <name> <role>",
	Short: "Add an agent node to a topology",
	Args:  cobra.ExactArgs(3),
	RunE: func(_ *cobra.Command, args []string) error {
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		topoID, err := parseID("topology-id", args[0])
		if err != nil {
			return err
		}
		if t, _ := store.GetTopology(topoID); t == nil {
			return fmt.Errorf("topology #%d not found", topoID)
		}

		model := agentModel
		if model == "" {
			// Check if the project has a configured default model.
			topo, _ := store.GetTopology(topoID)
			if topo != nil {
				if projModel, _, err := store.GetProjectConfig(topo.ProjectID); err == nil && projModel != "" {
					model = projModel
				}
			}
			if model == "" {
				model = "claude-sonnet-4-6"
			}
		}

		n, err := store.CreateAgentNode(topoID, args[1], args[2], model, nil)
		if err != nil {
			return fmt.Errorf("create agent node: %w", err)
		}
		fmt.Printf("✅ Agent #%d %q added to topology #%d  model=%s\n", n.ID, n.Name, topoID, n.Model)
		return nil
	},
}

// ── edge ──────────────────────────────────────────────────────────────────────

var edgeCmd = &cobra.Command{
	Use:   "edge",
	Short: "Manage edges (routing) in a topology",
}

var (
	edgeCondition string
)

var edgeAddCmd = &cobra.Command{
	Use:   "add <topology-id> <from-node> <to-node>",
	Short: "Add a directed edge between two agent nodes",
	Args:  cobra.ExactArgs(3),
	RunE: func(_ *cobra.Command, args []string) error {
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		topoID, err := parseID("topology-id", args[0])
		if err != nil {
			return err
		}

		e, err := store.CreateEdge(topoID, args[1], args[2], edgeCondition)
		if err != nil {
			return fmt.Errorf("create edge: %w", err)
		}
		label := "→"
		if e.Condition != "" {
			label = fmt.Sprintf("─[%s]→", e.Condition)
		}
		fmt.Printf("✅ Edge #%d: %s %s %s\n", e.ID, e.FromNode, label, e.ToNode)
		return nil
	},
}

// ── tool ──────────────────────────────────────────────────────────────────────

var toolCmd = &cobra.Command{
	Use:   "tool",
	Short: "Register extra tools for an agent node",
}

var (
	toolConfig string
)

var toolAddCmd = &cobra.Command{
	Use:   "add <agent-id> <tool-name>",
	Short: "Register a tool for an agent node",
	Args:  cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		agentID, err := parseID("agent-id", args[0])
		if err != nil {
			return err
		}
		if n, _ := store.GetAgentNode(agentID); n == nil {
			return fmt.Errorf("agent node #%d not found", agentID)
		}

		t, err := store.CreateAgentTool(agentID, args[1], toolConfig)
		if err != nil {
			return fmt.Errorf("create tool: %w", err)
		}
		fmt.Printf("✅ Tool #%d %q registered for agent #%d\n", t.ID, t.ToolName, agentID)
		return nil
	},
}

// ── show ──────────────────────────────────────────────────────────────────────

var topoShowCmd = &cobra.Command{
	Use:   "show <project-id>",
	Short: "Show the latest swarm topology for a project",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		projectID, err := parseID("project-id", args[0])
		if err != nil {
			return err
		}

		t, err := store.GetLatestTopology(projectID)
		if err != nil {
			return err
		}
		if t == nil {
			fmt.Printf("No topology found for project #%d.\n", projectID)
			return nil
		}

		fmt.Printf("Topology #%d  v%d  project #%d\n", t.ID, t.Version, t.ProjectID)
		fmt.Printf("  Supervisor:  %s\n", t.SupervisorName)
		fmt.Printf("  Checkpoint:  %s\n", t.CheckpointType)
		fmt.Printf("  Runtime:     %s\n", t.RuntimeType)

		nodes, _ := store.ListAgentNodes(t.ID)
		if len(nodes) > 0 {
			fmt.Printf("\n  Agents (%d):\n", len(nodes))
			for _, n := range nodes {
				tools, _ := store.ListAgentTools(n.ID)
				toolNames := make([]string, len(tools))
				for i, tool := range tools {
					toolNames[i] = tool.ToolName
				}
				toolStr := ""
				if len(toolNames) > 0 {
					toolStr = "  tools=[" + strings.Join(toolNames, ",") + "]"
				}
				fmt.Printf("    #%d %-20s  model=%-30s  role=%s%s\n",
					n.ID, n.Name, n.Model, n.Role, toolStr)
			}
		}

		edges, _ := store.ListEdges(t.ID)
		if len(edges) > 0 {
			fmt.Printf("\n  Edges (%d):\n", len(edges))
			for _, e := range edges {
				arrow := "→"
				if e.Condition != "" {
					arrow = fmt.Sprintf("─[%s]→", e.Condition)
				}
				fmt.Printf("    %s %s %s\n", e.FromNode, arrow, e.ToNode)
			}
		}
		return nil
	},
}

func init() {
	topoRegisterCmd.Flags().StringVar(&topoCheckpoint, "checkpoint", "memory", "Checkpoint type (memory or sqlite)")
	topoRegisterCmd.Flags().StringVar(&topoRuntime, "runtime", "langgraph", "Agent runtime (langgraph, crewai, autogen)")
	agentAddCmd.Flags().StringVar(&agentModel, "model", "", "LLM model identifier (default: claude-sonnet-4-6)")
	edgeAddCmd.Flags().StringVar(&edgeCondition, "condition", "", "Conditional routing expression")
	toolAddCmd.Flags().StringVar(&toolConfig, "config", "", "JSON tool configuration")

	agentCmd.AddCommand(agentAddCmd)
	edgeCmd.AddCommand(edgeAddCmd)
	toolCmd.AddCommand(toolAddCmd)
	topologyCmd.AddCommand(topoRegisterCmd, agentCmd, edgeCmd, toolCmd, topoShowCmd)
	rootCmd.AddCommand(topologyCmd)
}
