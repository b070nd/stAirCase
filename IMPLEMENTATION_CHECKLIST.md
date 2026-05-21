# stAirCase — Implementation Protocol Checklist

**Module:** `github.com/b070nd/staircase-core`
**Manifesto sources:** `.tasks/2026-03-19-isolation.md`, `.tasks/2026-03-18-enterprise.md`, `.tasks/2026-03-18-enterptise-context.md`
**Last audited:** 2026-05-21 (post Sprint 1/2 security remediation + audit FAIL fixes)
**Test suite:** 538 tests · 0 failures · 0 skips (on macOS/Linux) · 76.6% total coverage

---

## Legend
- ✅ Implemented and test-covered
- ⚠️ Implemented; structural/integration test gap noted
- ❌ Not implemented
- 🔒 Security-critical path

---

## §1 — System Identity & Zero-Trace Policy

| Requirement | Status | Evidence |
|---|---|---|
| All state lives in `$STAIRCASE_DIR` (workspace.db, venv, tmp, .key) | ✅ | `persistence.InitDB`, `runtime.BootstrapVenv`, path constants |
| No artifacts written into target repositories | ✅ | All paths under `wsDir`; `compile.go` writes to `tmp/` only |
| `STAIRCASE_DIR` configurable via env var (Paranoia/tmpfs mode) | ✅ | `viper.GetString("STAIRCASE_DIR")` in every command handler |
| Single static binary (`CGO_ENABLED=0`, `modernc.org/sqlite`) | ✅ | `go.mod`; pure-Go SQLite driver |

---

## §2 — Environment & Toolchain

### 2.1 Go Requirements
| Requirement | Status | Evidence |
|---|---|---|
| Go 1.22+ | ⚠️ | `go.mod: go 1.26.1`; govulncheck reports 6 active CVEs fixed in 1.26.2/1.26.3 — upgrade pending |
| `CGO_ENABLED=0` | ✅ | `modernc.org/sqlite v1.47.0` |
| Cobra CLI framework | ✅ | All cmd handlers |
| Viper config | ✅ | `STAIRCASE_DIR` lookup |
| Bubbletea HITL TUI | ✅ | `tui/yield.go` |
| Glamour Markdown rendering | ✅ | `tui/yield.go` |

### 2.2 Python Venv Management
| Requirement | Status | Evidence |
|---|---|---|
| Isolated venv in `$STAIRCASE_DIR/venv/` | ✅ | `runtime/venv.go:BootstrapVenv` |
| Frozen embedded `requirements.txt` | ✅ | `//go:embed requirements.txt` |
| SHA-256 hash tracks requirements changes | ✅ | `.requirements_hash` file |
| `--offline-wheels <dir>` air-gap support | ✅ | `--no-index --find-links` pip args |
| Rosetta/ARM64 vs x86_64 detection (macOS) | ✅ | `platform.machine()` check |
| Pip failure → human-readable error + `doctor --fix-venv` hint | ✅ | stderr parse in `runPipInstall` |
| Broken venv sentinel (`.requirements_hash.broken`) | ✅ | Written on pip failure; triggers `RemoveAll` on next init |
| `staircase_runner` importable in venv | ✅ | `recording.py` embedded in binary; written to `venvPath/runner_inject/staircase_runner/` on bootstrap; injected into `sys.path` via `RunnerPath` in `BootstrapMessage` |
| `staircase doctor` workspace diagnostics | ✅ | `cmd/staircase/doctor.go` |
| `staircase doctor --fix-venv` venv rebuild | ✅ | `doctorHandler` + `BootstrapVenv` |

