package persistence

// Schema contains the complete SQLite DDL for the stAirCase architecture.
const Schema = `
PRAGMA foreign_keys = ON;

-- Vendors & Projects
CREATE TABLE IF NOT EXISTS vendors (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS projects (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    vendor_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    source_path TEXT,
    webhook_url TEXT,
    default_model TEXT,
    budget_usd_per_run REAL DEFAULT 0,
    FOREIGN KEY (vendor_id) REFERENCES vendors(id) ON DELETE CASCADE,
    UNIQUE(vendor_id, name)
);

CREATE TABLE IF NOT EXISTS project_dependencies (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_project_id INTEGER NOT NULL,
    target_project_id INTEGER NOT NULL,
    FOREIGN KEY (source_project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY (target_project_id) REFERENCES projects(id) ON DELETE CASCADE,
    UNIQUE(source_project_id, target_project_id)
);

-- Secrets (Zero-Trace)
CREATE TABLE IF NOT EXISTS secrets (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    key_name TEXT NOT NULL,
    encrypted_value TEXT NOT NULL,
    scoped_to_project_id INTEGER,
    version INTEGER NOT NULL DEFAULT 1,
    is_active INTEGER NOT NULL DEFAULT 1 CHECK(is_active IN (0,1)),
    encryption_scheme TEXT NOT NULL DEFAULT 'aes-256-gcm',
    FOREIGN KEY (scoped_to_project_id) REFERENCES projects(id) ON DELETE CASCADE
);

-- Secret access audit log (SOC2 at-use audit, CHECK 4.1.3).
-- run_id is SET NULL on run delete so audit history is never silently lost.
CREATE TABLE IF NOT EXISTS secret_access_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id INTEGER,
    key_name TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK(outcome IN ('success','error','not_found')),
    accessed_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (run_id) REFERENCES runs(id) ON DELETE SET NULL
);
-- Enforce deterministic secret resolution: one global entry and one per-project entry per key.
CREATE UNIQUE INDEX IF NOT EXISTS uidx_secrets_global
    ON secrets(key_name) WHERE scoped_to_project_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uidx_secrets_project
    ON secrets(key_name, scoped_to_project_id) WHERE scoped_to_project_id IS NOT NULL;

-- Components
CREATE TABLE IF NOT EXISTS components (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    UNIQUE(project_id, name)
);

-- Cases & User Stories
CREATE TABLE IF NOT EXISTS cases (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING'
        CHECK(status IN ('PENDING','RUNNING','COMPLETED','FAILED')),
    last_modified DATETIME DEFAULT CURRENT_TIMESTAMP,
    prd_json TEXT,                    -- Semantic Context Hub
    deleted_at DATETIME,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS user_stories (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    case_id INTEGER NOT NULL,
    description TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING'
        CHECK(status IN ('PENDING','IMPLEMENTED','INVALIDATED')),
    custom_config TEXT,
    FOREIGN KEY (case_id) REFERENCES cases(id) ON DELETE CASCADE
);

-- Swarm Topology (versioned)
CREATE TABLE IF NOT EXISTS swarm_topologies (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    supervisor_name TEXT NOT NULL,
    checkpoint_type TEXT NOT NULL DEFAULT 'memory',
    runtime_type TEXT NOT NULL DEFAULT 'langgraph',
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    UNIQUE(project_id, version)
);

CREATE TABLE IF NOT EXISTS agent_nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    topology_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    role TEXT NOT NULL,
    model TEXT,
    component_id INTEGER,
    FOREIGN KEY (topology_id) REFERENCES swarm_topologies(id) ON DELETE CASCADE,
    FOREIGN KEY (component_id) REFERENCES components(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS agent_tools (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_id INTEGER NOT NULL,
    tool_name TEXT NOT NULL,
    tool_config TEXT,
    FOREIGN KEY (agent_id) REFERENCES agent_nodes(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS edges (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    topology_id INTEGER NOT NULL,
    from_node TEXT NOT NULL,
    to_node TEXT NOT NULL,
    condition TEXT,
    FOREIGN KEY (topology_id) REFERENCES swarm_topologies(id) ON DELETE CASCADE
);

-- Execution Traceability
CREATE TABLE IF NOT EXISTS runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    case_id INTEGER NOT NULL,
    topology_version INTEGER NOT NULL,
    status TEXT NOT NULL DEFAULT 'RUNNING'
        CHECK(status IN ('RUNNING','SUCCESS','FAILED','KILLED')),
    start_time DATETIME DEFAULT CURRENT_TIMESTAMP,
    end_time DATETIME,
    git_branch TEXT NOT NULL,
    git_commit_hash TEXT,
    FOREIGN KEY (case_id) REFERENCES cases(id) ON DELETE CASCADE
);

-- SOC2 Tamper-Proof Event Log
-- git_commit_hash stores the gitCommitHash value at log time so that
-- "staircase inspect log" can verify each entry with the hash that was
-- current when it was written (changes from "" to a real hash at teardown).
CREATE TABLE IF NOT EXISTS run_event_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id INTEGER NOT NULL,
    event_type TEXT NOT NULL,
    payload TEXT NOT NULL,
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
    event_hash TEXT NOT NULL,
    git_commit_hash TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (run_id) REFERENCES runs(id) ON DELETE CASCADE
);

-- Performance indexes
CREATE INDEX IF NOT EXISTS idx_projects_vendor ON projects(vendor_id);
CREATE INDEX IF NOT EXISTS idx_user_stories_case ON user_stories(case_id);
CREATE INDEX IF NOT EXISTS idx_runs_case ON runs(case_id);
-- Composite index makes GetLastEventHash O(1): WHERE run_id = ? ORDER BY id DESC LIMIT 1
CREATE INDEX IF NOT EXISTS idx_run_event_logs_run_id_desc ON run_event_logs(run_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_agent_nodes_topology ON agent_nodes(topology_id);
CREATE INDEX IF NOT EXISTS idx_agent_tools_agent ON agent_tools(agent_id);

-- Migration version tracking: each applied migration is recorded by its zero-based index.
-- Rows are only ever inserted, never updated or deleted.
CREATE TABLE IF NOT EXISTS schema_migrations (
    idx INTEGER PRIMARY KEY,
    applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
`

