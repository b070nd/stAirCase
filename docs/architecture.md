# stAirCase — Technical Review Document

**Version:** post-phase-4 (branch `feature/SAC-5`)
**Module:** `github.com/b070nd/staircase-core`
**Go version:** 1.26.1
**Date prepared:** 2026-03-25
**Purpose:** Independent deep review / audit

---

## 1. Executive Summary

stAirCase is a local CLI tool that orchestrates multi-agent LLM swarms (LangGraph/LangChain) against a user's source code repository. Its core design goals are:

- **Zero-trace isolation** — all state lives in `$STAIRCASE_DIR`, never touching the system Python or env vars
- **Human-in-the-loop (HITL) control** — every file edit proposed by an agent requires operator approval before it reaches disk
- **SOC2-grade audit trail** — a tamper-proof SHA-256 chain hash on all agent event logs
- **Blast-radius containment** — all mutations happen on an isolated git branch

The implementation spans ~3,200 lines of Go (production) and ~1,400 lines of test code across 6 packages. It compiles to a single static binary (`CGO_ENABLED=0` via `modernc.org/sqlite`).

**Overall assessment:** The architecture is coherent and the security model is well-considered. Several sharp edges exist in the IPC protocol, the secret-handling lifecycle, and the Python code generation pipeline that a reviewer should scrutinise closely.

---

## 2. Architecture Overview

```
┌────────────────────────────────────────────────────────────────┐
│  staircase CLI (cmd/staircase)                                 │
│  run / compile / gate / init / inspect / clean / ...           │
└───────────────┬────────────────────────────────────────────────┘
                │
        ┌───────▼────────┐        ┌──────────────────────┐
        │  persistence   │        │  template/codegen    │
        │  SQLite + WAL  │        │  graph_exec_caseN.py │
        └───────┬────────┘        └──────────────────────┘
                │
        ┌───────▼────────┐
        │  runtime       │        Bootstrap sequence:
        │  venv + python │─────►  stdin → JSON token → socket → UDS
        └───────┬────────┘
                │  Unix Domain Socket (JSON-Lines)
        ┌───────▼────────┐
        │  ipc/server    │──── SOC2 event log ──► SQLite
        │  auth/yield/   │──── HITL yield ──────► tui/yield (operator)
        │  secret/hb     │──── secret fetch ────► crypto + SQLite
        └────────────────┘
```

### Key data flows

1. `staircase run <case-id>` resolves the git branch, runs pre-flight gates, calls `staircase compile` to render `graph_exec_caseN.py`, starts the UDS server, launches Python, and then enters the HITL loop.
2. Python connects to the UDS, authenticates with the single-use token, and begins invoking LangGraph nodes.
3. Each node emits `state_emit` messages (written to the event log) and `yield_request` messages (blocked until the operator approves via the TUI).
4. On success the Go side commits the changes, calls `UpdateRunStatus`, and the defer restores the original git branch on any non-SUCCESS outcome.

---

## 3. Module-by-Module Analysis

### 3.1 `internal/domain` — Data Model

**File:** `models.go`

The domain package is a clean set of plain-Go structs with no logic. All fields are exported with JSON tags.

**Observations:**

| Issue | Detail |
|---|---|
| Status fields are untyped `string` | `Case.Status`, `Run.Status`, `UserStory.Status` use raw strings. No `const` set enforces valid values. An invalid string (e.g. `"COMPLTEED"`) silently persists. |
| `Run.Status` comment says `KILLED` | The persistence layer only ever writes `RUNNING`, `SUCCESS`, `FAILED`. `KILLED` is documented but never set. |
| No `UpdatedAt` on `Project` | Cannot determine when a project's `source_path` last changed — relevant for audit. |
| `Secret.EncryptedValue` naming | The field name implies the caller has already encrypted the value. The CLI does encrypt before storing, but the store itself accepts and stores whatever string it receives with no validation. A bug in the caller silently stores plaintext. |

---

### 3.2 `internal/persistence` — Storage Layer

**Files:** `db.go`, `schema.go`, `store.go`, `store_phase4.go`

