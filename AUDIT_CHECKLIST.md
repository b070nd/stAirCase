# stAirCase — Codebase Audit Checklist for Claude Sonnet 4.6

**Purpose:** Produce a single, rigorous, evidence-based audit report that the plan author (Opus) can read in 5 minutes and act on the same day.

**Input:** A checkout of the stAirCase repository, current branch.

**Output:** One file `AUDIT_REPORT.md` plus a machine-readable `audit_report.json`, written to `/mnt/user-data/outputs/`.

---

## 0. Rules of engagement (non-negotiable)

Read these before starting. Violating any of these rules invalidates the entire audit.

1. **Evidence, not opinion.** Every finding cites a file path, line number, and either a command's output or a quoted code span. If you cannot cite, omit the finding.
2. **Execute, don't speculate.** Every "test passes" claim comes from running the test. Every "coverage ≥ 75%" claim comes from a coverage tool. No guesses.
3. **One finding, one row.** Do not batch findings. Each distinct defect is its own row with its own ID.
4. **No scope creep.** If the checklist says check X, check X. If you notice Y, record it in §13 "Out-of-scope observations" — do not silently expand the audit.
5. **Red means red.** Do not soften ratings to be polite. If a check fails, it fails. If it's inconclusive, it's inconclusive — that is its own outcome, not a pass.
6. **Reproducibility.** Every command you run goes in `audit_commands.log`. Someone else rerunning those commands in order on the same commit must get the same report.
7. **Time-box each section.** If a section takes more than 30 minutes, stop, record what you have, note the incomplete areas in §14, and move on. A complete report with gaps is more useful than an incomplete report with none.
8. **Do not modify the code under audit.** Not even to "try a fix." If you need to test a hypothesis, work in `/tmp`. The repo ends the audit byte-identical to how it started (`git status --porcelain` empty at exit).

---

## 1. Report format (emit exactly this structure)

The final `AUDIT_REPORT.md` must contain these sections in this order. Section lengths may vary; the structure may not.

```
# stAirCase Audit Report
Date: <ISO-8601 UTC>
Commit: <full SHA>
Branch: <branch name>
Auditor: Claude Sonnet 4.6
Duration: <HH:MM:SS>

## Executive Summary
- Overall status: GREEN | YELLOW | RED | BLOCKED
- Critical findings: <N>
- High findings: <N>
- Medium findings: <N>
- Low findings: <N>
- Items not evaluated: <N>
- Top 3 recommendations (one line each)

## Findings Table
<ID, Severity, Area, Summary, File:Line, Reproduction>
(Every row must be individually actionable)

## Detail Per Finding
<For each ID in the table: full context, evidence, suggested fix>

## Checklist Results
<For each check in §3-§12 of the checklist: status, evidence, notes>

## Out-of-Scope Observations
<Things noticed that weren't in the checklist — flag only, do not fix>

## Incomplete Areas
<Anything the audit did not cover, with the reason>

## Appendix A: Commands Run
<Full list in order>

## Appendix B: Environment
<Go version, Python versions available, OS, available tools>
```

The machine-readable `audit_report.json` has a fixed schema:

```json
{
  "meta": { "commit": "...", "branch": "...", "date": "...", "duration_seconds": 0 },
  "summary": {
    "overall": "GREEN|YELLOW|RED|BLOCKED",
    "counts": { "critical": 0, "high": 0, "medium": 0, "low": 0, "info": 0, "not_evaluated": 0 }
  },
  "findings": [
    { "id": "F-001", "severity": "CRITICAL|HIGH|MEDIUM|LOW|INFO",
      "area": "ipc|secret|orchestrator|audit|...",
      "check_id": "3.2.1",
      "summary": "...", "file": "path/to/file.go", "line": 42,
      "evidence": "...", "reproduction": "shell command or steps",
      "suggested_fix": "..." }
  ],
  "checks": [
    { "id": "3.2.1", "description": "...", "status": "PASS|FAIL|INCONCLUSIVE|N/A",
      "evidence": "...", "duration_seconds": 0 }
  ],
  "out_of_scope_observations": [ "..." ],
  "incomplete_areas": [ { "area": "...", "reason": "..." } ]
}
```

### Severity definitions (binding)

- **CRITICAL**: correctness or security defect that is actively exploitable or that causes data loss / key exposure / audit-log forgery. Example: the I-3 encrypted-secret-to-Python bug.
- **HIGH**: defect that causes a feature to be broken or a documented invariant to be violated in realistic conditions. Example: orphan branches block rerun.
- **MEDIUM**: defect that degrades quality or creates foreseeable future pain, but does not break a feature today. Example: unbounded payload in a non-hot path.
- **LOW**: style, documentation, or minor cleanup. Example: inconsistent error message format.
- **INFO**: observation that is useful to record but is not a defect. Example: "this dependency is widely used but pinned at an unusual version."

### Status definitions (binding)

- **PASS**: the check's success criterion was met, with evidence.
- **FAIL**: the check's success criterion was not met, with evidence.
- **INCONCLUSIVE**: the check could not be definitively evaluated (tool missing, flaky test, etc.); record why.
- **N/A**: the check does not apply to this codebase state (phase not started yet).

**INCONCLUSIVE is not a synonym for PASS.** If you cannot determine a result, say so. The plan author would rather know what's unknown than assume what's unverified.

---

## 2. Pre-flight: environment and repo hygiene

Run these first. If any of them fail, the remaining checks may be invalid — note this and proceed with caveats.

### 2.1 Environment inventory

For each, capture version and path. Record in Appendix B.