### 2.3 IPC Architecture
| Requirement | Status | Evidence |
|---|---|---|
| Bootstrap via stdin (one-time token) | 🔒✅ | `runtime/python.go:LaunchPython` |
| UDS high-throughput channel (Unix) | ✅ | `ipc/server.go:Start` (`net.Listen("unix", ...)`) |
| TCP loopback fallback (Windows) | ✅ | `runtime.GOOS == "windows"` branch in `Start` |
| Socket mode 0600 (Unix) | 🔒✅ | `os.Chmod(s.socketPath, 0600)` after `Listen` |
| Auth timeout 10s | 🔒✅ | `authTimeout = 10s` |
| Heartbeat timeout 30s (zombie reaper) | ✅ | `heartbeatTimeout = 30s` |
| Connection limit (`maxConnections = 2`) | 🔒✅ | atomic `connCount` guard |
| Payload size cap 64 KiB | ✅ | `maxPayloadSize = 64*1024` before `AppendEventLog` |
| `SIGKILL` on hang/context cancel | ✅ | `proc.Kill()` in `runLoop` ctx.Done branch |
| Python stdin readline timeout 30s | ✅ | `select.select([sys.stdin], [], [], 30.0)` in template |

---

## §3 — Data & State Management

### 3.1 SQLite Schema
| Table | Status | Notes |
|---|---|---|
| `vendors` | ✅ | UNIQUE(name) |
| `projects` | ✅ | UNIQUE(vendor_id, name), `source_path`, `webhook_url` |
| `project_dependencies` | ✅ | UNIQUE(source, target), cascade delete |
| `secrets` | ✅ | Partial UNIQUE indexes: global + per-project |
| `components` | ✅ | UNIQUE(project_id, name) |
| `cases` | ✅ | CHECK(status IN (…)), `prd_json`, soft-delete `deleted_at` |
| `user_stories` | ✅ | CHECK(status IN (…)), `custom_config` |
| `swarm_topologies` | ✅ | UNIQUE(project_id, version), `runtime_type` |
| `agent_nodes` | ✅ | FK → topology, component |
| `agent_tools` | ✅ | FK → agent_node |
| `edges` | ✅ | FK → topology |
| `runs` | ✅ | CHECK(status IN (…)); `topology_version` validated in `CreateRun` |
| `run_event_logs` | ✅ | SHA-256 chain; composite index `(run_id, id DESC)` for O(1) tail lookup |
| `schema_migrations` | ✅ | Records applied migration indices; fresh-DB pre-seeding |

### Schema Integrity
| Constraint | Status | Notes |
|---|---|---|
| `cases.status` CHECK | ✅ | `IN ('PENDING','RUNNING','COMPLETED','FAILED')` |
| `runs.status` CHECK | ✅ | `IN ('RUNNING','SUCCESS','FAILED','KILLED')` |
| `user_stories.status` CHECK | ✅ | `IN ('PENDING','IMPLEMENTED','INVALIDATED')` |
| `secrets` unique (global) | ✅ | Partial index on `key_name WHERE scoped=NULL` |
| `secrets` unique (project-scoped) | ✅ | Partial index on `(key_name, project_id) WHERE scoped NOT NULL` |
| `runs.topology_version` referential integrity | ✅ | Validated in `Store.CreateRun` (FK not expressible in SQLite) |
| `run_event_logs` scale mitigation (>1M rows) | ✅ | `PruneEventLogs(1_000_000)` in `clean --aggressive` |

### 3.2 IPC Protocol Schemas
| Message | Status | Fields |
|---|---|---|
| `IpcStateEmit` | ✅ | `active_agent`, `state` |
| `IpcYieldRequest` | ✅ | `agent_name`, `action_type`, `proposed_edits` (search/replace), `reasoning_trace`, `confidence_score`, `batch_id` |
| `IpcYieldResponse` | ✅ | `approved`, `feedback` |
| `IpcSecretRequest` | ✅ | `key_name`, `project_id` |
| `IpcSecretResponse` | ✅ | plaintext value OR `error` (never raw ciphertext) |

---

## §4 — Execution & Business Logic