SQLite via `modernc.org/sqlite` (pure-Go). WAL mode, `busy_timeout=5s`, foreign keys enforced.

#### Schema observations

```sql
-- secrets table has NO unique constraint on (key_name, scoped_to_project_id)
CREATE TABLE IF NOT EXISTS secrets (
    key_name TEXT NOT NULL,
    encrypted_value TEXT NOT NULL,
    scoped_to_project_id INTEGER,
    ...
);
```

**Issue S-1 (Medium):** Duplicate secrets are silently allowed at the DB level. The only guard is the `secret.no_duplicate_keys` quality gate (advisory warn, not a DB constraint). `GetSecret` uses `LIMIT 1` implicitly via `QueryRow` — the first row wins, but which row that is depends on SQLite's internal rowid order, not insertion order. This is non-deterministic.

**Recommended fix:** `UNIQUE(key_name, scoped_to_project_id)` or `UNIQUE(key_name) WHERE scoped_to_project_id IS NULL` + `UNIQUE(key_name, scoped_to_project_id) WHERE scoped_to_project_id IS NOT NULL` (SQLite partial index).

```sql
-- run_event_logs has no index on (run_id, id DESC) for GetLastEventHash
SELECT event_hash FROM run_event_logs WHERE run_id = ? ORDER BY id DESC LIMIT 1
```

**Issue S-2 (Low):** `GetLastEventHash` is called on every `AppendEventLog`. The index `idx_run_event_logs_run` covers `(run_id)` but not the `ORDER BY id DESC`. For high-frequency agent telemetry this becomes a full-scan over the filtered set. Adding a composite `(run_id, id DESC)` index would make this O(1).

#### Store method observations

- All reads use `QueryRow.Scan` with explicit `sql.ErrNoRows` → `nil, nil` returns. Consistent and correct.
- `CreateRun` does not validate that `topologyVersion` matches an existing topology record. A mismatched version is stored without error.
- `AppendEventLog` truncation of the payload happens in the IPC server (`maxPayloadSize = 64KiB`), not here. The store itself will accept arbitrarily large payloads if called directly — no defence-in-depth.
- `UpdateComponent` correctly checks `RowsAffected() == 0` and returns an error. `DeleteComponent` silently succeeds even for a non-existent ID — inconsistent behaviour.

---

### 3.3 `internal/crypto` — Encryption

**File:** `encrypt.go`

AES-256-GCM with a random 12-byte nonce prepended to ciphertext, then base64-encoded. The pattern is correct and follows best practice.

**Observations:**

| Issue | Detail |
|---|---|
| Key file permissions checked at gate time, not at load time | `LoadKey` reads `.key` with no permission check. If permissions were widened to `0644` after init, `LoadKey` succeeds silently. The `secret.key_file` gate catches this, but only if the gate is run. |
| No key rotation mechanism | Rotating the AES key requires re-encrypting all secrets. There is no tooling for this. |
| Key material held as `[]byte` in memory | The key is loaded fresh per command invocation and not zeroed after use. This is a minor risk (core dump, swap), acceptable for a local CLI tool. |
| `GenerateKey` uses `os.WriteFile` | `WriteFile` creates the file and writes atomically on most UNIX platforms, but not guaranteed. `tempfile + rename` is safer to avoid a partial write at `.key`. |

---

### 3.4 `internal/ipc` — Unix Domain Socket Server

**Files:** `schemas.go`, `server.go`

The IPC server is the most security-critical component. It is the only pathway for the Python process to interact with secrets, storage, and the operator.

**Positive observations:**
- `crypto/subtle.ConstantTimeCompare` for token comparison (timing side-channel safe)
- `authTimeout = 10s` on new connections
- `heartbeatTimeout = 30s` read deadline (prevents zombie connections)
- `maxPayloadSize = 64KiB` before `AppendEventLog` (DB inflation guard)
- `ctx.Done()` guards on both sides of yield channel send/receive (deadlock prevention)
- `scanner.Buffer(make([]byte, 1MiB), 1MiB)` — bounded read buffer

**Issues:**