| Tool | Command | Required? |
|---|---|---|
| Go | `go version` | Required (≥ 1.22) |
| Python | `python3 --version`, `which python3.11`, `which python3.12`, `which python3.13` | At least one of 3.11/3.12/3.13 |
| git | `git --version` | Required |
| sqlite3 | `sqlite3 --version` | Required for DB inspection |
| staticcheck | `which staticcheck` | Nice to have |
| golangci-lint | `which golangci-lint` | Nice to have |
| gosec | `which gosec` | Nice to have |
| govulncheck | `which govulncheck` | Nice to have |
| ripgrep (rg) | `which rg` | Nice to have (fallback: `grep -r`) |

If a "nice to have" tool is missing, install it if `go install` is available; otherwise mark the dependent checks INCONCLUSIVE with the reason.

### 2.2 Repo hygiene

```bash
git status --porcelain   # must be empty
git log -1 --oneline     # record commit
git describe --always --dirty
```

- **Check 2.2.1** — Working tree clean. Fail if `git status --porcelain` is non-empty before audit starts.
- **Check 2.2.2** — `go.sum` present and `go mod verify` succeeds.
- **Check 2.2.3** — No binary artifacts checked in outside `testdata/` (`git ls-files | xargs file | grep -v 'ASCII\|UTF-8\|empty\|symbolic link'`).
- **Check 2.2.4** — No files with mode other than 0644 or 0755 checked in (`git ls-files -s | awk '{print $1}' | sort -u`).

### 2.3 Baseline build

```bash
go build ./...
go test ./... -count=1 -short 2>&1 | tee /tmp/test_baseline.log
```

- **Check 2.3.1** — `go build ./...` succeeds with zero output.
- **Check 2.3.2** — `go test ./... -short` succeeds with zero failures. If anything fails, record in findings with severity HIGH minimum and note this will cause cascading failures in later checks.

---

## 3. IPC protocol and server

This is the highest-priority area. The threat model makes IPC the trust boundary; the original review flagged the worst bugs here. Every check in this section is mandatory.

### 3.1 Schema as source of truth

- **Check 3.1.1** — A schema file exists at `proto/ipc.v1.schema.json` (or equivalent canonical path). If absent, Phase 2 has not started; mark §3.1–§3.3 N/A with reason and continue.
- **Check 3.1.2** — The schema is valid JSON Schema 2020-12.
  ```bash
  python3 -c "import jsonschema; import json; s=json.load(open('proto/ipc.v1.schema.json')); jsonschema.Draft202012Validator.check_schema(s); print('ok')"
  ```
- **Check 3.1.3** — Every `Kind` enum value in the schema has a corresponding `$defs` entry for its payload.
  ```bash
  # Extract Kind enum, extract $defs keys, assert every Kind has a matching payload def
  python3 <<'PY'
  import json
  s = json.load(open('proto/ipc.v1.schema.json'))
  kinds = s['$defs']['Kind']['enum']
  defs = set(s['$defs'].keys())
  missing = [k for k in kinds if k.replace('.', '_') not in defs and k not in defs]
  print('missing:', missing)
  PY
  ```
  Expect empty list.
- **Check 3.1.4** — Every message schema has `additionalProperties: false`. Find any that don't:
  ```bash
  python3 -c "import json,sys; s=json.load(open('proto/ipc.v1.schema.json')); [print(k) for k,v in s['$defs'].items() if v.get('type')=='object' and v.get('additionalProperties', True) is not False]"
  ```
  Expect no output.

### 3.2 Generated code freshness

- **Check 3.2.1** — Running `go generate ./...` produces no diff:
  ```bash
  go generate ./... && git diff --exit-code -- internal/ipc/gen/
  ```
  Non-zero exit = generated code is stale. FAIL = HIGH finding.
- **Check 3.2.2** — Python generated code matches:
  ```bash
  make runner-codegen 2>/dev/null || python3 tools/genipc/py.py
  git diff --exit-code -- runner/staircase_runner/ipc/
  ```
- **Check 3.2.3** — Every `Kind` in the schema has exactly one handler in Go. Extract Kind enum, grep for handler names:
  ```bash
  # For each kind, expect exactly one match in internal/ipc/server/handlers.go
  for k in $(python3 -c "import json; [print(k) for k in json.load(open('proto/ipc.v1.schema.json'))['\$defs']['Kind']['enum']]"); do
    count=$(rg -c "\"$k\"" internal/ipc/server/ 2>/dev/null | head -1)
    echo "$k: $count"
  done
  ```

### 3.3 Conformance suite

- **Check 3.3.1** — Conformance corpus exists at `tests/conformance/corpus/` with at least one `.json` per `Kind`.
- **Check 3.3.2** — Go-side conformance test passes:
  ```bash
  go test ./tests/conformance/... -run TestConformance -v -count=1
  ```
- **Check 3.3.3** — Python-side conformance test passes:
  ```bash
  cd runner && python3 -m pytest tests/test_conformance.py -v
  ```
- **Check 3.3.4** — The corpus includes malformed cases (each with `expect_error: true`) and oversized cases. Count them:
  ```bash
  ls tests/conformance/corpus/*.json | wc -l
  grep -l '"expect_error": true' tests/conformance/corpus/*.json | wc -l
  grep -l 'oversized\|too_large' tests/conformance/corpus/*.json | wc -l
  ```
  Expect ≥ 10 malformed, ≥ 3 oversized.

### 3.4 The critical secret-plaintext contract

This is the I-3 / T-1 bug from the original review. It must be verifiable independently.

- **Check 3.4.1** — The `secret.response` schema contains a field named `value` (or equivalent plaintext field), NOT `encrypted_value`. Grep:
  ```bash
  rg "encrypted_value" proto/ internal/ipc/ runner/ 2>&1 | grep -v test
  ```
  Hits in production code (non-test) = CRITICAL finding.
