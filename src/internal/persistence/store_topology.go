package persistence

import (
	"database/sql"
	"fmt"

	"github.com/b070nd/stAirCase/src/internal/domain"
)

func (s *Store) CreateSwarmTopology(projectID int64, supervisorName, checkpointType, runtimeType string) (*domain.SwarmTopology, error) {
	var maxVersion int
	err := s.db.QueryRow(
		`SELECT COALESCE(MAX(version), 0) FROM swarm_topologies WHERE project_id = ?`, projectID,
	).Scan(&maxVersion)
	if err != nil {
		return nil, fmt.Errorf("get max version: %w", err)
	}
	version := maxVersion + 1
	if checkpointType == "" {
		checkpointType = "memory"
	}
	if runtimeType == "" {
		runtimeType = "langgraph"
	}
	res, err := s.db.Exec(
		`INSERT INTO swarm_topologies (project_id, version, supervisor_name, checkpoint_type, runtime_type) VALUES (?, ?, ?, ?, ?)`,
		projectID, version, supervisorName, checkpointType, runtimeType,
	)
	if err != nil {
		return nil, fmt.Errorf("create topology: %w", err)
	}
	id, _ := res.LastInsertId()
	return &domain.SwarmTopology{
		ID: id, ProjectID: projectID, Version: version,
		SupervisorName: supervisorName, CheckpointType: checkpointType, RuntimeType: runtimeType,
	}, nil
}

func (s *Store) GetTopology(topologyID int64) (*domain.SwarmTopology, error) {
	t := &domain.SwarmTopology{}
	err := s.db.QueryRow(
		`SELECT id, project_id, version, supervisor_name, checkpoint_type, runtime_type FROM swarm_topologies WHERE id = ?`, topologyID,
	).Scan(&t.ID, &t.ProjectID, &t.Version, &t.SupervisorName, &t.CheckpointType, &t.RuntimeType)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

func (s *Store) GetLatestTopology(projectID int64) (*domain.SwarmTopology, error) {
	t := &domain.SwarmTopology{}
	err := s.db.QueryRow(
		`SELECT id, project_id, version, supervisor_name, checkpoint_type, runtime_type
		 FROM swarm_topologies WHERE project_id = ? ORDER BY version DESC LIMIT 1`, projectID,
	).Scan(&t.ID, &t.ProjectID, &t.Version, &t.SupervisorName, &t.CheckpointType, &t.RuntimeType)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

func (s *Store) ListTopologiesByProject(projectID int64) ([]domain.SwarmTopology, error) {
	rows, err := s.db.Query(
		`SELECT id, project_id, version, supervisor_name, checkpoint_type, runtime_type
		 FROM swarm_topologies WHERE project_id = ? ORDER BY version DESC`, projectID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ts []domain.SwarmTopology
	for rows.Next() {
		var t domain.SwarmTopology
		if err := rows.Scan(&t.ID, &t.ProjectID, &t.Version, &t.SupervisorName, &t.CheckpointType, &t.RuntimeType); err != nil {
			return nil, err
		}
		ts = append(ts, t)
	}
	return ts, rows.Err()
}

// AgentNode

func (s *Store) CreateAgentNode(topologyID int64, name, role, model string, componentID *int64) (*domain.AgentNode, error) {
	res, err := s.db.Exec(
		`INSERT INTO agent_nodes (topology_id, name, role, model, component_id) VALUES (?, ?, ?, ?, ?)`,
		topologyID, name, role, model, componentID,
	)
	if err != nil {
		return nil, fmt.Errorf("create agent node: %w", err)
	}
	id, _ := res.LastInsertId()
	return &domain.AgentNode{
		ID: id, TopologyID: topologyID, Name: name,
		Role: role, Model: model, ComponentID: componentID,
	}, nil
}

func (s *Store) GetAgentNode(nodeID int64) (*domain.AgentNode, error) {
	n := &domain.AgentNode{}
	err := s.db.QueryRow(
		`SELECT id, topology_id, name, role, COALESCE(model,''), component_id FROM agent_nodes WHERE id = ?`, nodeID,
	).Scan(&n.ID, &n.TopologyID, &n.Name, &n.Role, &n.Model, &n.ComponentID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return n, err
}

func (s *Store) ListAgentNodes(topologyID int64) ([]domain.AgentNode, error) {
	rows, err := s.db.Query(
		`SELECT id, topology_id, name, role, COALESCE(model,''), component_id
		 FROM agent_nodes WHERE topology_id = ?`, topologyID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var nodes []domain.AgentNode
	for rows.Next() {
		var n domain.AgentNode
		if err := rows.Scan(&n.ID, &n.TopologyID, &n.Name, &n.Role, &n.Model, &n.ComponentID); err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// AgentTool

func (s *Store) CreateAgentTool(agentID int64, toolName, toolConfig string) (*domain.AgentTool, error) {
	res, err := s.db.Exec(
		`INSERT INTO agent_tools (agent_id, tool_name, tool_config) VALUES (?, ?, ?)`,
		agentID, toolName, toolConfig,
	)
	if err != nil {
		return nil, fmt.Errorf("create agent tool: %w", err)
	}
	id, _ := res.LastInsertId()
	return &domain.AgentTool{ID: id, AgentID: agentID, ToolName: toolName, ToolConfig: toolConfig}, nil
}

func (s *Store) ListAgentTools(agentID int64) ([]domain.AgentTool, error) {
	rows, err := s.db.Query(
		`SELECT id, agent_id, tool_name, COALESCE(tool_config,'') FROM agent_tools WHERE agent_id = ?`, agentID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var tools []domain.AgentTool
	for rows.Next() {
		var t domain.AgentTool
		if err := rows.Scan(&t.ID, &t.AgentID, &t.ToolName, &t.ToolConfig); err != nil {
			return nil, err
		}
		tools = append(tools, t)
	}
	return tools, rows.Err()
}

// Edge

func (s *Store) CreateEdge(topologyID int64, from, to, condition string) (*domain.Edge, error) {
	res, err := s.db.Exec(
		`INSERT INTO edges (topology_id, from_node, to_node, condition) VALUES (?, ?, ?, ?)`,
		topologyID, from, to, condition,
	)
	if err != nil {
		return nil, fmt.Errorf("create edge: %w", err)
	}
	id, _ := res.LastInsertId()
	return &domain.Edge{ID: id, TopologyID: topologyID, FromNode: from, ToNode: to, Condition: condition}, nil
}

func (s *Store) ListEdges(topologyID int64) ([]domain.Edge, error) {
	rows, err := s.db.Query(
		`SELECT id, topology_id, from_node, to_node, COALESCE(condition,'') FROM edges WHERE topology_id = ?`, topologyID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var edges []domain.Edge
	for rows.Next() {
		var e domain.Edge
		if err := rows.Scan(&e.ID, &e.TopologyID, &e.FromNode, &e.ToNode, &e.Condition); err != nil {
			return nil, err
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

// Run