**Issue I-1 (High):** The socket file is created at a path inside `$STAIRCASE_DIR/tmp/`. If `$STAIRCASE_DIR` is world-readable (e.g. `/tmp/staircase`), any local user can connect to the UDS and attempt the auth handshake. The 32-byte token provides 256 bits of entropy, making brute-force infeasible, but the surface exists. The socket should be created with `0600` mode.

```go
// Current: no mode set on the socket
ln, err := net.Listen("unix", s.socketPath)
```

**Recommended fix:**
```go
os.Remove(s.socketPath)
ln, err := net.Listen("unix", s.socketPath)
if err == nil {
    os.Chmod(s.socketPath, 0600)
}
```

**Issue I-2 (Medium):** There is no rate limit or connection count limit on the UDS server. A malicious or buggy Python process could open thousands of connections consuming file descriptors. The server should reject connections beyond 1 (the expected Python process).

**Issue I-3 (Medium):** The `secret_request` handler returns `encrypted_value` — the *encrypted* blob from the database — directly to Python. Python is expected to know the AES key to decrypt it, but there is no evidence in the Python template that decryption happens. Reviewing the generated `graph_exec.py` template: `_ipc.get_secret(...)` simply returns `resp.get("encrypted_value")` and passes it directly as `api_key` to `ChatAnthropic`. This means the secret is passed encrypted to the Anthropic SDK, which will fail. **This is likely a functional bug.**

**Issue I-4 (Low):** The `IpcSecretResponse.Error` field is defined in the schema but never populated by the server. The handler either returns a secret or returns nothing (no message sent). Python will hang if a secret lookup fails.

---

### 3.5 `internal/runtime` — Python Process Management

**Files:** `python.go`, `venv.go`

**Positive observations:**
- Bootstrap token passed over stdin (not env vars — `/proc` leak prevention)
- `context.WithCancel` ensures SIGKILL propagates to child processes
- `stdin.Close()` seals the bootstrap channel immediately after message delivery
- SHA-256 hash of embedded `requirements.txt` stored in `venv/.requirements_hash` for automatic venv update without full recreation

**Issues:**

**Issue R-1 (Medium):** `cmd.Stderr = nil` (line 71) means Python stderr is inherited from the Go process. This is intentional ("so Python tracebacks surface in the terminal") but has a side effect: if Python prints sensitive debug output to stderr, it appears in the terminal. A dedicated stderr capture with selective display would be safer.

**Issue R-2 (Low):** The Rosetta check (macOS arm64 vs x86_64 Python) compares `runtime.GOARCH == "arm64"` and `platform.machine() == "x86_64"`. However `platform.machine()` returns the CPU architecture of the running interpreter, not the Python binary. On a native arm64 machine with a Rosetta Python, this correctly detects the mismatch. The check is correct but narrowly tested.

**Issue R-3 (Low):** `runPipInstall` writes `requirements.txt` to `$STAIRCASE_DIR/tmp/requirements.txt`. If `tmp/` does not exist, `os.MkdirAll` creates it. However, if `pip install` fails midway, the hash file is NOT updated (correct), but the partially-installed venv is not cleaned up. Subsequent `staircase init` calls will attempt to re-run `pip install` into a potentially broken venv.

---

### 3.6 `internal/template` — Python Code Generation

**File:** `codegen.go`

The template uses `[[ ]]` delimiters (not `{{ }}`) to avoid conflict with Python's dict/format syntax. The template is 218 lines of embedded Python rendered to `graph_exec_caseN.py`.

**Positive observations:**
- `pyStr`: escapes `\`, `"`, `\r`, `\n`, `\t` — correct for Python string literals
- `pyIdent`: maps hyphens/spaces → `_`, prefixes leading digits with `_`, appends `_` to Python keywords (full 38-keyword set)
- `buildConditionalGroups`: correctly pre-computes routing groups and END sentinels
- `OutgoingConditions` pre-populated per agent for conditional ROUTE: injection
- `pylist` function avoids `[[[range` template parse ambiguity

**Issues:**