- **Check 3.4.2** — The handler decrypts before responding. Read `internal/ipc/server/handlers.go` and find `handleSecretRequest` (or equivalent). The function body must contain a call to a decryption function (e.g., `vault.Decrypt`, `crypto.Decrypt`). If the handler returns a database row's `EncryptedValue` field directly, that is the original bug and is CRITICAL.
- **Check 3.4.3** — The Python client uses the response as plaintext. In `runner/staircase_runner/`, grep:
  ```bash
  rg "request_secret|get_secret|secret_request" runner/staircase_runner/
  rg "encrypted" runner/staircase_runner/
  ```
  The response should be passed directly as an API key or similar; it should NOT be passed through a decryption call on the Python side (Python never holds the AES key).
- **Check 3.4.4** — An end-to-end test exists that actually starts Python, requests a secret, and verifies the plaintext arrives:
  ```bash
  go test ./tests/e2e/... -run TestSecretRoundTrip -v -count=1
  ```

### 3.5 IPC server hardening

- **Check 3.5.1** — UDS socket is chmod'd to 0600 after bind. Grep:
  ```bash
  rg "net\.Listen.*unix" internal/ipc/
  rg "Chmod.*0o?600|Chmod.*384" internal/ipc/
  ```
  Both patterns must be present in the same file, and the Chmod must textually follow the Listen.
- **Check 3.5.2** — A test verifies the 0600 mode:
  ```bash
  rg -l "FileMode|0o?600|Mode\(\)\.Perm\(\)" internal/ipc/*_test.go
  ```
- **Check 3.5.3** — Peer credential check exists. At least one of:
  ```bash
  rg "SO_PEERCRED|LOCAL_PEERCRED|Ucred|getpeereid" internal/ipc/
  ```
  For platforms where it's supported. Missing on all platforms = HIGH.
- **Check 3.5.4** — Only one client connection is accepted. Look for a sentinel/flag pattern:
  ```bash
  rg "Accept\(\)" internal/ipc/server/
  # Read the surrounding context: there must be logic that rejects a second Accept
  ```
- **Check 3.5.5** — Schema validation runs before unmarshal. In the message read loop:
  ```bash
  rg "Validate|validate" internal/ipc/server/ | head -20
  ```
  The validator must be called on raw bytes before the message is unmarshaled.
- **Check 3.5.6** — Rate limits exist per message kind:
  ```bash
  rg "rate\.|Limiter|TokenBucket" internal/ipc/
  ```
- **Check 3.5.7** — Heartbeat timeout enforced. Grep for `SetReadDeadline` and a timeout constant:
  ```bash
  rg "SetReadDeadline|heartbeatTimeout|HeartbeatTimeout" internal/ipc/
  ```
- **Check 3.5.8** — All handlers return either a typed response OR an `error` envelope (I-4 fix). Read each handler; assert every code path ends in one or the other. If any handler has a code path that silently returns nil to the client, that is HIGH.
- **Check 3.5.9** — Payload size cap enforced at framing layer (not just at AppendEventLog). Look for scanner buffer sizing:
  ```bash
  rg "scanner\.Buffer|bufio\.MaxScanTokenSize|MaxMessageSize" internal/ipc/
  ```
  Expect a cap ≤ 256 KiB, not 1 MiB.

### 3.6 Coverage and tests

- **Check 3.6.1** — IPC server package coverage ≥ 85%:
  ```bash
  go test -coverprofile=/tmp/ipc.cov ./internal/ipc/server/... 2>&1
  go tool cover -func=/tmp/ipc.cov | tail -1
  ```
- **Check 3.6.2** — Fuzz target exists and runs clean for 60s:
  ```bash
  go test -fuzz=FuzzEnvelope -fuzztime=60s ./internal/ipc/server/... 2>&1 | tail
  ```
  Any crash = HIGH. No fuzz target = MEDIUM.
- **Check 3.6.3** — `-race` clean:
  ```bash
  go test -race ./internal/ipc/... -count=1
  ```


---

## 4. Secret vault and at-use audit

### 4.1 Data model

- **Check 4.1.1** — The `secrets` table has `version`, `is_active`, `encryption_scheme` columns.
  ```bash
  sqlite3 $STAIRCASE_DIR/staircase.db ".schema secrets"
  # Or inspect migration files:
  rg "ALTER TABLE secrets|CREATE TABLE.*secrets" internal/persistence/migrations/
  ```
- **Check 4.1.2** — Partial unique indexes exist (one for global, one for project-scoped):
  ```bash
  sqlite3 $STAIRCASE_DIR/staircase.db ".indexes secrets"
  ```
  Or, in migrations, find `CREATE UNIQUE INDEX.*WHERE scoped_to_project_id IS (NOT )?NULL`.
- **Check 4.1.3** — `secret_access_log` table exists with `outcome` CHECK constraint.
- **Check 4.1.4** — `ON DELETE SET NULL` (not CASCADE) on `secret_access_log.run_id`. Deleting a run must not delete its audit history.

### 4.2 Crypto correctness

- **Check 4.2.1** — AES-GCM nonce is 12 bytes and comes from `crypto/rand`, never from a counter:
  ```bash
  rg "NonceSize|nonce.*make|rand\.Read" internal/crypto/
  ```
  Must see `rand.Read` writing into a 12-byte slice.
- **Check 4.2.2** — The `Plaintext` (or equivalent) type has a `Zero()` method and a `String()` that returns redaction:
  ```bash
  rg "type Plaintext|func.*Plaintext.*Zero|func.*Plaintext.*String" internal/secret/ internal/crypto/
  ```
  Test that `fmt.Sprintf("%v", pt)` returns `<redacted>` or equivalent:
  ```bash
  go test ./internal/secret/ -run TestPlaintextRedaction -v
  ```