// Migrations are additive ALTER TABLE statements applied after Schema.
// Each entry is tracked by its zero-based index in the schema_migrations table,
// so every migration runs exactly once regardless of restart or re-init.
var Migrations = []string{
	`ALTER TABLE swarm_topologies ADD COLUMN runtime_type TEXT NOT NULL DEFAULT 'langgraph'`,
	`ALTER TABLE projects ADD COLUMN webhook_url TEXT`,
	`ALTER TABLE cases ADD COLUMN deleted_at DATETIME`,
	// Stores the git commit hash at log-time so hash-chain verification uses
	// the correct per-entry value instead of the run's final commit hash.
	`ALTER TABLE run_event_logs ADD COLUMN git_commit_hash TEXT NOT NULL DEFAULT ''`,
	// Enforce deterministic secret resolution for existing databases.
	// Fresh databases already have these indexes via the base Schema DDL.
	// CREATE UNIQUE INDEX IF NOT EXISTS is a no-op when the index already exists.
	`CREATE UNIQUE INDEX IF NOT EXISTS uidx_secrets_global
	    ON secrets(key_name) WHERE scoped_to_project_id IS NULL`,
	`CREATE UNIQUE INDEX IF NOT EXISTS uidx_secrets_project
	    ON secrets(key_name, scoped_to_project_id) WHERE scoped_to_project_id IS NOT NULL`,
	`ALTER TABLE projects ADD COLUMN default_model TEXT`,
	`ALTER TABLE projects ADD COLUMN budget_usd_per_run REAL DEFAULT 0`,
	// §4.1.1: add versioning and activation columns to secrets (idx 8-10).
	`ALTER TABLE secrets ADD COLUMN version INTEGER NOT NULL DEFAULT 1`,
	`ALTER TABLE secrets ADD COLUMN is_active INTEGER NOT NULL DEFAULT 1`,
	`ALTER TABLE secrets ADD COLUMN encryption_scheme TEXT NOT NULL DEFAULT 'aes-256-gcm'`,
	// §4.1.3: secret at-use audit log; run_id SET NULL on delete so history is never lost (idx 11).
	`CREATE TABLE IF NOT EXISTS secret_access_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		run_id INTEGER,
		key_name TEXT NOT NULL,
		outcome TEXT NOT NULL CHECK(outcome IN ('success','error','not_found')),
		accessed_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (run_id) REFERENCES runs(id) ON DELETE SET NULL
	)`,
}
