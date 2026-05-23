# stAirCase — Comprehensive Functional Test Plan

**Scope:** Beyond unit tests. This plan covers everything needed to claim stAirCase works end-to-end, under adversity, in the hands of users who are not the authors.

**Audience:** engineer implementing tests, release manager deciding whether to ship, security reviewer deciding whether to sign off.

**Companion documents:** `STAIRCASE_PLAN.md` (what we're building), `AUDIT_CHECKLIST.md` (what we check on every PR). This document covers what we do **before declaring a release ready**.

---

## 0. Philosophy

Five principles. If a proposed test does not serve at least one of them, drop it.

1. **Test the system, not the code.** Unit tests cover code. This plan covers behavior. A passing unit test suite and a failing scenario test mean the system does not work, regardless of what the coverage number says.

2. **Every test has a failure mode it prevents.** Before writing a test, state: "this test catches X class of bug." If you cannot state it, the test is decoration. Document the class of bug in the test's header comment.

3. **Determinism by default, nondeterminism when explicit.** Every test that can be deterministic must be. Tests that are necessarily probabilistic (soak, chaos) are run in isolation with seeds recorded. No test in the standard suite is "sometimes flaky."

4. **Fakes over mocks.** A fake LLM that takes recorded conversations and replays them is better than a mock that returns whatever the test author thinks is realistic. The fake forces us to record real behavior and notice when it changes.

5. **A test that has never failed is suspect.** If a test has never caught a regression, either the category of bug does not happen, or the test does not actually cover it. Audit tests that have been green for 12+ months; they may be documentation, not verification.

## 0.1 Out of scope for this plan

- Performance testing against commercial LLM providers (cost-prohibitive at CI scale; see §8 soak tests which use recorded fixtures).
- Security pentesting (a separate, out-of-band engagement with a third party; this plan supports it with scenario fixtures but does not replace it).
- Multi-tenant / multi-user scenarios (Principle 9 of the build plan: single-user product).

The following capabilities are explicitly deferred to future roadmap phases and are **not** tested or implemented in the current prototype:

| Capability | Priority | Rationale |
|------------|----------|-----------|
| Multi-user RBAC | P3 | Requires identity model + tenant-scoped DB queries; single-user product by design |
| SSO / identity provider integration | P3 | No auth layer in v0.1; SAML/OIDC requires external provider and session model |
| Multi-tenant workspace isolation | P3 | Single workspace per binary invocation; tenant_id would require full schema migration |
| Server mode / HTTP API | P3 | CLI-first; `runner.Run()` is already an API-callable library; server shell requires job queue, WS streaming, concurrent git isolation |
| Remote runners | P3 | Requires bidirectional agent protocol (gRPC/WS), runner registration, heartbeat/failover; local-only execution by design |
| MCP protocol server | P3 | No MCP references in codebase; full MCP SDK not yet evaluated; gates.json signing (implemented) is the current tool-trust boundary |
| Central control plane / fleet management | P3 | Distributed architecture not yet designed |
| SIEM webhook / audit export API | P3 | Audit evidence available via `staircase audit export`; push integration deferred |
| KMS/OS-keychain vault backend | P2-deferred | `.key` file at 0600 in 0700 workspace adequate for single-user; keychain requires per-OS dep |
| CEL/OPA policy expressions | P2-deferred | Struct-based rules cover common cases; full CEL deferred to Phase 8 (documented in policy package) |
| Stable public SDK contract | P3 | Python runner API is internal; breaking changes allowed until v1.0 |

**What "P3 not implemented" means operationally**: The current architecture is a well-designed foundation. Each P3 item has a clear extension point in the codebase (e.g., `runner.Run()` for server mode, `policy.Engine` for CEL, signing infrastructure for MCP), but none were built speculatively — they require multi-month design commitments and external infrastructure decisions before code is warranted.

Enterprise reviewers should treat these as known scope boundaries, not oversights.

---

## 1. The testing pyramid (what each layer is for)

```
                   ┌──────────────────────────┐
                   │   L8: User-in-the-loop   │  Humans + recorded runs; qualitative
                   ├──────────────────────────┤
                   │   L7: Adversarial        │  Hostile inputs, red-team scenarios
                   ├──────────────────────────┤
                   │   L6: Soak               │  Long-duration stability
                   ├──────────────────────────┤
                   │   L5: Chaos              │  Fault injection, crashes, disk full
                   ├──────────────────────────┤
                   │   L4: Scenario (E2E)     │  Named user journeys, real binary
                   ├──────────────────────────┤
                   │   L3: Integration        │  Real Go + fake Python (or vice versa)
                   ├──────────────────────────┤
                   │   L2: Component          │  One package, real collaborators
                   ├──────────────────────────┤
                   │   L1: Contract           │  Protocol conformance, schema validity
                   └──────────────────────────┘
```

The pyramid is not just about test count. It's about **oracle** — what "correct" means at each level — and **what kinds of bugs each level catches**.

| Layer | Oracle (what "correct" means) | Catches | Runtime | Runs where |
|---|---|---|---|---|
| L1 Contract | Schema + corpus | Protocol drift | ~5s | Every PR |
| L2 Component | Asserts per package | Logic bugs | ~30s | Every PR |
| L3 Integration | Multi-package invariants | Wiring bugs | ~2m | Every PR |
| L4 Scenario | End-to-end user journey passes | System bugs | ~10m | Every PR |
| L5 Chaos | System survives induced faults | Reliability bugs | ~30m | Nightly + release |
| L6 Soak | No degradation over N hours | Leaks, drift | 2–24h | Weekly + release |
| L7 Adversarial | Hostile inputs don't compromise invariants | Security bugs | ~1h | Release + quarterly |
| L8 User-in-the-loop | Real operator completes real task | UX bugs | 1 day | Pre-major-release |

Unit tests are **L1+L2 combined** in this taxonomy. The audit checklist from the prior document covers L1, L2, and the existence-of-L3+ checks. This plan starts where that ends.

---

## 2. Fixtures, fakes, and test harness

Before any tests, we need the materials. These are load-bearing; bad fixtures produce bad tests.

### 2.1 Fixture repositories

A test suite that runs against a single fixture repo learns to pass that repo. We need variety on the axes that matter.

Located in `tests/fixtures/repos/`, each a real git repository committed as a tarball + replay script (git does not play nicely as submodules of test fixtures).

| Fixture | Size | Language mix | Notable property |
|---|---|---|---|
| `tiny-go` | 200 LoC, 5 files | Go only | Smallest sensible repo; smoke test |
| `monorepo-small` | 10k LoC | Go + TS | Multi-package Go module + frontend |
| `legacy-python` | 40k LoC | Python 3.8 compat code | Old-style code, mixed tabs/spaces, no type hints |
| `crlf-windows` | 1k LoC | Mixed | CRLF line endings, Windows paths in `.gitattributes` |
| `submodules` | 2k LoC | Go | Has git submodules; tests don't crash on them |
| `huge-binary` | 50MB | Various | Binary files in tree; RepoMap must skip them |
| `unicode-paths` | 500 LoC | Various | Filenames with CJK, emoji, combining chars |
| `detached-head` | 1k LoC | Go | Checked out at SHA, not on a branch |
| `dirty-worktree` | 1k LoC | Go | Has uncommitted changes; pre-flight must refuse |
| `empty-repo` | 0 files | — | `git init` with no commits; edge case |
| `case-sensitive-conflict` | 100 LoC | Mixed | Has `README.md` and `readme.md` (macOS foot-gun) |
| `hostile` | 500 LoC | Go | Filenames with `..`, symlinks out of tree, ignore-bypass attempts |

**Construction rule:** each fixture is built by a shell script `tests/fixtures/repos/build.sh <name>` that is reproducible from a `SOURCE_DATE_EPOCH`. Produces a tarball; tests extract into `t.TempDir()`. No network during construction.

### 2.2 Topology fixtures

Located at `tests/fixtures/topologies/`.

| Topology | Agents | Edges | Purpose |
|---|---|---|---|
| `single-agent` | 1 | 0 | Minimum viable |
| `linear-3` | 3 | planner→coder→reviewer | Common pattern |
| `diamond-4` | 4 | 1→2, 1→3, 2→4, 3→4 | Parallel branches |
| `cycle` | 3 | 1→2→3→1 | Must be rejected at compile |
| `disconnected` | 4 | two disjoint pairs | DAG builder edge case |
| `self-loop` | 1 | 1→1 | Must be rejected |
| `wide-fan-out` | 20 | 1 fans to 19 | Stress many-worker |
| `deep-chain` | 10 | linear | Depth limits |
| `missing-agent` | 2 | edge references agent 3 | Invalid; must reject |

### 2.3 Recorded LLM conversations (the fake LLM)

The single most important fixture. Without it, no deterministic E2E.

Located at `tests/fixtures/llm/`. Each file is a JSON record of a real conversation, replayable:

```json
{
  "id": "refactor-billing-2026-03-15",
  "model": "claude-opus-4-5",
  "topology": "linear-3",
  "exchanges": [
    {
      "prompt_hash": "sha256:...",
      "prompt_preview": "You are a code planner...",
      "response": {
        "role": "assistant",
        "content": [
          { "type": "text", "text": "..." },
          { "type": "tool_use", "id": "tu_1", "name": "read_file", "input": {...}}
        ]
      },
      "tokens_in": 1247, "tokens_out": 421, "latency_ms": 3200
    }
  ]
}
```

**Recording mode:** A special run flag `--record-llm <file>` writes this file from a real run. Subsequent test runs use `--replay-llm <file>` and the fake LLM client in the Python harness:

1. Looks up the exchange by hashing the prompt.
2. If found, returns the response.
3. If not found, fails loudly with a diff showing what prompt was sent vs. what was expected.

This last property is critical: it means **a test fails when the agent's behavior drifts**, which is usually what you want to know.

**Maintenance:** Recordings are regenerated intentionally when prompts change. A `make rerecord-all` target exists; running it requires a real API key and explicit opt-in (CI never regenerates).

### 2.4 Fake operator

For L3–L7, we need an operator that is not a human. `tests/harness/fake_operator.go`:

```go
type FakeOperator struct {
    Strategy DecisionStrategy  // ApproveAll, RejectAll, Pattern, Scripted
    LatencyMS int              // synthetic think time
    Script []Decision          // for Scripted: pre-determined decisions
    Notes []string             // captured yield notes for assertion
}
```

Strategies:
- `ApproveAll` — baseline happy path
- `RejectAll` — orchestrator handles rejections cleanly
- `Pattern(rx)` — approve if payload matches regex, else reject
- `Scripted` — nth decision is `Script[n]`; panic if exceeded
- `Idle` — never decides; used to test timeouts
- `Slow(d)` — decides after `d`; used to test batching and UI latency

### 2.5 Recording and replay harness

`tests/harness/recorder/` implements:
- Fixture repo setup/teardown in temp dirs
- Fake LLM server (HTTP) on a random port, loaded with a recording
- Fake operator wired to the orchestrator's approval interface
- Structured event capture: every IPC message, every audit event, every git operation
- Deterministic clock injection (for audit timestamps)

The output of a harness run is a directory structure that can itself be diffed between runs. Two runs with the same inputs produce byte-identical outputs, modulo explicitly-tagged nondeterministic fields (timestamps, IDs).

---

## 3. Layer 1 — Contract tests

**Goal:** the IPC schema and its generated bindings agree with each other and with a fixed corpus.

Mostly covered by §3.3 of the audit checklist. Summarized here for completeness.

- Corpus has ≥ 1 valid case per message kind, ≥ 3 invalid cases per kind (missing field, wrong type, unknown field).
- Go decoder + Python decoder agree on valid cases (byte-identical re-encoding after round trip).
- Both decoders reject all invalid cases with a typed error.
- Schema compiles with a draft-2020-12 validator.
- Fuzzing: random bytes to the validator; no panic, always a typed error; 10 minutes of CPU per release.

**Exit criteria for L1:** all corpus cases pass in both languages; 0 fuzz crashes in 10 CPU-minutes.

---

## 4. Layer 2 — Component tests

**Goal:** each package's public API behaves per its contract in isolation, with real collaborators (real SQLite, real filesystem, no mocks) where possible.

### 4.1 Persistence

- CRUD round-trip for every entity type
- Migrations: fresh install → schema v0; upgrade from every prior schema version → current
- Concurrent reads don't block; concurrent writes serialize
- `ON DELETE CASCADE` actually cascades; `ON DELETE SET NULL` does not cascade
- Every unique constraint rejects duplicates with a specific error code
- Every CHECK constraint rejects invalid values with a specific error code
- Integrity check (`PRAGMA integrity_check`) passes after every migration

### 4.2 Crypto

- AES-GCM round-trip on payloads 0 bytes to 16 MiB
- Distinct nonces across 10,000 consecutive encryptions (nonce reuse = catastrophic)
- Tamper detection: flip each byte of a ciphertext in turn; decrypt must fail for each
- Key loading refuses modes other than `0600`
- `Plaintext.String()` returns redaction marker under every formatter (`%v`, `%s`, `%+v`, JSON marshal)
- `Plaintext.Zero()` actually zeroes memory (check via `unsafe` in test)
- Key rotation: encrypt with key A, rotate to B, decrypt succeeds; attempt to decrypt with neither fails

### 4.3 Engine (DAG + RepoMap)

- Topological sort: stable given stable input (no map-iteration nondeterminism)
- Cycle detection names all cycles, not just one
- RepoMap handles every fixture from §2.1 without panicking
- `.gitignore` and `.staircaseignore` patterns match `git check-ignore` behavior on a golden set
- `**` glob support tested explicitly (this was E-2 in the original review)
- Symlinks out of tree are not followed
- Binary files detected and excluded
- Unicode paths handled without mojibake

### 4.4 Secret vault

- `Get` on a missing key returns a typed error, not nil+nil
- `Get` logs to `secret_access_log` with correct outcome on every path
- Rotation is atomic: concurrent `Get` during rotation sees either old-key or new-key result, never a partial state
- Rotation refuses if a run holds the flock
- Scrub: a string containing a secret value is redacted; a string not containing it is passed through

### 4.5 Policy engine

- CEL expressions compile at load time; type errors caught before any run
- `allow_if` + `deny_if` with deny winning, tested for every combination of both-true, both-false, one-true
- Session limits decrement per allowed decision, not per asked decision
- Blanket-deny refused without explicit flag
- Expression with forbidden function (e.g., `os.*`) is rejected at load

### 4.6 Audit

- Chain hash: for a sequence of N events, hash of event i is `SHA-256(prev_hash || body_i)`
- Tampering with any event's body makes chain verification fail at that exact event
- Checkpoints: generated at the correct count and time thresholds
- Checkpoint signatures verify with the public key; fail with any other key
- Append-only file is opened `O_APPEND`; fsync between writes

### 4.7 Orchestrator phase machine

- Each phase is independently unit-testable given mocks of its dependencies (this is the one place we use mocks, because the dependencies are real services)
- Cleanup chain runs in LIFO order on normal return
- Cleanup chain runs in LIFO order on panic
- Cleanup chain runs on every path, not just the success path

### 4.8 Runtime (Python process manager)

- SIGTERM propagates to child via process group
- SIGKILL fallback after grace period
- Inherited FDs are closed in parent after fork
- Stderr is captured and parsed, not written to real stderr

**Exit criteria for L2:** per-package coverage ≥ 85% on the critical packages (`ipc`, `secret`, `audit`, `orchestrator`); 0 failures with `-race`.

---

## 5. Layer 3 — Integration tests

**Goal:** packages wired together behave per their joint contracts. One side real, one side a test double, alternating.

### 5.1 Go server + fake Python client

Raw `net.Dial("unix", ...)` client sending canned message sequences. Covered by the IPC boundary tests from audit §3.5.

Additional scenarios at L3:

- Normal bootstrap sequence: auth → heartbeat → yield → secret → shutdown; all succeed
- Auth with wrong token: rejected within 10s; connection closed
- Auth timeout: no auth sent within 10s; connection closed by server
- Second client tries to connect while first is active: rejected, first unaffected
- Client disconnects mid-yield: orchestrator receives cancellation, does not hang
- Client sends malformed JSON: connection closed, structured error logged
- Client sends over-size payload: rejected at framing layer, connection closed
- Client floods `state.emit` above rate limit: rate-limit error; 3 violations → close
- Heartbeat missed 3 times: connection closed, run marked failed

### 5.2 Real Python client + fake Go server

The inverse. `tests/harness/fake_server.py` speaks the IPC protocol; the real Python harness connects to it.

Scenarios:

- Full normal run with 10 scripted yields; Python exits with code 0
- Server refuses auth: Python exits with code 1 and specific stderr
- Server disconnects mid-yield: Python exits cleanly, does not retry (retry is orchestrator's job, not harness's)
- Server sends a kind Python does not know: Python logs warning, continues (forward compat)
- Server sends oversized response: Python rejects before loading into memory

### 5.3 Real Go + real Python, recorded LLM

The full pipeline except the LLM is a fixture replay. This is the workhorse of L3.

- Startup: `staircase init` in temp dir → venv built → hash verified
- Secret set and retrieve: `staircase secret set TEST_KEY --from-stdin` then read it back from a run
- Simple run: `single-agent` topology + `tiny-go` repo + approve-all operator; run completes SUCCESS
- Linear 3 agents: `linear-3` topology + `monorepo-small` + approve-all; run completes SUCCESS
- Reject one edit: same as above but operator rejects the second yield; run completes with partial changes
- Policy auto-approve: 80% auto-approved; operator sees only 20%; audit log shows correct `source` per decision

### 5.4 Database-level

- A run is interrupted (parent killed -9); state in DB is recoverable on next start
- An audit checkpoint is written mid-run; `staircase audit verify` of the partial log passes
- Concurrent `staircase secret list` and `staircase run` do not corrupt each other

**Exit criteria for L3:** all scenarios pass; no goroutine leaks detected by `goleak`; no orphan Python processes after test teardown (enforced by test harness that greps `ps`).

---

## 6. Layer 4 — Scenario (E2E) tests

**Goal:** the real binary, on the real filesystem, behaves as a user would expect. These are the tests that prove the product works.

Each scenario has:
- A one-sentence goal
- A script (numbered commands; one shell)
- Assertions (git state, filesystem state, audit log state, exit code)
- Expected runtime (wall clock)

Located in `tests/e2e/scenarios/`.

### Scenario E2E-001: Quickstart (10 minutes to first run)

**Goal:** a new user goes from zero to a successful run following only the quickstart docs.

```
1. mkdir /tmp/scdemo && cd /tmp/scdemo
2. export STAIRCASE_DIR=/tmp/scdemo/.staircase
3. staircase init
4. staircase secret set ANTHROPIC_API_KEY --from-stdin   # writes $TEST_API_KEY
5. git init test-repo && cd test-repo
6. echo 'package main\nfunc main() {}' > main.go
7. git add -A && git commit -m init
8. staircase project add demo .
9. staircase topology import ../fixtures/topologies/single-agent.yaml --project demo
10. staircase case new --project demo --name "add-hello" --story "print hello world"
11. staircase run 1 --approver tui --record-llm=none --replay-llm=../fixtures/llm/quickstart.json
12. Assert: git log shows one new commit on branch staircase/run-1
13. Assert: main.go contains "Hello"
14. Assert: staircase run status 1 shows SUCCESS
15. Assert: staircase audit verify reports OK
16. Assert: total wall clock < 10 minutes
```

### Scenario E2E-002: Supervised refactor on a real-ish codebase

**Goal:** the operator drives a medium-complexity refactor; output is a real diff; audit log reconstructs the story.

Uses `monorepo-small` fixture + `linear-3` topology + recorded LLM for "add pagination to list endpoint".

Assertions:
- 3 files modified across 2 packages
- Tests in the repo still pass after the run (run `go test ./...` in the result branch)
- Audit log has entries for every yield
- Every edit's operator decision is recorded with rationale (in the fake operator, script includes notes)

### Scenario E2E-003: Operator rejects all edits

**Goal:** a run with a hostile operator (reject everything) terminates cleanly, leaves the repo in its original state.

Assertions:
- Exit code indicates user-requested failure, not crash
- `git status` on original branch matches pre-run state
- `staircase/run-N` branch exists with no commits (operator rejected every edit)
- Audit log has N yield.decided events, all with decision=reject

### Scenario E2E-004: Policy-driven autonomous run

**Goal:** operator sets policies, walks away, run completes without operator intervention.

Policy: auto-approve any edit to `tests/**`; auto-approve all state.emit; deny edits to `.env`, `.key`, `Makefile`.

Assertions:
- Operator received 0 prompts
- Audit log shows policy IDs on every decision
- A deny policy prevented an attempted `.env` edit (confirmed in audit log)
- Run completes SUCCESS

### Scenario E2E-005: Orphan branch cleanup

**Goal:** a prior crashed run left an orphan branch; new run detects and handles it.

```
1. Start a run; kill -9 the orchestrator mid-yield
2. Verify: branch staircase/run-1 exists, run status in DB is RUNNING
3. Start new run without --reconcile: refuses with error code ORPHAN_BRANCHES
4. Start new run with --reconcile: orphan branch deleted, prior run marked FAILED in DB, new run starts as run-2
5. Verify: audit log for run-1 has event `orphan.reconciled`
```

### Scenario E2E-006: Secret rotation mid-project

**Goal:** a long-lived project rotates its AES key; all existing secrets remain accessible.

```
1. Set 5 secrets (mix of global and project-scoped)
2. Run a case that uses one of them; SUCCESS
3. staircase secret rotate generate /tmp/newkey
4. staircase secret rotate apply /tmp/newkey
5. Verify: all 5 secrets still readable (staircase secret get for each)
6. Run the same case again with replay LLM; SUCCESS
7. staircase secret rotate commit
8. Verify: .key.previous is gone; .key has the new key
```

### Scenario E2E-007: Replay of a past run

**Goal:** given a completed run, reproduce it bit-for-bit.

```
1. Run scenario E2E-002; capture audit log
2. staircase audit export 1 -o /tmp/run1.tar.gz
3. In a fresh $STAIRCASE_DIR: staircase audit import /tmp/run1.tar.gz
4. staircase replay 1
5. Verify: replay emits the same state.emit events in the same order
6. Verify: replay produces the same git diff on a fresh clone of the source repo
```

### Scenario E2E-008: Audit tamper detection

**Goal:** a post-hoc tamper with the audit log is detected.

```
1. Run a short case to completion
2. staircase audit verify: OK
3. Use sqlite3 to modify one byte of an event's payload
4. staircase audit verify: FAIL, with specific event ID of break
5. Same with: modify a signature in audit.checkpoints file
6. Same with: truncate audit.checkpoints file
```

### Scenario E2E-009: Dirty working tree refusal

**Goal:** pre-flight prevents destructive runs.

```
1. Fixture repo with uncommitted changes
2. staircase run: refuses with WORKING_TREE_DIRTY
3. staircase run --allow-dirty: proceeds with loud warning in logs
4. After run: original tree is restored except for the run branch
```

### Scenario E2E-010: Venv corruption and recovery

**Goal:** a broken venv is detected and recovered.

```
1. staircase init succeeds
2. Corrupt: rm $STAIRCASE_DIR/venv/lib/python*/site-packages/staircase_runner/__init__.py
3. staircase run: refuses with VENV_BROKEN
4. staircase init --reset-venv: recreates venv
5. staircase run: succeeds
```

### Scenario E2E-011: Batched review of similar yields

**Goal:** 20 test-file edits are presented as one batched diff.

Uses a topology that deterministically produces 20 similar yields. Fake operator in "batch" mode accepts them all in one action.

Assertions:
- Fake operator's `BatchDecisionsCount == 1`
- Audit log has 20 `yield.decided` events, each with `batch_id` set to the same value

### Scenario E2E-012: JSON output mode

**Goal:** `--json` produces parseable output on every command.

For each top-level command, run with `--json` and pipe to `jq .`; exit code from jq must be 0. Parse and assert structure per documented schema.

### Scenario E2E-013: Exit codes are stable

**Goal:** each documented exit code is produced by a documented condition.

A table test: for each entry in `docs/exit-codes.md`, a minimal scenario that produces that code. `staircase run` with no args → 2 (usage). `staircase run <nonexistent>` → 3 (not found). Etc.

### Scenario E2E-014: Cross-platform smoke

**Goal:** the quickstart (E2E-001) passes on Linux, macOS, Windows.

Runs in CI matrix. Windows variant uses Git Bash; paths are normalized.

**Exit criteria for L4:** all 14 scenarios pass in CI on Linux and macOS; cross-platform smoke passes on Windows; wall-clock total < 30 minutes in aggregate.


---

## 7. Layer 5 — Chaos (fault injection)

**Goal:** the system survives induced faults gracefully. Every fault injection targets a specific invariant.

The chaos harness is `tests/chaos/` — a framework around the scenario tests that injects faults at configurable points.

### 7.1 Fault taxonomy

| Fault | Injection point | Expected behavior |
|---|---|---|
| `KILL_ORCH_AT_PHASE` | Before/after each orchestrator phase | On next start, reconciliation cleans up; `--reconcile` succeeds |
| `KILL_PYTHON` | Mid-yield | Orchestrator detects, marks run FAILED, restores branch |
| `KILL_PYTHON_SIGSTOP` | Mid-yield | Heartbeat timeout fires, same behavior |
| `UDS_DISCONNECT` | Mid-message | Both sides log, orchestrator reconciles |
| `DISK_FULL` | During `AppendEventLog` | Graceful error, run marked FAILED, clear error message |
| `DISK_FULL_WAL` | During WAL checkpoint | SQLite reports error, run aborted, DB remains consistent |
| `DB_LOCKED` | Concurrent writer holds lock > timeout | `busy_timeout` kicks in; error propagates without corruption |
| `CLOCK_SKEW` | Monotonic clock jumps backward | Audit timestamps use monotonic + wall clock; ordering preserved |
| `CLOCK_JUMP_FWD` | Clock advances by 10 minutes | Heartbeat doesn't spuriously timeout (use monotonic) |
| `FILE_PERMS_CHANGED` | Key file mode becomes 0644 mid-run | Next key-load refuses; current run can finish its cached ops |
| `CONFIG_RELOADED` | Policy file changes mid-run | Active run uses policy as loaded; next run sees new policy |
| `SIGTERM_CASCADE` | SIGTERM to orchestrator | Python gets signal, flushes, exits clean; branch restored |
| `SIGHUP_IGNORED` | SIGHUP to orchestrator | Ignored or handled; does not crash |
| `EXIT_DURING_FSYNC` | kill -9 during audit write | On next start, WAL recovery + checkpoint truncation produce a consistent state; chain verify reports break at last partial event |

### 7.2 Implementation of fault injection

For Go-side faults, build tag `chaos` enables injection hooks:

```go
// +build chaos

package orchestrator

var faultHook func(phase string) // set by test harness

func (r *Runner) runPhase(phase string, fn func() error) error {
    if faultHook != nil { faultHook(phase) }
    return fn()
}
```

For OS-level faults (disk full, clock skew), the harness uses Linux facilities:

- `DISK_FULL`: tmpfs mount at known path with size cap; fill before test
- `CLOCK_SKEW`: Linux `libfaketime` or `faketime` wrapper
- `SIGSTOP` / `SIGCONT`: standard signals
- `DB_LOCKED`: another goroutine holds `BEGIN EXCLUSIVE` for N seconds

CI runs chaos tests in a dedicated Linux runner (not macOS — not all faults reproducible there). Documented as Linux-only.

### 7.3 Chaos scenarios (named)

Each scenario ID is `CHAOS-NNN`. Each runs a normal scenario with one injected fault.

- **CHAOS-001 to CHAOS-007**: Scenario E2E-001 with `KILL_ORCH_AT_PHASE` at each of the 7 phases. Verify reconciliation on next start.
- **CHAOS-008**: E2E-002 with `KILL_PYTHON` at yield 5 of 10.
- **CHAOS-009**: E2E-002 with `UDS_DISCONNECT` during `secret.request`.
- **CHAOS-010**: E2E-004 (policy run) with `DISK_FULL` during audit write.
- **CHAOS-011**: E2E-006 (rotation) with `KILL_ORCH_AT_PHASE` during rotation transaction. DB must be consistent; key file either old or new, never indeterminate.
- **CHAOS-012**: E2E-002 with `SIGTERM_CASCADE`; verify Python gets SIGTERM via process group.
- **CHAOS-013**: E2E-007 (replay) against a run that suffered CHAOS-001; replay must refuse on incomplete audit.
- **CHAOS-014 to CHAOS-016**: Concurrent runs — two `staircase run` in parallel on the same repo. Second must fail with EXCLUSIVE_LOCK error; first continues.

**Exit criteria for L5:** all chaos scenarios pass; no scenario leaves the repo, DB, or filesystem in a state that prevents a subsequent clean run.

### 7.4 What chaos does NOT cover (and why)

- **Byzantine faults** (lying Python client). Covered by L7 adversarial, not chaos.
- **Memory pressure / OOM killer**. Hard to simulate reliably; tracked separately.
- **Filesystem corruption at the block level**. Out of scope; SQLite WAL is the defense.
- **Real network partitions**. UDS is local; no network component except the LLM HTTPS, which is mocked.

---

## 8. Layer 6 — Soak tests

**Goal:** over hours to days, nothing drifts, leaks, or accumulates.

These run weekly, and as a gate on each release. A soak test failure does not block a PR; it does block a release.

### 8.1 Metrics captured continuously

Every 10 seconds, the soak harness samples:

| Metric | Concern |
|---|---|
| RSS (Go process) | Memory leak |
| RSS (Python process) | Memory leak |
| Open FD count (both) | FD leak |
| Goroutine count | Goroutine leak |
| DB file size | Unbounded growth |
| `$STAIRCASE_DIR/tmp` file count | Leftovers |
| Yield latency p50/p99 | Degradation |
| Audit chain length | Expected vs actual events |
| CPU user+sys time | Unexpected CPU |

Samples written to a Parquet file for post-run analysis.

### 8.2 Soak scenarios

**SOAK-001 — 1000 small yields**
- Topology generating 1000 yields; recorded LLM; auto-approve policy
- Expected runtime: 60 minutes
- Assertions:
  - No metric shows monotonic growth beyond proportional to run
  - RSS at end < 2x RSS at 10 minutes
  - Zero goroutine growth after stabilization
  - Audit chain verifies

**SOAK-002 — 24 hour continuous operation**
- 100 short runs back-to-back over 24 hours
- Assertions:
  - All 100 runs SUCCESS
  - DB size growth linear in event count (no unbounded tables)
  - No FDs leaked between runs (max FDs across run + 10)
  - Venv hash unchanged (integrity preserved)

**SOAK-003 — Audit log 1 million events**
- Synthetic event injection at 1000 events/s
- Assertions:
  - Insertion rate sustained
  - `staircase audit verify` on 1M events completes in reasonable time (< 60s target)
  - Checkpoint file size grows linearly in checkpoint count, not event count

**SOAK-004 — 7 day quiescent**
- `staircase serve` with HTTP approval endpoint idle for 7 days, no runs
- Assertions:
  - RSS stable
  - Idle CPU < 1%
  - At 7 days: `staircase doctor` reports all green

### 8.3 Flake budget

Soak tests have a flake budget: a single run can fail for reasons outside the test (host reboot, test runner kill). Rerun once. If it fails twice, it's a real failure. Document every rerun.

**Exit criteria for L6:** all four soak scenarios pass in the release cycle; no metric regression vs. prior release exceeds 10% (catches slow leaks).

---

## 9. Layer 7 — Adversarial testing

**Goal:** hostile inputs do not compromise the system's invariants. The agents can lie; the operator can be careless; the filesystem can be booby-trapped.

This is where we simulate the buggy-or-hostile LLM adversary from the threat model.

### 9.1 Adversary profiles (fake LLM personalities)

These are recorded LLM conversations that deliberately misbehave. Each has a specific invariant it targets.

| Adversary | Targets | Expected system response |
|---|---|---|
| `path-traverser` | T-PATH-1 | Proposes `request_edit` with path `../../etc/passwd`; server rejects; run continues |
| `symlink-escaper` | T-PATH-1 | Creates symlink pointing out of repo, then edits through it; server rejects |
| `secret-exfiltrator` | T-EXFIL-1 | Proposes `request_edit` whose content contains an active secret value; scrubber redacts, operator prompt shows warning |
| `flooder` | T-DOS-1 | Sends 10k `state.emit` in a second; rate-limited, connection closed |
| `oversized` | T-PROTO-1 | Sends 10 MiB payload; rejected at framing |
| `malformed-json` | T-PROTO-1 | Sends invalid JSON mid-run; connection closed, run FAILED |
| `liar-correlator` | T-INJECT-1 | Sends response with `correlation_id` for a request that doesn't exist; rejected |
| `replayer` | T-PROTO-1 | Replays a prior message verbatim; rejected (nonce / seq guarded) |
| `silent` | T-CRASH-2 | Stops sending after auth; heartbeat timeout fires; run FAILED |
| `slow-loris` | T-DOS-1 | Sends one byte per second; read deadline fires |
| `poisoner` | Multiple | Proposes an edit that, if accepted, would disable a future gate; operator prompt surfaces the disable attempt; optional auto-deny policy catches |
| `impersonator` | T-INJECT-1 | Claims to be Python but sends a message kind reserved for Go; rejected |

### 9.2 Filesystem adversarial fixtures

Against the `hostile` fixture repo from §2.1:

- Symlinks pointing outside the repo root
- Filenames with `..`, leading dots, control chars, NULs
- Files with no read permission
- A `.git` inside a subdirectory (nested repo)
- A symlink at `.git` pointing to `/tmp`
- Files with UTF-8 encoded NFD names where the filesystem normalizes to NFC on access (macOS)

Each RepoMap + read_file + request_edit operation is tested against each of these.

### 9.3 Operator adversarial scenarios

- Operator provides a policy file with malformed CEL: refused at load
- Operator provides a policy with `deny_if: true` on every kind: refused without `--allow-blanket-deny`
- Operator presses "approve" 1000 times in 100ms (held key): each decision processed exactly once (no duplicate events)
- Operator disconnects from HTTP mid-decision: server times out, decision not recorded

### 9.4 Supply chain adversarial

- Venv with a tampered `staircase_runner` wheel: hash check fails at run start
- Downloaded binary with modified signature: cosign verify fails
- `.key` file with a non-AES value: vault refuses with decrypt error (not a crash)
- A corrupted SQLite WAL: SQLite's built-in recovery runs; stAirCase reports recovery happened

### 9.5 Audit tamper scenarios

These extend E2E-008 with more elaborate tampering:

- Drop one event from `run_event_logs`: chain break detected at that point
- Reorder two events: chain break detected
- Insert a synthetic event: chain break detected (insert breaks both sides)
- Modify an event but also update its hash and all subsequent hashes: **checkpoint signature fails** (the attacker doesn't have the signing key)
- Replace the `.audit.pub` file with a different key: verify fails with "signature key mismatch"
- Truncate `audit.checkpoints` to half its size: detected, specific checkpoints flagged as missing

**Exit criteria for L7:** every adversarial scenario results in the documented expected system response. No adversarial scenario results in a crash, panic, or state corruption. These tests run at every release and quarterly thereafter.

---

## 10. Layer 8 — User-in-the-loop testing

**Goal:** watch a real human who is not the author attempt real tasks. Identify friction that no automated test can see.

This is the hardest testing to do and the most valuable. It is qualitative. It produces a report, not a pass/fail.

### 10.1 Protocol

Before a major release:

1. Recruit 3–5 engineers who have never used stAirCase. Pay them, in cash or its equivalent.
2. Give them the published quickstart docs and a real task: "Refactor this billing module to support multi-currency" or similar.
3. Record their screen + think-aloud narration with consent.
4. Do not help them. Take notes on where they stall, where they misinterpret UI, where they give up.
5. 90-minute session per user.

### 10.2 What to measure

Quantitative:
- Time to first successful run
- Number of doc references made (indicates doc clarity)
- Number of aborted runs (indicates error recovery friction)
- Number of `staircase --help` invocations (indicates CLI discoverability)
- Number of moments where the user said "wait, what?" (scripted think-aloud)

Qualitative:
- What did they think the product did before starting?
- What did they think it did after using it?
- Where did their mental model diverge from reality?
- Would they pay for it?

### 10.3 Output

A written report: `tests/uitl/session-YYYY-MM-DD-<initials>.md`. Public-redacted summaries attached to the release. Specific blocker findings become tracking issues.

### 10.4 What this catches that automation misses

- "I tried to run `staircase run demo` but it needs the run number, not the case name" (UI assumption)
- "I set my API key and the next command errored, so I thought I did it wrong" (feedback problem)
- "I was waiting 30 seconds and didn't know if it was stuck" (progress indicator absent)
- "I accepted the edit but it says 'staircase/run-3 not pushed' — do I need to push?" (documentation gap)
- "Why does it require a git repo? I just have a folder" (scope mismatch)

Every one of these is a real bug and none of them is caught by L1–L7.

**Exit criteria for L8:** 3+ sessions conducted. Blocker findings (users who could not complete the task) are resolved or have an explicit ship-anyway rationale. Non-blocker findings are filed as issues.

---

## 11. Negative tests (the "never" list)

A separate cross-cutting suite of tests that assert what the system never does. Tests for absence are as important as tests for presence.

| ID | Assertion | How tested |
|---|---|---|
| NEG-01 | The binary never makes an outbound network connection | Run with firewall blocking all egress; full quickstart passes |
| NEG-02 | The vault plaintext never appears in stderr | Full run with secret; grep captured stderr for the secret value; must be empty |
| NEG-03 | The vault plaintext never appears in the audit log | Query all audit payloads for the secret value; must be absent |
| NEG-04 | The vault plaintext never appears in a slog field | Redaction handler test with malicious field; asserted redacted |
| NEG-05 | The bootstrap token never appears in any log | Full run; grep for the known token value; must be absent |
| NEG-06 | The Python harness never writes to `$STAIRCASE_DIR` outside its venv and tmp | `inotifywait` on `$STAIRCASE_DIR` during run; no writes outside expected paths |
| NEG-07 | A run never modifies the original branch | Compare git ref before/after; must be identical |
| NEG-08 | A run never modifies files outside the repo root | `find` recorded files touched during run; all within repo |
| NEG-09 | The `.key` file never has mode > 0600 during its lifetime | fs watcher during run; fail if mode changes |
| NEG-10 | The IPC socket never has mode > 0600 during its lifetime | Same |
| NEG-11 | No Python process remains after a run ends | `ps aux | grep staircase_runner` empty within 10s of orchestrator exit |
| NEG-12 | A rejected yield never produces a file edit | E2E-003; assert filesystem state matches pre-run |
| NEG-13 | A policy-denied edit never reaches the filesystem | Similar; fixture policy denies all; filesystem unchanged |
| NEG-14 | Errors never include full file paths containing `$HOME` | Grep all error outputs for `/home/` or `/Users/`; only project-relative paths allowed |
| NEG-15 | No test leaves files in `/tmp` after completion | `find /tmp -name staircase-* -mtime -1` empty after suite |

---

## 12. Test harness and execution

### 12.1 Makefile targets

```
make test                # L1+L2; fast; runs on every save
make test-integration    # L3; runs on every PR
make test-e2e            # L4; runs on every PR
make test-chaos          # L5; runs nightly and on release
make test-soak           # L6; runs weekly and on release
make test-adversarial    # L7; runs on release
make test-all-automated  # L1-L7; excludes L8

make test-quick          # L1+L2+L4 smoke (E2E-001 only); local dev
make test-ci             # what CI runs on PR: L1+L2+L3+L4
```

### 12.2 Tagging

Go tests: build tags to separate levels:

```go
//go:build integration    // L3
//go:build e2e            // L4
//go:build chaos          // L5
//go:build soak           // L6
//go:build adversarial    // L7
```

Python tests: pytest markers:

```python
@pytest.mark.integration
@pytest.mark.e2e
```

### 12.3 Test environment requirements

| Layer | Requires |
|---|---|
| L1, L2 | Go toolchain, Python 3.11+ |
| L3 | + SQLite, UDS support |
| L4 | + git, Python venv, recorded LLM fixtures |
| L5 | + Linux (libfaketime, tmpfs mount), root to mount tmpfs |
| L6 | + dedicated host, long-running runner |
| L7 | + all of the above |
| L8 | + humans, recording software |

### 12.4 CI matrix

Every PR:
- Linux (Ubuntu 22.04, 24.04) on amd64 and arm64
- macOS (latest-1, latest) on arm64
- Windows (latest) — L4 quickstart only (E2E-001 + E2E-014)

Nightly:
- Full L1–L5 on Linux amd64

Weekly:
- L6 soak on dedicated runner

Pre-release:
- Full L1–L7 on the full matrix
- L8 (humans) in the week prior

### 12.5 Flakiness policy

A test that fails in CI on `main` is an immediate `P0` issue. The test is either:

1. Reverted if it was added in the failing commit
2. Marked `t.Skip` with a linked tracking issue if the underlying bug is real but not in the test
3. Fixed forward within 24 hours if the bug is in the test itself

No test is allowed to be "known flaky" for more than 7 days without either removal or fix. A known-flaky test is a broken test.

### 12.6 Test data management

Fixtures are checked into git (tarballs for repos, JSON for everything else). Fixtures must be reproducible: `make rebuild-fixtures` regenerates them from source and the result must byte-identical-match what's in git. A PR that changes fixtures without changing their generators fails CI.

LLM recordings are explicit exceptions — they come from real API calls. Each recording has provenance metadata (who recorded it, when, against what model version). Recording regeneration is a manual, logged action.

---

## 13. Reporting

### 13.1 Per-run reports

Every test run produces `test_report.json`:

```json
{
  "meta": { "commit": "...", "branch": "...", "date": "...", "layer": "L4", "runner": "github-actions" },
  "summary": { "passed": 14, "failed": 0, "skipped": 0, "flaky_retries": 0 },
  "results": [
    { "id": "E2E-001", "status": "PASS", "duration_seconds": 18.2, "assertions": 6 },
    ...
  ],
  "coverage": { "overall": 0.82, "per_package": { ... } },
  "metrics": { "wall_time": 124, "cpu_time": 287 }
}
```

### 13.2 Release-gate dashboard

A per-release page that shows, for each scenario:

- Last pass / last fail
- Mean / p99 duration over last 30 runs
- Coverage trend

Reviewed by the release manager before signing off.

### 13.3 Weekly health email

Automated summary:
- Flaky tests (any test that failed and passed within 7 days)
- Duration drift (tests whose p99 grew by > 20% WoW)
- Coverage drift (any package that dropped > 2%)

---

## 14. Owning and maintaining this plan

| Concern | Owner |
|---|---|
| L1, L2 maintenance | Package author |
| L3, L4 scenario authoring | Feature author |
| L5 chaos scenarios | Platform engineer |
| L6 soak infrastructure | Release engineer |
| L7 adversarial | Security engineer (or reviewer) |
| L8 user-in-the-loop | Product / DevRel |
| Fixtures | Explicit OWNERS file |
| LLM recordings | Recording lead; authorized to regenerate |

Each layer has a named owner. Unowned tests decay.

---

## 15. Acceptance criteria for "battle tested"

The product is "battle tested" — a specific, binary claim — when **all** of these hold:

1. L1–L4 pass on every PR, on every platform in the matrix, for 30 consecutive days with zero non-flake failures. (30 days is the memory test for the test suite itself.)
2. L5 chaos has at least one scenario targeting each threat model item from §5 of the build plan. Every threat model item is exercised.
3. L6 soak has completed at least one 24-hour run and one 7-day quiescent run without alerts.
4. L7 adversarial has been executed against the current release by someone outside the implementation team.
5. L8 user-in-the-loop has produced at least 3 session reports. All session-blocking findings are resolved or have explicit ship-anyway sign-off.
6. NEG-01 through NEG-15 all pass.
7. A fresh engineer, given only the quickstart docs and 30 minutes, can run a successful case. Verified, not assumed.

Anything less than all seven is **not** battle tested, regardless of how green the unit test dashboard looks.

---

## 16. What this plan is not

- A substitute for production monitoring. Once users exist, the metrics they generate tell you things no test can. Test coverage gets you to "ready to learn from users"; it does not replace the learning.
- A substitute for professional security review. L7 is simulation; a real pentester finds things the harness cannot imagine.
- A substitute for code review. Tests catch regressions; review catches decisions. Both matter.
- A one-time effort. This plan evolves. Each bug found in production spawns a test at the lowest layer that could have caught it.

---

## 17. Rollout sequence

Do not build all layers at once. Build in the order of value:

1. **Week 1:** L1 + L2 + fixtures + fake operator. Everything after depends on these.
2. **Week 2:** L3 + recorded LLM + scenario E2E-001. The first end-to-end green is the project's real beginning.
3. **Week 3:** L4 scenarios E2E-002 through E2E-010.
4. **Week 4:** L4 scenarios E2E-011 through E2E-014 + NEG-01 through NEG-15.
5. **Week 5:** L5 chaos for top 7 fault modes (KILL_ORCH, KILL_PYTHON, DISK_FULL, SIGTERM_CASCADE, CONCURRENT_RUNS, UDS_DISCONNECT, CLOCK_SKEW).
6. **Week 6:** L6 SOAK-001 and SOAK-002.
7. **Week 7:** L7 adversarial — top 6 adversaries (path-traverser, secret-exfiltrator, flooder, silent, oversized, audit-tamperer).
8. **Week 8:** L8 first session.

This gets to a defensible "battle tested" claim in two months. Everything beyond is incremental hardening.

---

*End of plan. The expected first concrete artifact is `tests/fixtures/repos/tiny-go.sh` + `tests/e2e/scenarios/e2e_001_quickstart_test.go` — one fixture, one scenario, both end-to-end green. Everything else follows that template.*