- **Check 4.2.3** — Key file permission check at every load, not just at init:
  ```bash
  rg "Stat.*key|Mode\(\)\.Perm|0o?600" internal/crypto/ internal/secret/
  ```
  Read the `LoadKey` function body. It must check the mode bits and refuse if not 0600.
- **Check 4.2.4** — Atomic key write uses temp+rename, not `WriteFile`:
  ```bash
  rg "os\.WriteFile.*\.key|TempFile|Rename" internal/crypto/
  ```
  If `GenerateKey` or equivalent uses `os.WriteFile`, that is R-3 / C-3 territory; MEDIUM.
- **Check 4.2.5** — Decryption distinguishes "wrong key" from "corrupt ciphertext":
  ```bash
  go test ./internal/crypto/ -run "TestDecrypt.*Corrupt|TestDecrypt.*WrongKey" -v
  ```

### 4.3 Rotation

- **Check 4.3.1** — `staircase secret rotate` command exists:
  ```bash
  ./staircase secret rotate --help 2>&1 | head -20
  ```
- **Check 4.3.2** — Rotation is atomic (single DB transaction):
  ```bash
  rg -B2 -A20 "func.*Rotate" internal/secret/
  ```
  Look for `store.BeginTx` / `tx.Commit` wrapping the re-encryption loop.
- **Check 4.3.3** — Rotation is guarded by a filesystem lock; refuses while a run is active:
  ```bash
  rg "flock|Flock|LOCK_EX" internal/
  ```

### 4.4 At-use audit

- **Check 4.4.1** — Every code path that decrypts a secret writes a `secret_access_log` row with outcome. Read the vault's `Get` / decrypt methods; every return (including errors) must have a matching `Audit(...)` call.
- **Check 4.4.2** — Integration test: a run that requests N secrets produces N rows in `secret_access_log`:
  ```bash
  go test ./internal/secret/ -run TestAtUseAuditCounts -v
  ```
- **Check 4.4.3** — Pre-presentation secret scrub exists. In the diff presentation code (Phase 7), active secret values are scanned:
  ```bash
  rg "scrub|Scrub|redact.*secret|activeValues" internal/secret/ internal/tui/ internal/orchestrator/
  ```

### 4.5 CLI surface

- **Check 4.5.1** — `staircase secret set` does NOT accept the value as an argv. Grep:
  ```bash
  rg -A10 "\"set\"" cmd/staircase/secret.go
  ```
  If there's a `--value <v>` flag, that is MEDIUM (shell history leak).
- **Check 4.5.2** — `staircase secret list` never prints values. Run and confirm:
  ```bash
  ./staircase secret set TEST_KEY --from-stdin <<< "s3cr3t"
  ./staircase secret list | grep -v "s3cr3t"  # must pass
  ```
  (Cleanup: `./staircase secret delete TEST_KEY`.)

---

## 5. Orchestrator and git reconciliation

### 5.1 Phase machine presence

- **Check 5.1.1** — `internal/orchestrator/` exists; has a `Runner` type with a single `Run(ctx, caseID, opts)` entry point.
- **Check 5.1.2** — A phase enum or labeled sequence exists (PRE_FLIGHT, BRANCH_CREATE, IPC_LISTEN, PYTHON_BOOT, TOPOLOGY_LOAD, AGENT_LOOP, FINALIZE, BRANCH_RESTORE):
  ```bash
  rg "PRE_FLIGHT|BRANCH_CREATE|IPC_LISTEN|PYTHON_BOOT|AGENT_LOOP|FINALIZE|BRANCH_RESTORE" internal/orchestrator/
  ```

### 5.2 Working tree safety

- **Check 5.2.1** — Pre-flight refuses to run on a dirty tree unless `--allow-dirty`:
  ```bash
  rg "porcelain|IsClean|WorkingTreeClean|allow.dirty|allow-dirty|AllowDirty" internal/orchestrator/ cmd/
  ```
- **Check 5.2.2** — Orphan branch detection and reconciliation exists:
  ```bash
  rg "orphan|Orphan|staircase/run-|Reconcile" internal/orchestrator/
  ```
- **Check 5.2.3** — Branch restoration uses defer/cleanup chain:
  ```bash
  rg -B2 -A5 "defer.*[Cc]leanup|cleanup\.Run" internal/orchestrator/
  ```
  Must run LIFO regardless of panic; test this:
  ```bash
  go test ./internal/orchestrator/ -run TestCleanupChainOnPanic -v
  ```
- **Check 5.2.4** — Detached-HEAD handling: capture by SHA:
  ```bash
  rg "detached|HEAD|RevParse|CommitHash" internal/orchestrator/
  ```

### 5.3 Git operations

- **Check 5.3.1** — Uses `go-git` library, not shelling out:
  ```bash
  rg "go-git\.v5|exec\.Command.*git" internal/orchestrator/
  ```
  `exec.Command("git", ...)` uses in the orchestrator (not in test fixtures) = MEDIUM.
- **Check 5.3.2** — Integration test creates orphan branches, then runs reconciliation:
  ```bash
  go test ./internal/orchestrator/ -run TestOrphanReconciliation -v
  ```

### 5.4 Python process lifecycle