**Issue T-1 (High — functional):** See Issue I-3. The generated Python calls `_ipc.get_secret("ANTHROPIC_API_KEY")` and passes the result directly to `ChatAnthropic(api_key=...)`. The IPC server returns the *encrypted* base64 value. The Anthropic SDK will reject this. Either:
  - The IPC server should decrypt before returning (preferred — key never leaves Go), or
  - The Python template should include decryption logic using the AES key (the key would need to be bootstrapped to Python, which conflicts with the zero-trace model)
  - **Preferred resolution:** Decrypt in the IPC server `secret_request` handler before sending the response.

**Issue T-2 (Medium):** The generated script uses `sys.stdin.readline()` for the bootstrap message, then immediately connects to the UDS. There is no timeout on `sys.stdin.readline()`. If Go fails to write the bootstrap message (e.g. broken pipe), Python blocks indefinitely.

**Issue T-3 (Low):** The `_WORKER_NAMES` Python list is generated from `pylist(Agents)` which uses the raw agent names (not `pyIdent`-sanitised). If an agent has a name containing a `"` or `\`, the list literal would be malformed. The `pylist` function should call `pyStr(a.Name)` (which it does) — this is actually correct. **Not a bug.**

**Issue T-4 (Low):** The `request_edit` tool writes a tempfile via `tempfile.mkstemp` and replaces the target with `os.replace`. This is correct for atomic writes. However the `search_block` is matched using exact string comparison (`content.find(search_block)`). If the file uses CRLF line endings (e.g. cloned on Windows), matches will fail silently.

---

### 3.7 `internal/engine` — DAG and Repo Map

**Files:** `dag.go`, `skeleton.go`

**DAG (Kahn's Algorithm):**
- Correctly skips edges where source OR target is outside the project set (external deps are ignored)
- Cycle detection with named project diagnostics
- No sort stability guaranteed (queue seeds from `map` iteration order) — output order may vary between runs for equal-depth nodes

**RepoMap:**
- Respects `.gitignore` and `.staircaseignore` patterns via filepath.Match
- Correctly skips `node_modules`, `.git`, `__pycache__`, `venv`, etc.
- Signature extraction covers Go, Python, TypeScript/JavaScript
- Signatures truncated at 120 chars + `…`

**Issues:**

**Issue E-1 (Low):** `loadIgnorePatterns` silently swallows all errors (returns `patterns, nil` even on failure). A corrupt `.gitignore` would result in no ignore patterns being loaded, potentially including large directories in the repo map.

**Issue E-2 (Low):** `shouldIgnoreFile` uses `filepath.Match` against both the base filename and the relative path. `filepath.Match` does not support `**` glob syntax. Patterns like `src/**/*.test.ts` in `.gitignore` will not match. This is a known limitation of Go's `filepath.Match`.

---

### 3.8 `internal/gate` — Quality Gate System

**Files:** `gate.go`, `structural.go`, `security.go`, `runtime.go`, `dependency.go`

17 gates across 4 categories registered via `init()`. Extensible via `Register(&myGate{})`.

**Overall design is solid.** The `export_test.go` / `package gate_test` pattern correctly separates test concerns.

**Issues:**

**Issue G-1 (Medium):** The `runtime.script_compiled` gate checks for `graph_exec_caseN.py` in `tmp/`. However `staircase run` also calls `compile` internally before each run. The gate is therefore only meaningful when run standalone via `staircase gate`. When used as a pre-flight check inside `run.go`, the script will have just been generated, making the gate trivially pass. The gate could be more useful if it checked the script's modification time against the topology's last-known change.

**Issue G-2 (Low):** The `deps.deps_completed` gate warns if an upstream project has no COMPLETED case. However it checks `ListCasesByProject` for any case with `status == "COMPLETED"`, without verifying it was completed against the *current* topology version. A project whose topology changed after its last successful run would still be marked as satisfied.

**Issue G-3 (Low):** Gate results are not persisted. Each `staircase gate` invocation re-runs all gates from scratch. For long-running pipelines with many projects, the dependency gates could be slow (O(N) DB queries). Caching last-good gate results in the DB would improve this.

---

### 3.9 `cmd/staircase` — CLI Layer

**Files:** `run.go`, `compile.go`, `gate.go`, `component.go`, `init.go`, `clean.go`, `project.go`, `secret.go`, `topology.go`, `case.go`, `story.go`, `vendor.go`, `inspect.go`, `doctor.go`, `helpers.go`

**Positive observations:**
- `run.go` uses pessimistic `finalStatus := ""` with `defer` to restore the original git branch on non-SUCCESS
- `--skip-gates` flag allows bypassing pre-flight for CI/CD scenarios
- `--dry-run` flag for compile-only validation
- `clean.go` uses `os.Lstat` + `ModeSymlink` check before `os.RemoveAll` (symlink escape prevention)
- `project.go` `--source` validates both `filepath.IsAbs` and `os.Stat`
- `secret.go` encrypts before store, decrypts on display

**Issues:**

**Issue C-1 (Medium):** `run.go` creates the git branch (`git checkout -b staircase/run-N`), then starts the IPC server, then compiles the script, then launches Python. If IPC start or compile fails *after* the branch is created, the defer correctly restores the original branch. However `git checkout -b` will fail if a branch with that name already exists (e.g. a crash left a stale branch). There is no `git branch -D staircase/run-N` before checkout.

**Issue C-2 (Medium):** `compile.go` passes `RepoContext` to the template by calling `engine.RepoMap(project.SourcePath)`. If `SourcePath` is not set, this is called with an empty string and `filepath.WalkDir("")` walks the *current working directory* of the `staircase` process. This is unexpected and potentially large.

**Issue C-3 (Low):** `secret.go` stores `encrypted_value` as a raw base64 string. There is no prefix/version byte to indicate the encryption scheme. If the encryption algorithm is changed in a future version, there is no way to distinguish old ciphertexts from new ones.

**Issue C-4 (Low):** The `inspect` command lists event logs including the full `payload` JSON. Payloads may contain partial source code or agent reasoning traces. There is no truncation or redaction in the output.

---

## 4. Security Model Summary

| Control | Status | Notes |
|---|---|---|
| AES-256-GCM for secrets at rest | ✅ Implemented | Key at `.key` (0600) |
| Timing-safe token comparison | ✅ Implemented | `crypto/subtle.ConstantTimeCompare` |
| Bootstrap token via stdin only | ✅ Implemented | Never via env vars |
| UDS socket permissions | ⚠️ Not set | Socket inherits umask; should be 0600 |
| Secret decryption before Python delivery | ❌ Missing | Python receives encrypted value — likely breaks LLM calls |
| Path traversal in `read_file` tool | ✅ Implemented | `os.path.realpath` + root-prefix check |
| Path traversal in `request_edit` tool | ✅ Implemented | Same pattern |
| IPC connection limit | ⚠️ Missing | Unbounded connections from Python |
| Payload size cap | ✅ Implemented | 64 KiB before `AppendEventLog` |
| Blast-radius branch isolation | ✅ Implemented | `git checkout -b staircase/run-N` |
| Branch restore on failure | ✅ Implemented | Pessimistic defer |
| SOC2 tamper-proof event log | ✅ Implemented | SHA-256 chain hash |
| Duplicate secrets unique constraint | ⚠️ DB-level missing | Only advisory gate |
| Symlink escape in clean | ✅ Implemented | `os.Lstat + ModeSymlink` |

---

## 5. Data Model Integrity

### Entity Relationship (simplified)

```
vendors ──< projects ──< cases ──< user_stories
               │              └──< runs ──< run_event_logs
               │
               ├──< components
               ├──< project_dependencies (self-ref)
               └──< swarm_topologies ──< agent_nodes ──< agent_tools
                                     └──< edges