### 4.1 Compile Pipeline (Context-Hub Pattern)
| Requirement | Status | Evidence |
|---|---|---|
| DAG resolution (topological sort) | ✅ | `engine.TopoSort` (Kahn's algorithm) |
| Cycle detection with named projects | ✅ | `TestTopoSort_cycle_error_names_involved_projects` |
| Multi-project transitive compilation | ✅ | `resolveProjectSet` in `compile.go` |
| Repo Skeletonization (`RepoMap`) | ✅ | `engine.RepoMap` — directory tree + function signatures |
| `.gitignore` / `.staircaseignore` respect | ✅ | `loadIgnorePatterns` |
| `**` glob support in ignore files | ✅ | `matchGlob` with multi-segment `**` expansion |
| Semantic XML packing (`<repo>…</repo>`) | ✅ | `PackXML`; `<context>` wrapper in `compile.go` |
| Token budget warning (> 100k tokens) | ✅ | `engine.WarnTokenBudget` |
| Lazy fetching: `read_file` tool in generated Python | ✅ | `@tool def read_file(…)` in template; path-traversal safe |
| Lazy fetching: `list_dir` tool in generated Python | ✅ | `@tool def list_dir(…)` in template; path-traversal safe |
| Exact search-and-replace editing (no diffs) | ✅ | `request_edit` tool with `search_block` / `replace_block` |
| CRLF normalization in `request_edit` | ✅ | `src.replace("\r\n", "\n")` before match |
| Atomic file write in `request_edit` | ✅ | `tempfile.mkstemp` + `os.replace` |
| `active_agents []string` for parallel fan-out | ✅ | `AgentState.active_agents` field in generated Python |
| `staircase/run-N` blast-radius branch snapshot | ✅ | Template writes to isolated git branch |
| Topology version sidecar (`.topo` file) | ✅ | Written by `compile.go`; read by `runtime.script_compiled` gate |
| `dag viz` DOT format output | ✅ | `cmd/staircase/dag.go:dagVizCmd` |

### 4.2 Run Loop
| Requirement | Status | Evidence |
|---|---|---|
| Dirty-tree pre-flight (`git status --porcelain`) | ✅ | `handleDirtyTree` |
| `--auto-stash` flag + stash pop on teardown | ✅ | `runAutoStash`; defer pop |
| `--force` flag (skip dirty-tree check) | ✅ | `runForce` flag |
| Blast-radius branch `staircase/run-{RunID}` | ✅ | `git checkout -b` |
| Stale branch cleanup before create | ✅ | `git rev-parse --verify` + `git branch -D` |
| Secure bootstrap: token via stdin only | 🔒✅ | `stdin.Close()` immediately after write; not in `cmd.Env` |
| IPC server start before Python launch | ✅ | Ordering in `runCaseHandler` |
| Python subprocess via isolated venv binary | ✅ | `$STAIRCASE_DIR/venv/bin/python` |
| HITL TUI on `yield_request` | ✅ | `tui.RunYieldTUI(yieldReq)` |
| Webhook fallback for external HITL | ✅ | `sendWebhookYield` in `run.go` |
| Kill stale RUNNING runs on start (> 2h) | ✅ | `store.KillStaleRuns(caseID, 2*time.Hour)` |
| `--dry-run` flag (print plan, no Python launch) | ✅ | `runDryRun` flag |
| `--skip-gates` flag for CI bypass | ✅ | `runSkipGate` flag |
| SOC2 event log with SHA-256 chain | 🔒✅ | `store.AppendEventLog` with `prevHash + gitHash` |
| `inspect log` hash-chain re-verification | ✅ | `inspect.go` re-derives expected hash, marks `❌ TAMPERED` |
| `inspect log --full` payload reveal | ✅ | `inspectLogFull` flag; default truncates at 120 chars |
| git commit on SUCCESS | ✅ | `git add . + git commit` in teardown |
| `UpdateRunStatus` + `GitCommitHash` | ✅ | `store.UpdateRunStatus` after commit |
| Mark `UserStory` PENDING → IMPLEMENTED on SUCCESS | ✅ | Loop in teardown |
| Branch restore on non-SUCCESS (pessimistic defer) | ✅ | Defer in `runCaseHandler` |

### 4.3 Maintenance & Graceful Teardown
| Requirement | Status | Evidence |
|---|---|---|
| `staircase clean` — GC `tmp/` scripts + sockets | ✅ | `cleanHandler`: removes `*.py` + `*.sock` |
| `staircase clean --aggressive` | ✅ | Venv prune + stale branches + flagged cases |
| Stale `staircase/run-*` branches older than 30 days | ✅ | `pruneRepoBranches` with 30-day cutoff |
| `--keep-failed` forensic preservation | ✅ | `preservedRunIDs` set in `cleanHandler` |
| `--dry-run` (print targets, no delete) | ✅ | `cleanDryRun` flag in `removeTarget` |
| Symlink escape prevention in clean | ✅ | `os.Lstat` + `ModeSymlink` check |
| Preserve Vendor/Project/Topology during clean | ✅ | Clean only touches `tmp/`, `venv/`, flagged cases |
| Soft-delete cases (`FlagCaseDeleted`) | ✅ | `deleted_at` column; `DeleteFlaggedCases` sweeps on aggressive |
| Event log pruning > 1M rows | ✅ | `store.PruneEventLogs(1_000_000)` in aggressive clean |

---

## §5 — Security Model

| Control | Status | Notes |
|---|---|---|
| AES-256-GCM encryption at rest | 🔒✅ | `crypto/encrypt.go` |
| `v1:` version prefix on all ciphertexts | 🔒✅ | Forward-compatible; legacy bare base64 still decrypts |
| Atomic key file write (tempfile + rename) | 🔒✅ | `os.CreateTemp` + `os.Rename`; no partial writes |
| Key file permissions 0600 enforced at write | 🔒✅ | `tmp.Chmod(0600)` before rename |
| Key file permissions verified at read (`LoadKey`) | 🔒✅ | `os.Stat` perm check on Unix; error if `perm & 0177 != 0` |
| Token passed via stdin only (never env var) | 🔒✅ | `/proc/[pid]/environ` leak prevention |
| Constant-time token comparison | 🔒✅ | `crypto/subtle.ConstantTimeCompare` |
| Auth timeout (10s on new connections) | 🔒✅ | `authTimeout` deadline |
| Heartbeat timeout (30s dead-connection reaper) | 🔒✅ | `heartbeatTimeout` read deadline |
| UDS socket mode 0600 | 🔒✅ | `os.Chmod(socketPath, 0600)` after `Listen` |
| Connection limit (max 2 simultaneous) | 🔒✅ | Atomic `connCount`; over-limit connections dropped |
| Payload size cap (64 KiB per log entry) | ✅ | `maxPayloadSize` in IPC server |
| Decrypt secrets in Go before Python delivery | 🔒✅ | `crypto.Decrypt(s.aesKey, ...)` in `secret_request` handler |
| Secret project_id scoping | 🔒✅ | IPC server rejects `secret_request` with mismatched `project_id` |
| Secret scrubbing before HITL render | 🔒✅ | `scrubSecrets()` replaces all secret plaintexts in `YieldRequest` before TUI/webhook/approvalhttp |
| Workspace directory permissions 0700 | 🔒✅ | `os.MkdirAll(workspaceDir, 0o700)` in `persistence.InitDB` |
| Error response on secret lookup failure (no hang) | ✅ | `IpcSecretResponse{Error: "not found"}` |
| Unique secret constraints (DB-level) | ✅ | Partial UNIQUE indexes via migrations |
| `CreateRun` topology version validation | ✅ | Store-level guard; rejects unknown versions |
| Path traversal prevention in `read_file` | 🔒✅ | `os.path.realpath` + root-prefix check |
| Path traversal prevention in `request_edit` | 🔒✅ | Same pattern |
| Path traversal prevention in `list_dir` | 🔒✅ | Same pattern |
| SOC2 tamper-proof event log (SHA-256 chain) | 🔒✅ | `payload + prevHash + gitCommitHash` |

---

## §6 — Quality Gate System (17 gates)

### Structural (9)
| Gate | Severity | Status |
|---|---|---|
| `case.project_exists` | BLOCK | ✅ Detects deleted cases |
| `case.has_stories` | BLOCK | ✅ Requires ≥1 PENDING story |
| `case.has_prd` | BLOCK | ✅ Requires non-empty `prd_json` |
| `topology.exists` | BLOCK | ✅ Requires registered topology |
| `topology.has_agents` | BLOCK | ✅ Requires ≥1 agent node |
| `topology.supervisor_registered` | BLOCK | ✅ Supervisor node must be declared |
| `topology.edges_valid` | BLOCK | ✅ All edge endpoints must be registered nodes |
| `topology.no_orphan_agents` | WARN | ✅ Detects unreachable agent nodes |
| `topology.runtime_valid` | BLOCK | ✅ Only `langgraph` / `crewai` accepted |

### Security (3)
| Gate | Severity | Status |
|---|---|---|
| `secret.key_file` | BLOCK | ✅ Checks existence, size, and mode 0600 |
| `secret.anthropic_key` | BLOCK | ✅ Global or project-scoped secret required |
| `secret.no_duplicates` | WARN | ✅ Advisory (DB constraint is the authoritative guard) |

### Runtime (6)
| Gate | Severity | Status |
|---|---|---|
| `runtime.script_compiled` | BLOCK | ✅ Checks script exists; warns if topology version is stale |
| `runtime.venv_ready` | BLOCK | ✅ Checks venv python + `.requirements_hash` |
| `runtime.venv_broken` | BLOCK | ✅ Fails if `.requirements_hash.broken` sentinel present |
| `runtime.source_path` | BLOCK | ✅ Validates source_path is accessible on disk |
| `runtime.no_concurrent_run` | BLOCK | ✅ Blocks if another run is RUNNING for this case |
| `runtime.git_available` | BLOCK | ✅ Requires git in PATH when source_path is set |

### Dependency (2)
| Gate | Severity | Status |
|---|---|---|
| `deps.deps_completed` | WARN | ✅ Checks upstream projects have SUCCESS run at **current** topology version |
| `deps.no_cycle` | BLOCK | ✅ DAG cycle detection |

---

## §7 — Test Coverage

| Package | Coverage | Type | Notes |
|---|---|---|---|
| `internal/crypto` | 83.1% | Unit | Encrypt/decrypt, key gen, permissions, v1 prefix, legacy compat |
| `internal/engine` | 96.1% | Unit | DAG (all cycle/sort cases), RepoMap, `**` glob, XML pack, token budget |
| `internal/persistence` | 85.2% | Integration | All store methods; unique constraints; migrations; CreateRun validation |
| `internal/template` | 88.0% | Unit | pyStr, pyIdent, GenerateGraphExec, read_file, list_dir, request_edit, conditional edges |
| `internal/gate` | 92.1% | Integration | All 17 gates; staleness detection; topology version deps; edge cases |
| `internal/ipc` | 86.8% | Integration | Auth, heartbeat, state_emit, payload cap, yield round-trip, secret decrypt, project_id scoping, connection limit |
| `internal/approvalhttp` | 96.4% | Integration | All endpoints; auth; race conditions (409 Conflict); TLS config |
| `internal/policy` | 96.3% | Unit | Rule evaluation, first-match-wins, `CheckLimits`, deny/allow precedence |
| `internal/orchestrator` | 59.5% | Integration | Policy limit wiring, scrubber, yield routing; `Run()` is E2E boundary |
| `internal/runtime` | 84.2% | Unit | venv.go, python.go, recording embed; Windows stub |
| `internal/tui` | 88.3% | Unit | Yield TUI model; no bubbletea snapshot tests yet |
| `internal/obs` | 100% | Unit | Metrics, redacting logger |
| `internal/audit` | 90.9% | Unit | Checkpoint NDJSON, signing, verify |
| `internal/monitor` | 95.7% | Unit | Monitor display |
| `cmd/staircase` | 22.4% | E2E | Workspace bootstrap, compile+sidecar, gate blocking/passing, secret lifecycle |
| `tests/conformance` | — | Conformance | IPC schema corpus (Go + Python) |
| **Total** | **76.6%** | | **538 tests · 0 failures · 0 skips** |

---

## §8 — Known Gaps & Future Work

| ID | Priority | Description |
|---|---|---|
| F-1 | **HIGH** | Go 1.26.1 has 6 active stdlib CVEs (govulncheck F-001–F-003). Upgrade to 1.26.3. |
| F-2 | Medium | IPC server does not validate raw JSON against `proto/ipc.v1.schema.json` before unmarshal (CHECK 3.5.5). `additionalProperties:false` not enforced at wire level. |
| F-3 | Medium | `exec.Command("git", ...)` shell-outs in orchestrator (3 sites). Replace with `go-git` library (CHECK 5.3.1). |
| F-4 | Medium | `graph_exec_case*.py` written to `$STAIRCASE_DIR/tmp/` (CHECK 5.4.2/6.1.2). Architecture should deliver via inherited fd. Deferred. |
| F-5 | Medium | `staircase secret rotate` is not crash-atomic (CHECK 4.3.2): DB re-encrypt commits before key rename. Deferred. |
| F-6 | Medium | Secret rotation does not block active runs that hold the same key (CHECK 4.3.3). Deferred. |
| F-7 | Low | No `EventYieldDecided` audit event on operator/policy decision (CHECK 7.3.1). |
| F-8 | Low | `internal/tui` has no bubbletea snapshot tests (CHECK 7.4.1). |
| F-9 | Low | OTel tracing is a stub (`internal/obs/otel.go`); no real exporter wired (CHECK 10.3.1). |
| F-10 | Low | Windows TCP loopback IPC path has a smoke test but no full integration test (no Windows CI runner). |
| F-11 | Info | `spf13/viper` used solely for `STAIRCASE_DIR` lookup. Could be replaced with `os.Getenv`. |
| F-12 | Info | LangGraph pinned at `langgraph==0.2.45` in embedded `requirements.txt`. Review before production use. |

---

## §9 — Implementation Protocol (New Feature Checklist)

When adding a new feature to stAirCase, verify each item before marking the feature complete:

### Data Layer
- [ ] Schema change added to `schema.go` base DDL (`CREATE TABLE IF NOT EXISTS`)
- [ ] If column added to existing table: migration added to `Migrations` slice in `schema.go`
- [ ] New store method added with `%w` error wrapping throughout
- [ ] `RowsAffected() == 0` check on UPDATE/DELETE methods (consistent with `UpdateComponent`, `DeleteComponent`)
- [ ] If status field added: `CHECK(status IN (…))` constraint in DDL
- [ ] If unique constraint needed: partial UNIQUE index preferred over `UNIQUE` on nullable column
- [ ] Store tests cover: happy path, not-found → `nil, nil`, constraint violation, edge case

### Security
- [ ] No secrets passed via `cmd.Env`
- [ ] No secrets written to disk in plaintext
- [ ] Any new file written with sensitive content uses `os.CreateTemp` + `os.Rename` pattern with explicit `0600` mode
- [ ] Any new path handling uses `os.path.realpath` + root-prefix check (Python) or `filepath.Clean` + prefix check (Go)
- [ ] New IPC message type: server handler sends an error response on failure (no silent hang)

### IPC / Template
- [ ] New Python tool registered in `_BUILTIN_TOOLS`
- [ ] New Python tool includes path-traversal guard (realpath + root-prefix)
- [ ] New template field properly escaped via `pyStr` / `pyIdent` before insertion
- [ ] Template test asserts the new construct appears in generated output

### Quality Gates
- [ ] New business invariant → new gate (or extend existing gate)
- [ ] Gate registered via `Register(&myGate{})` in package `init()`
- [ ] Gate test covers: PASS case, FAIL case, edge/skip case
- [ ] If gate is BLOCK severity: E2E test verifies it blocks the gate report

### CLI Commands
- [ ] Command registered in `rootCmd.AddCommand(...)` in `init()`
- [ ] `RunE` returns descriptive `fmt.Errorf("context: %w", err)` errors
- [ ] E2E test exercises the handler function directly (whitebox via `package main`)
- [ ] `--dry-run` flag considered for any destructive command

### Test Completeness
- [ ] Unit tests cover happy path + error path + boundary values
- [ ] Integration test uses `t.TempDir()` for isolation (no shared state)
- [ ] No `time.Sleep` in tests; use channels/polling with `require.Eventually` if async
- [ ] `go test ./... -count=1 -timeout 120s` passes with 0 failures before merge

---

*Generated from source audit on branch `feature/SAC-5` · commit `post-5b-remediation`*