- **Check 5.4.1** — Bootstrap token passed via stdin, not env:
  ```bash
  rg "STAIRCASE_TOKEN|bootstrap.*env|Setenv.*token" internal/runtime/
  ```
  Any match in production code = CRITICAL (leak via /proc/*/environ).
- **Check 5.4.2** — Topology passed via inherited fd, not written to `tmp/`:
  ```bash
  rg "ExtraFiles|topology.*WriteFile|graph_exec_case" internal/runtime/
  ```
  Presence of `graph_exec_case*.py` write = architectural regression; HIGH.
- **Check 5.4.3** — Process group signaling works: `Setpgid: true` and signal propagation:
  ```bash
  rg "Setpgid|Setsid|SysProcAttr" internal/runtime/
  ```
- **Check 5.4.4** — Stderr captured and routed to structured logger (R-1 fix):
  ```bash
  rg "cmd\.Stderr" internal/runtime/
  ```
  If `cmd.Stderr = nil` or inherits os.Stderr directly, note as MEDIUM.
- **Check 5.4.5** — SIGTERM + 5s grace + SIGKILL pattern:
  ```bash
  rg "SIGTERM|SIGKILL|Signal.*Interrupt|WithTimeout.*5" internal/runtime/
  ```

### 5.5 Reproducibility test

- **Check 5.5.1** — Kill the orchestrator mid-run at each phase boundary; verify branch restoration on next run with `--reconcile`. An existing test must do this:
  ```bash
  go test ./internal/orchestrator/ -run "TestCrashInjection|TestKillAtPhase" -v
  ```
  Missing = HIGH.

---

## 6. Python harness (vendored runner)

### 6.1 Structure

- **Check 6.1.1** — `runner/` directory exists with `pyproject.toml` and `staircase_runner/` package.
- **Check 6.1.2** — No `codegen.go` template system remains:
  ```bash
  find . -name "codegen.go" -not -path "./legacy/*"
  find . -name "graph_exec_case*.py" -not -path "./legacy/*"
  ```
  Any hits = architectural regression; HIGH.
- **Check 6.1.3** — Wheel hash is embedded in the Go binary:
  ```bash
  rg "wheel_hash|WheelHash|sha256.*whl" internal/runtime/
  ```

### 6.2 Dependencies

- **Check 6.2.1** — `requirements.txt` or `pyproject.toml` has hash-pinned dependencies (`--require-hashes` compatible):
  ```bash
  grep -E "^[a-zA-Z0-9_-]+==[0-9]+\.[0-9]+" runner/requirements.txt | head -5
  grep -c "sha256:" runner/requirements.txt
  ```
- **Check 6.2.2** — `pip-audit` clean:
  ```bash
  cd runner && pip-audit --requirement requirements.txt 2>&1 | tail
  ```

### 6.3 Type safety and lint

- **Check 6.3.1** — `mypy --strict` clean:
  ```bash
  cd runner && mypy --strict staircase_runner/ 2>&1 | tail
  ```
- **Check 6.3.2** — `ruff check` clean:
  ```bash
  cd runner && ruff check staircase_runner/ 2>&1 | tail
  ```
- **Check 6.3.3** — `bandit` clean:
  ```bash
  cd runner && bandit -r staircase_runner/ 2>&1 | tail
  ```

### 6.4 Timeout and error handling

- **Check 6.4.1** — `sys.stdin.readline()` has a timeout wrapper (T-2 fix):
  ```bash
  rg "stdin.readline|readline\(" runner/staircase_runner/
  rg "select\.select|signal\.alarm|threading.*Timer" runner/staircase_runner/
  ```
  Must find both patterns in proximity.
- **Check 6.4.2** — Every IPC call has a timeout:
  ```bash
  rg "timeout=" runner/staircase_runner/ipc/
  ```
  Count calls; every network op should have one.

### 6.5 Runner conformance

- **Check 6.5.1** — Python conformance tests pass (already covered in 3.3.3 but run again scoped):
  ```bash
  cd runner && python3 -m pytest tests/test_conformance.py -v
  ```
- **Check 6.5.2** — Wheel builds reproducibly:
  ```bash
  cd runner && SOURCE_DATE_EPOCH=1700000000 python3 -m build --wheel 2>&1 | tail
  # Hash the result
  sha256sum dist/*.whl > /tmp/hash1
  rm -rf dist/ build/ *.egg-info
  SOURCE_DATE_EPOCH=1700000000 python3 -m build --wheel 2>&1 | tail
  sha256sum dist/*.whl > /tmp/hash2
  diff /tmp/hash1 /tmp/hash2
  ```
  Diff must be empty.

---

## 7. HITL (TUI, policy engine, batching)

### 7.1 Policy engine

- **Check 7.1.1** — `internal/policy/` exists; uses CEL (`github.com/google/cel-go`):
  ```bash
  ls internal/policy/
  rg "cel-go|cel\." internal/policy/ go.mod
  ```
- **Check 7.1.2** — Policy file format has `version`, `policies[]`, and `limits{}`:
  ```bash
  find . -name "*policy*.yaml" -path "*/fixtures/*" | head -5
  find . -name "*policy*.yaml" -path "*/examples/*" | head -5
  ```
- **Check 7.1.3** — Policies compile at load time; invalid expressions refused:
  ```bash
  go test ./internal/policy/ -run TestPolicyLoadValidation -v
  ```
- **Check 7.1.4** — `deny_if` wins over `allow_if`:
  ```bash
  go test ./internal/policy/ -run TestDenyBeatsAllow -v
  ```
- **Check 7.1.5** — Blanket-deny refused without explicit flag:
  ```bash
  go test ./internal/policy/ -run TestBlanketDenyRequiresFlag -v
  ```

### 7.2 Session limits

- **Check 7.2.1** — Limits are enforced mid-run:
  ```bash
  rg "max_auto_approved|max_total_yields|max_run_duration" internal/policy/
  ```
- **Check 7.2.2** — Exhaustion surfaces as `Ask` to the operator (not silent allow or deny):
  ```bash
  go test ./internal/policy/ -run TestLimitExhaustionAsksOperator -v
  ```

### 7.3 Decision recording

- **Check 7.3.1** — Every decision produces an audit event with `source` field (operator/policy:id/limit):
  ```bash
  rg "EventYieldDecided|yield.decided|DecisionSource" internal/audit/ internal/orchestrator/
  ```
- **Check 7.3.2** — Integration test: run with mixed policy + operator decisions; verify audit log counts:
  ```bash
  go test ./tests/e2e/ -run TestPolicyE2E -v
  ```

### 7.4 TUI

- **Check 7.4.1** — TUI snapshot tests exist:
  ```bash
  find internal/tui -name "*_test.go" | xargs grep -l "golden\|snapshot\|teatest" 2>/dev/null
  ```
- **Check 7.4.2** — Secret scrubbing runs before rendering:
  ```bash
  rg "Scrub|scrub.*Maybe|REDACTED" internal/tui/ internal/orchestrator/
  ```
  Any code path that displays `request_edit` content without going through a scrub = HIGH.

---

## 8. HTTP approval endpoint (if Phase 8 started)

- **Check 8.1** — `cmd/staircase/serve.go` and `internal/http/approval/` present. If absent, §8 is N/A.
- **Check 8.2** — Authentication required on all endpoints except `/v1/healthz`, `/v1/readyz`:
  ```bash
  rg "http\.Handle|mux\.Handle|router\.(Get|Post)" internal/http/approval/
  # Read each route; check for auth middleware
  ```
- **Check 8.3** — Bearer token never logged:
  ```bash
  rg "slog\.|log\.|fmt\.Print" internal/http/approval/ | rg -i "token|authorization"
  ```
  Any hit requires manual review; if the value is emitted, CRITICAL.
- **Check 8.4** — Default bind is `127.0.0.1`:
  ```bash
  rg "bind|Listen.*Addr|0\.0\.0\.0|127\.0\.0\.1" internal/http/approval/ cmd/
  ```
- **Check 8.5** — TLS config uses Mozilla intermediate suites:
  ```bash
  rg "tls\.Config|CipherSuites|MinVersion" internal/http/approval/
  ```
- **Check 8.6** — Decision race test: concurrent decides on same yield; one wins, other gets 409:
  ```bash
  go test ./internal/http/approval/ -run TestDecisionRace -v
  ```

---

## 9. Audit chain and checkpoints

### 9.1 Chain hash

- **Check 9.1.1** — Each event log row includes `event_hash` computed as `SHA-256(prev_hash || event_body)`:
  ```bash
  rg "event_hash|EventHash|chain.*hash" internal/audit/ internal/persistence/
  ```
- **Check 9.1.2** — A helper function recomputes and verifies the chain:
  ```bash
  rg "VerifyChain|chain.*verify|walkChain" internal/audit/
  ```
- **Check 9.1.3** — Tamper test: modify one event body; verification reports the exact break point:
  ```bash
  go test ./internal/audit/ -run TestChainTamperDetection -v
  ```

### 9.2 Signed checkpoints

- **Check 9.2.1** — Ed25519 signing key exists, separate from AES vault key:
  ```bash
  rg "ed25519|Ed25519" internal/audit/
  ls $STAIRCASE_DIR/.audit.key $STAIRCASE_DIR/.audit.pub 2>/dev/null
  ```
- **Check 9.2.2** — Checkpoints written to an append-only file outside SQLite:
  ```bash
  rg "O_APPEND|audit\.checkpoints" internal/audit/
  ```
- **Check 9.2.3** — `staircase audit verify` command present:
  ```bash
  ./staircase audit verify --help 2>&1 | head
  ```
- **Check 9.2.4** — Signature tamper test:
  ```bash
  go test ./internal/audit/ -run TestCheckpointSignatureTamper -v
  ```

### 9.3 Replay

- **Check 9.3.1** — `staircase replay <run_id>` command present.
- **Check 9.3.2** — Replay verifies audit first (refuses tampered run).
- **Check 9.3.3** — Replay does not make LLM calls (stubbed at module-load time):
  ```bash
  rg "replay|Replay" runner/staircase_runner/
  ```
- **Check 9.3.4** — Bit-identical replay test:
  ```bash
  go test ./tests/e2e/ -run TestReplayBitIdentical -v
  ```


---

## 10. Observability

### 10.1 Structured logging

- **Check 10.1.1** — `log/slog` used everywhere; no `fmt.Println`, `log.Print*` in non-CLI code:
  ```bash
  rg "fmt\.Print|log\.Print" internal/ --type go | rg -v _test.go
  ```
  Hits in non-test production code = LOW to MEDIUM per hit.
- **Check 10.1.2** — Redacting handler exists and is the default:
  ```bash
  rg "slog\.Handler|RedactingHandler|NewHandler" internal/obs/
  ```
- **Check 10.1.3** — Sensitive field redaction test: log a `Plaintext` value and a struct with field `token`; capture output; assert no leak:
  ```bash
  go test ./internal/obs/ -run TestRedaction -v
  ```

### 10.2 Metrics

- **Check 10.2.1** — Core metrics exposed (per §10 of plan):
  ```bash
  rg "staircase_yields_total|staircase_yield_latency|staircase_secret_access_total|staircase_ipc_messages_total|staircase_run_duration|staircase_active_runs|staircase_audit_chain_length" internal/obs/
  ```
  Each missing metric is INFO (if planned but not started) or MEDIUM (if Phase 10 is declared done).
- **Check 10.2.2** — No unbounded-cardinality labels. Inspect every label set; reject `run_id`, `user_id`, or any UUID-typed label:
  ```bash
  rg "prometheus\.NewCounterVec|NewHistogramVec|NewGaugeVec" internal/obs/ -A5 | rg "run_id|user_id|uuid"
  ```

### 10.3 Tracing

- **Check 10.3.1** — OpenTelemetry integration opt-in behind `--otel-endpoint`:
  ```bash
  rg "otel-endpoint|OTelEndpoint|OTEL_EXPORTER" internal/ cmd/
  ```
- **Check 10.3.2** — Trace context propagates into IPC messages. Schema includes `trace_context` field:
  ```bash
  rg "trace_context|traceparent" proto/ internal/ipc/
  ```

### 10.4 Run summary

- **Check 10.4.1** — Per-run summary written to `$STAIRCASE_DIR/runs/<id>/summary.{md,json}`:
  ```bash
  rg "summary\.md|summary\.json|RunSummary|WriteSummary" internal/obs/ internal/orchestrator/
  ```
- **Check 10.4.2** — Summary generation is deterministic (given same log, produces same output):
  ```bash
  go test ./internal/obs/ -run TestSummaryDeterministic -v
  ```

---

## 11. Plugin gates

Mark §11 N/A if Phase 11 not started.

- **Check 11.1** — `plugins/gates/` exists with at least one example plugin.
- **Check 11.2** — Plugin protocol documented in `docs/` with an input/output JSON schema.
- **Check 11.3** — Sandbox: subprocess env cleared, working dir is a fresh tempdir:
  ```bash
  rg "TempDir|Env = \[\]string|\"PATH=\"" internal/gate/
  ```
- **Check 11.4** — Plugin timeout enforced:
  ```bash
  rg "CommandContext.*gate|WithTimeout.*Plugin" internal/gate/
  ```
- **Check 11.5** — Malformed plugin output test: plugin prints garbage; gate reports parse error, does not crash:
  ```bash
  go test ./internal/gate/ -run TestPluginMalformedOutput -v
  ```
- **Check 11.6** — Plugin cannot access `$STAIRCASE_DIR`:
  ```bash
  go test ./internal/gate/ -run TestPluginIsolation -v
  ```

---

## 12. Cross-cutting: build, lint, security, tests

### 12.1 Build

- **Check 12.1.1** — `go build ./...` zero output.
- **Check 12.1.2** — `CGO_ENABLED=0 go build ./...` succeeds (pure Go binary):
  ```bash
  CGO_ENABLED=0 go build -o /tmp/staircase-pure ./cmd/staircase
  file /tmp/staircase-pure | grep -v "dynamically linked"
  ```
- **Check 12.1.3** — Reproducible build: two consecutive builds identical:
  ```bash
  export SOURCE_DATE_EPOCH=1700000000
  go build -trimpath -ldflags="-s -w -buildid=" -o /tmp/sc1 ./cmd/staircase
  sha256sum /tmp/sc1
  go build -trimpath -ldflags="-s -w -buildid=" -o /tmp/sc2 ./cmd/staircase
  sha256sum /tmp/sc2
  diff <(sha256sum /tmp/sc1 | awk '{print $1}') <(sha256sum /tmp/sc2 | awk '{print $1}')
  ```

### 12.2 Lint

- **Check 12.2.1** — `go vet ./...` clean.
- **Check 12.2.2** — `staticcheck ./...` clean.
- **Check 12.2.3** — `golangci-lint run` clean (with project's `.golangci.yaml`).
- **Check 12.2.4** — `gofumpt -l .` empty output.

### 12.3 Security

- **Check 12.3.1** — `gosec ./...` zero HIGH/CRITICAL:
  ```bash
  gosec -fmt=json -out=/tmp/gosec.json ./... 2>&1
  jq '.Issues | map(select(.severity == "HIGH" or .severity == "CRITICAL")) | length' /tmp/gosec.json
  ```
- **Check 12.3.2** — `govulncheck ./...` zero unfixed:
  ```bash
  govulncheck ./... 2>&1 | tail
  ```
- **Check 12.3.3** — No hardcoded credentials:
  ```bash
  rg -i "password\s*=\s*\"|api[_-]?key\s*=\s*\"|secret\s*=\s*\"" --type go -g '!*_test.go'
  ```

### 12.4 Tests

- **Check 12.4.1** — Race-clean: `go test -race ./... -count=1`.
- **Check 12.4.2** — Overall coverage ≥ 70%:
  ```bash
  go test -coverprofile=/tmp/cov.out ./... 2>&1
  go tool cover -func=/tmp/cov.out | tail -1
  ```
- **Check 12.4.3** — Per-critical-package coverage ≥ 85%: `internal/ipc/...`, `internal/secret/...`, `internal/audit/...`, `internal/orchestrator/...`:
  ```bash
  for pkg in internal/ipc internal/secret internal/audit internal/orchestrator; do
    go test -coverprofile=/tmp/pkg.cov ./$pkg/... 2>/dev/null
    pct=$(go tool cover -func=/tmp/pkg.cov 2>/dev/null | tail -1 | awk '{print $3}')
    echo "$pkg: $pct"
  done
  ```
- **Check 12.4.4** — Goroutine leak check present in tests:
  ```bash
  rg "goleak" internal/ --type go
  ```
- **Check 12.4.5** — E2E suite exists and passes:
  ```bash
  go test ./tests/e2e/... -count=1 -timeout=10m 2>&1 | tail
  ```

### 12.5 Dependencies

- **Check 12.5.1** — No indirect dependencies on CGO packages:
  ```bash
  go list -deps -f '{{if .CgoFiles}}{{.ImportPath}}{{end}}' ./... | head
  ```
  Any output = needs review.
- **Check 12.5.2** — `go mod tidy` is a no-op:
  ```bash
  go mod tidy
  git diff --exit-code -- go.mod go.sum
  ```
- **Check 12.5.3** — License compatibility: all deps are OSI-approved permissive (`MIT`, `Apache-2.0`, `BSD-*`, `ISC`, `MPL-2.0`):
  ```bash
  go-licenses report ./... 2>/dev/null | awk -F, '{print $3}' | sort -u
  ```
  Any `GPL`, `AGPL`, `SSPL`, `Commons Clause`, unknown = HIGH finding.

### 12.6 Documentation presence

- **Check 12.6.1** — `README.md` at repo root is non-trivial (> 50 lines, has install section, has usage section).
- **Check 12.6.2** — `docs/` directory exists with mkdocs config.
- **Check 12.6.3** — Every error code in `internal/errs/` has a docs page:
  ```bash
  # Extract error codes from errs package
  codes=$(rg "Code[A-Z][a-zA-Z]+\s+Code = \"" internal/errs/ -o | awk -F'"' '{print $2}' | sort -u)
  for c in $codes; do
    if ! grep -rq "$c" docs/ 2>/dev/null; then
      echo "Missing docs for: $c"
    fi
  done
  ```
  Any missing = LOW per code (sum them; 10+ missing = MEDIUM).

---

## 13. Out-of-scope observations (free-form)

If during the audit you notice something that is clearly wrong but is not covered by a specific check above, record it here with:

- File:line
- Observation (one sentence)
- Why it's not a finding (e.g., "outside audit scope" or "covered by a planned phase not yet started")

Do not fix. Do not silently expand the audit. These are flags for the plan author to decide on.

---

## 14. Incomplete areas

If any section could not be completed within its time budget, record:

- Section number
- What was covered
- What was not covered
- Why (tool missing, test hung, phase not started, time exhausted)

An incomplete area is not a failure of the audit — failing to declare it is.

---

## 15. Required commands that produce the report

Run these in order. The auditor may run intermediate commands as needed but must always end with these.

```bash
# 1. Record environment
{
  date -u +%Y-%m-%dT%H:%M:%SZ
  git log -1 --format='%H%n%D'
  go version
  python3 --version 2>&1
  uname -a
} > /tmp/audit_env.txt

# 2. Run the aggregate test pass for evidence (non-blocking for this step)
go test ./... -count=1 -short 2>&1 | tee /tmp/audit_test.log
go test -race ./... -count=1 -timeout=15m 2>&1 | tee /tmp/audit_race.log
go test -coverprofile=/tmp/audit_cov.out ./... 2>&1 | tee -a /tmp/audit_test.log
go tool cover -func=/tmp/audit_cov.out > /tmp/audit_cov_func.txt

# 3. Collect static analysis
go vet ./... 2>&1 | tee /tmp/audit_vet.txt
staticcheck ./... 2>&1 | tee /tmp/audit_staticcheck.txt
golangci-lint run --out-format=json 2>&1 | tee /tmp/audit_lint.json
gosec -fmt=json -out=/tmp/audit_gosec.json ./... 2>&1 || true
govulncheck ./... 2>&1 | tee /tmp/audit_vuln.txt

# 4. Python side (if present)
[ -d runner ] && {
  cd runner
  python3 -m pytest --tb=short 2>&1 | tee /tmp/audit_pytest.log
  mypy --strict staircase_runner/ 2>&1 | tee /tmp/audit_mypy.log
  ruff check staircase_runner/ 2>&1 | tee /tmp/audit_ruff.log
  bandit -r staircase_runner/ 2>&1 | tee /tmp/audit_bandit.log
  cd ..
}

# 5. Emit reports
# (populate /mnt/user-data/outputs/AUDIT_REPORT.md and audit_report.json)
```

---

## 16. Final self-check before emitting the report

Before you write the final report, verify each of the following. If any fails, fix the report before emitting.

- [ ] Every finding has a file:line citation OR a command output quoted.
- [ ] Every finding has a severity, and severities are assigned per the definitions in §1.
- [ ] No finding uses the words "might", "could", "possibly", or "likely" without a concrete reproduction attached.
- [ ] The summary counts at the top match the finding rows in the table.
- [ ] `Overall status` follows this rule: any CRITICAL → RED; any HIGH and no CRITICAL → YELLOW; only MEDIUM/LOW/INFO → GREEN; catastrophic build failure preventing most checks → BLOCKED.
- [ ] `audit_report.json` is valid JSON (pipe it through `jq .` before writing).
- [ ] `git status --porcelain` on the audited repo is empty.
- [ ] `audit_commands.log` lists every shell command run during the audit in the order run.
- [ ] Incomplete areas (§14) account for every check marked INCONCLUSIVE or N/A.
- [ ] No claim of "test passes" without the test's exit code being 0 in the captured output.

---

## 17. Anti-pattern detector (things the auditor must not do)

These are common AI-assistant failure modes. If you catch yourself doing any of them, stop and restart the affected section.

1. **Declaring PASS because "the code looks right."** Reading code is not running code. If a check says "run test X", run it.
2. **Merging multiple findings into one row** to keep the table short. Each distinct defect is its own ID.
3. **Inventing a finding to fill a section.** If §8 is N/A because Phase 8 hasn't started, §8 is N/A — do not synthesize findings from imagination.
4. **Quoting code you did not read.** If you cite a line number, you opened that file and saw that line. No exceptions.
5. **Writing aspirational severity.** Severity reflects what the code does today, not what it might do if someone exploited it in an elaborate scenario.
6. **Padding the executive summary with hedges.** The top 3 recommendations are three lines, each actionable today. "Improve observability" is not actionable; "Add `staircase_ipc_messages_total` counter (missing per check 10.2.1)" is.
7. **Skipping the machine-readable JSON.** The markdown report is for humans; the JSON is for diffing between audit runs. Both are required.
8. **Running commands as root.** If the environment requires root, note it and mark the affected checks INCONCLUSIVE.

---

*End of checklist. Expected runtime for a full audit on a well-developed codebase: 90–120 minutes. If the codebase is in early phases (before Phase 5 complete), many checks will be N/A and runtime will be proportionally shorter.*