secrets (global or scoped to project)
```

### Cascade behaviour

All foreign keys use `ON DELETE CASCADE`. This means:
- Deleting a vendor deletes all projects, cases, runs, and event logs
- Deleting a project deletes its topology history and all run traces
- This is appropriate for a local developer tool but would be inappropriate in a multi-tenant service

### Missing constraints

| Table | Missing constraint |
|---|---|
| `secrets` | `UNIQUE(key_name, scoped_to_project_id)` |
| `runs` | No FK to `swarm_topologies(id)` — `topology_version` is a loose integer |
| `case.status` | No CHECK constraint (`CHECK(status IN ('PENDING','RUNNING','COMPLETED','FAILED'))`) |
| `run.status` | No CHECK constraint |
| `user_story.status` | No CHECK constraint |

---

## 6. Test Coverage Assessment

### Coverage by package

| Package | Test file | Type | Count | Notes |
|---|---|---|---|---|
| `crypto` | `encrypt_test.go` | unit | 11 | Full roundtrip, tamper, key mgmt |
| `engine` | `dag_test.go` | unit | 11 | All cycle/sort cases |
| `engine` | `skeleton_test.go` | unit | 15 | RepoMap, PackXML, sigs |
| `persistence` | `store_test.go` | integration | 20 | Real SQLite in t.TempDir |
| `template` | `codegen_test.go` | unit | 25+ | pyStr, pyIdent, GenerateGraphExec |
| `gate` | `gate_test.go` | integration | 55+ | All 17 gates |

### What is not tested

| Area | Risk |
|---|---|
| `ipc/server.go` | No tests. The entire auth/yield/secret/heartbeat loop is untested. |
| `runtime/python.go` | No tests. Bootstrap sequence, process lifecycle untested. |
| `runtime/venv.go` | No tests. Hash comparison, pip install path untested. |
| `cmd/staircase/*` | No tests. All CLI commands untested (no functional/E2E tests). |
| `tui/yield.go` | No tests. HITL operator loop untested. |
| Concurrent IPC connections | No tests. Race condition on `YieldCh`/`ResponseCh` untested. |
| Git operations in `run.go` | No tests. Branch create/restore logic untested. |

### Test architecture

All test packages correctly use `package foo_test` (black-box). Two packages use the `export_test.go` pattern to expose internals without polluting the production API:
- `gate/export_test.go` — exposes 17 gate singletons + `ReplaceRegistry`
- `template/export_test.go` — exposes `PyStr`, `PyIdent`, `BuildConditionalGroups`

---

## 7. Dependency Analysis

```
go 1.26.1

Direct:
  charmbracelet/bubbletea   v1.3.10    TUI framework
  charmbracelet/glamour     v1.0.0     Markdown rendering
  charmbracelet/lipgloss    v1.1.1     TUI styling
  spf13/cobra               v1.10.2    CLI framework
  spf13/viper               v1.20.1    Config management
  stretchr/testify          v1.10.0    Test assertions
  modernc.org/sqlite        v1.47.0    Pure-Go SQLite (CGO_ENABLED=0)
```

**Observations:**
- `modernc.org/sqlite` enables fully static builds. Correct choice for a developer CLI.
- `spf13/viper` is used only for `STAIRCASE_DIR` environment variable lookup. This is a heavyweight dependency for a single config value. `os.Getenv` would suffice.
- No direct HTTP client dependencies — the system does not make outbound HTTP calls (all Anthropic API calls originate from the Python process). This is correct.
- LangGraph version is pinned at `langgraph==0.2.45` in the embedded `requirements.txt`. LangGraph has had breaking API changes between minor versions. This pin should be reviewed against the current stable release.

---

## 8. Open Issues (Priority Order)

| ID | Severity | Category | Description |
|---|---|---|---|
| I-3 / T-1 | **Critical** | Security/Functional | IPC server returns **encrypted** secret value to Python. Python passes it directly as API key — likely breaks all LLM calls. Decrypt in Go before sending. |
| S-1 | High | Data integrity | No unique constraint on `secrets(key_name, scoped_to_project_id)`. Deterministic resolution not guaranteed. |
| I-1 | High | Security | UDS socket file lacks explicit 0600 mode. Any local user can attempt auth handshake. |
| C-2 | Medium | Correctness | `compile.go` calls `engine.RepoMap("")` if `SourcePath` is unset — walks process CWD. |
| C-1 | Medium | Correctness | `run.go` does not delete a stale `staircase/run-N` branch before creating it. Crashes leave stale branches that block re-run. |
| I-2 | Medium | Security | No connection limit on IPC UDS server. |
| I-4 | Medium | Correctness | `secret_request` failure sends no response to Python — Python hangs. |
| R-3 | Medium | Reliability | Partial pip install failure leaves a broken venv with no cleanup. |
| T-2 | Medium | Correctness | Python `sys.stdin.readline()` has no timeout — blocks indefinitely if bootstrap fails. |
| G-1 | Low | Design | `runtime.script_compiled` gate is trivially satisfied when called from `run.go` (script was just generated). |
| E-2 | Low | Correctness | `filepath.Match` does not support `**` glob — `.gitignore` patterns with `**` are silently ignored. |
| C-3 | Low | Maintainability | No version byte on encrypted secrets — future algorithm migration would be ambiguous. |

---

## 9. Recommendations

### Immediate (before first external use)

1. **Fix I-3/T-1:** Decrypt secrets in the IPC server before sending to Python. The AES key must never leave the Go process.

2. **Fix S-1:** Add a SQLite partial unique index on secrets:
   ```sql
   CREATE UNIQUE INDEX IF NOT EXISTS uidx_secrets_global
     ON secrets(key_name) WHERE scoped_to_project_id IS NULL;
   CREATE UNIQUE INDEX IF NOT EXISTS uidx_secrets_project
     ON secrets(key_name, scoped_to_project_id) WHERE scoped_to_project_id IS NOT NULL;
   ```

3. **Fix I-1:** Set UDS socket mode to 0600 after `net.Listen`.

4. **Fix C-2:** Guard `engine.RepoMap` call behind a `SourcePath != ""` check.

### Short-term

5. **Test the IPC server.** This is the highest-risk untested code. A table-driven test using `net.Dial("unix", ...)` against a real `Server` instance would cover auth, yield, secret, heartbeat, and malformed messages.

6. **Fix C-1:** Before `git checkout -b staircase/run-N`, check if the branch exists with `git rev-parse --verify` and delete it if it does (or fail with a clear error).

7. **Fix I-4:** Return an error response for failed secret lookups instead of silence.

8. **Add CHECK constraints** to `cases.status`, `runs.status`, `user_stories.status` at the schema level.

### Longer-term

9. **Schema migrations.** The current approach applies `CREATE TABLE IF NOT EXISTS` on every startup. This works for the initial schema but provides no path for additive migrations (new columns, indexes). Consider `golang-migrate` or a simple version table.

10. **Venv repair.** On pip install failure, either delete and recreate the venv, or mark it as dirty with a `.broken` sentinel file so the next `staircase init` knows to recreate it.

11. **`**` glob support in RepoMap.** Implement recursive glob matching or use a library like `github.com/bmatcuk/doublestar`.

12. **Functional tests.** The `tests/` directory contains only `integration.bats` (empty). Adding BATS-based functional tests for the core workflows (`init → compile → gate → run`) would provide confidence in the end-to-end path.

---

## 10. Code Quality Observations

**Positive:**
- Consistent error wrapping with `%w` throughout the persistence layer
- `defer rows.Close()` and `rows.Err()` checks everywhere — no resource leaks
- Template functions (`pyStr`, `pyIdent`) are pure functions with comprehensive test coverage
- Security-sensitive paths (symlink escape, path traversal, token comparison) show deliberate hardening
- The `export_test.go` / black-box test pattern is clean and idiomatic

**Needs attention:**
- `store.go` and `store_phase4.go` are split by development phase rather than semantic concern. The split creates a confusing two-file store with no clear boundary.
- Several CLI commands (`compile.go`, `run.go`) are 200–300 lines. Extracting sub-operations into named functions would improve readability.
- Emoji output (`📦`, `✅`, `🚀`) in `venv.go` will produce garbage on terminals that don't support Unicode. A `--no-color`/`--plain` flag or `isatty` check would help.

---

*Document generated from source at commit `1438446` on branch `feature/SAC-5`.*
*Reviewer should verify issues against the live source as development continues.*
