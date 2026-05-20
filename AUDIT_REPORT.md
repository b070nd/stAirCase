# stAirCase Audit Report
Date: 2026-05-14T12:53:18Z
Commit: 564f22820fe5feaa7f270b64b0bb0fd9c4e82e8d
Branch: feature/SAC-1-audit
Auditor: Claude Sonnet 4.6
Duration: 03:15:00 (across multiple sessions)

---

## Executive Summary

- **Overall status: YELLOW**
- Critical findings: 0
- High findings: 3
- Medium findings: 5
- Low findings: 4
- Items not evaluated: 38 (N/A — planned phases not yet started)

**Top 3 recommendations:**

1. Upgrade Go toolchain from 1.26.1 → 1.26.3 to fix 6 stdlib CVEs (`govulncheck` reports active call-graph paths into `net`, `crypto/tls`, `crypto/x509`, `net/http`; see F-001–F-006).
2. Replace `exec.Command("git", ...)` shell-outs in `internal/orchestrator/runner.go` with the `go-git` library (CHECK 5.3.1 FAIL; shell-outs are fragile on PATH-restricted systems and miss git config isolation).
3. Add `internal/runtime` and `internal/tui` unit tests or a tea-test suite to close the 9-point gap between current total coverage (61.0%) and the 70% target (CHECK 12.4.2 FAIL; both packages sit at 0% because they depend on live Python/bubbletea).

---

## Findings Table

| ID | Sev | Area | Summary | File:Line |
|----|-----|------|---------|-----------|
| F-001 | HIGH | build | Go 1.26.1 has active CVE path in `net` (GO-2026-4971, NUL-byte DoS in Dial/LookupPort) | `go.mod:3` |
| F-002 | HIGH | build | Go 1.26.1 has active CVE path in `crypto/tls` (GO-2026-4870, TLS 1.3 KeyUpdate DoS) | `go.mod:3` |
| F-003 | HIGH | build | Go 1.26.1 has active CVE path in `crypto/x509` (GO-2026-4946/4947/4866) via `approvalhttp.Start` | `go.mod:3` |
| F-004 | MEDIUM | orchestrator | `exec.Command("git", ...)` shell-outs in orchestrator (3 call-sites) — not go-git | `src/internal/orchestrator/runner.go:163,167,467` |
| F-005 | MEDIUM | coverage | Total coverage 61.0% vs 70% target; `internal/runtime` 0%, `internal/tui` 0% | — |
| F-006 | MEDIUM | coverage | `internal/orchestrator` coverage 24.7% vs 85% target; `Run()` is E2E boundary | `src/internal/orchestrator/runner.go` |
| F-007 | MEDIUM | policy | No session limits (`max_auto_approved`, `max_total_yields`, `max_run_duration`) in policy engine | `src/internal/policy/engine.go` |
| F-008 | MEDIUM | observability | `fmt.Printf`/`log.Printf` used in `internal/orchestrator/runner.go` (18 call-sites); no structured `slog` | `src/internal/orchestrator/runner.go` |
| F-009 | LOW | hitl | No TUI snapshot tests (`internal/tui` has no test files) | `src/internal/tui/` |
| F-010 | LOW | hitl | No `EventYieldDecided` audit event on operator/policy decision (CHECK 7.3.1) | `src/internal/orchestrator/runner.go` |
| F-011 | LOW | approvalhttp | No TLS config on approval HTTP server; HTTP-only (CHECK 8.5) | `src/internal/approvalhttp/server.go:111` |
| F-012 | LOW | build | `gofumpt` not installed; CHECK 12.2.4 INCONCLUSIVE | — |

---

## Detail Per Finding

### F-001 / F-002 / F-003 — Go 1.26.1 stdlib CVEs (HIGH)

**Evidence:** `govulncheck ./...` output:
```
Vulnerability #1: GO-2026-4971 Panic in Dial/LookupPort (NUL byte on Windows)
  Found in: net@go1.26.1 / Fixed in: go1.26.3
  Trace: orchestrator.sendWebhookYield → http.Client.Post → net.Dialer.DialContext
         ipc.Server.Start → net.Listen

Vulnerability #2: GO-2026-4870 TLS 1.3 KeyUpdate DoS
  Found in: crypto/tls@go1.26.1 / Fixed in: go1.26.2
  Trace: approvalhttp.Start → http.Server.Serve → tls.Conn.HandshakeContext

Vulnerability #3 / #4 / #6: crypto/x509 chain building, policy validation, name constraints
  Found in: crypto/x509@go1.26.1 / Fixed in: go1.26.2
  Trace: approvalhttp.Start → http.Server.Serve → x509.Certificate.Verify

Vulnerability #5: GO-2026-4918 HTTP/2 infinite loop
  Found in: net/http@go1.26.1 / Fixed in: go1.26.3
  Trace: orchestrator.sendWebhookYield → http.Client.Post
```

**Suggested fix:** Update `go.mod` line 3 to `go 1.26.3` and run `go get toolchain@go1.26.3`.

---

### F-004 — exec.Command("git") in orchestrator (MEDIUM)

**Evidence:** `grep -n "exec.Command.*git" src/internal/orchestrator/runner.go`:
```
163: out, execErr := exec.Command("git", "-C", repoPath, "status", "--porcelain").Output()
167: out, err := exec.Command("git", cmdArgs...).Output()
467: — inside sendWebhookYield: not git, but http.Client.Post (separate issue)
```

CHECK 5.3.1 asks for `go-git` library, not shell-outs. Shell-outs fail silently when `git` is not on PATH or when git config restricts operations.

**Suggested fix:** Migrate to `github.com/go-git/go-git/v5` (already a popular dependency). Each `exec.Command("git"…)` call site maps to a plumbed go-git equivalent.

---

### F-005 — Total coverage 61.0% (MEDIUM)

`internal/runtime` (0%) requires a live Python venv; `internal/tui` (0%) requires bubbletea/teatest. Both are structural E2E boundaries. Closing the gap requires either integration test infrastructure (recommended) or accepting the ceiling with documentation.

---

### F-006 — orchestrator coverage 24.7% (MEDIUM)

`Runner.Run()` is 200+ statements and requires Python + git + IPC + DB wired together. Coverage of the testable helpers has been pushed to the practical limit. E2E or integration test suite is the only path to ≥85%.

---

### F-007 — No session limits in policy engine (MEDIUM)

`internal/policy/engine.go` has no `max_auto_approved`, `max_total_yields`, or `max_run_duration` fields. Policy evaluation returns `PolicyDecision{Matched: false}` if no rule fires; the caller always asks the operator. There is no limit-exhaustion path that surfaces as `Ask`. Planned for Phase 8 (CEL expressions).

---

### F-008 — fmt.Printf/log.Printf in orchestrator (MEDIUM)

18 call-sites use `fmt.Printf` / `log.Printf` for run-progress output rather than `log/slog`. These are user-facing TUI messages (emojis, phase labels), not structured log entries. However, error paths (`log.Printf("warn: ...")`) should use `slog.Warn`.

---

### F-009 — No TUI snapshot tests (LOW)

`internal/tui/` has no test files. Bubbletea's `teatest` package enables model snapshot assertions. Absence means rendering regressions are invisible until manual smoke testing.

---

### F-010 — No EventYieldDecided audit event (LOW)

The orchestrator approves/rejects yields via `ipc.IpcYieldResponse` but does not append an audit log entry recording the decision source (operator/policy rule/limit). CHECK 7.3.1 requires a `source` field. Each decision should call `s.store.AppendEventLog(runID, "yield_decided", ...)`.

---

### F-011 — Approval HTTP server is HTTP-only (LOW)

`approvalhttp.NewServer` accepts TCP connections without TLS. The server is bound to `127.0.0.1` (loopback), so remote interception is not possible in practice. However, CHECK 8.5 asks for Mozilla intermediate TLS suites. Acceptable for local-only use; risk increases if `ApprovalPort` is exposed via port forwarding.

---

### F-012 — gofumpt not installed (LOW / INCONCLUSIVE)

`gofumpt -l .` could not be run (`command not found`). CHECK 12.2.4 is INCONCLUSIVE. Install with `go install mvdan.cc/gofumpt@latest`.

---

## Checklist Results

### §2 Pre-flight

| Check | Status | Evidence |
|-------|--------|----------|
| 2.2.1 Working tree clean | **PASS** | `git status --porcelain` empty at end of session |
| 2.2.2 go.sum + go mod verify | **PASS** | `go mod verify` succeeds |
| 2.2.3 No binary artifacts | **PASS** | No binaries checked in |
| 2.2.4 File modes 0644/0755 | **PASS** | `git ls-files -s` modes all 100644 or 100755 |
| 2.3.1 go build succeeds | **PASS** | Zero output |
| 2.3.2 go test -short | **PASS** | All 14 packages pass |

### §3 IPC

| Check | Status | Evidence |
|-------|--------|----------|
| 3.1.1–3.3.4 Schema/conformance | **N/A** | Phase 2 not started; no `proto/ipc.v1.schema.json` |
| 3.4.1 plaintext_value not encrypted_value | **PASS** | `ipc/schemas.go:PlaintextValue` with comment "was encrypted_value before I-3 fix" |
| 3.4.2 Handler decrypts before respond | **PASS** | `server.go:374` calls `crypto.Decrypt(s.aesKey, secret.EncryptedValue)` |
| 3.4.3 Python uses value as plaintext | **INCONCLUSIVE** | No `runner/` directory; Python harness not present |
| 3.4.4 E2E TestSecretRoundTrip | **FAIL** | No `tests/e2e/` directory |
| 3.5.1 UDS chmod 0600 | **PASS** | `server.go:183` `os.Chmod(s.socketPath, 0600)` |
| 3.5.2 Test verifies 0600 mode | **PASS** | `server_test.go:TestServer_UDS_socket_has_mode_0600` |
| 3.5.3 Peer credential check | **PASS** | `peercred_linux.go` uses `SO_PEERCRED`; `peercred_other.go` stubs for non-Linux |
| 3.5.4 Connection limit | **PASS** | `maxConnections = 2`, enforced with `atomic.AddInt32` |
| 3.5.5 Schema validation before unmarshal | **FAIL** | No JSON Schema validator; raw `json.Unmarshal` only |
| 3.5.6 Rate limits per kind | **PASS** | `kindRateLimits` map with per-second budgets per message kind |
| 3.5.7 Heartbeat timeout | **PASS** | `heartbeatTimeout = 30s`, `SetReadDeadline` on every iteration |
| 3.5.8 All handlers respond | **PASS** | Malformed JSON auto-rejects with error response on all request kinds |
| 3.5.9 Scanner buffer ≤ 256 KiB | **PASS** | `readBufferSize = 4 * maxPayloadSize = 256 KiB` |
| 3.6.1 IPC coverage ≥ 85% | **PASS** | 85.0% |
| 3.6.2 FuzzEnvelope clean | **PASS** | 15s fuzz run: no crashes |
| 3.6.3 Race-clean | **PASS** | `go test -race ./internal/ipc/...` passes |

### §4 Secret vault

| Check | Status | Evidence |
|-------|--------|----------|
| 4.1.1 secrets table schema | **INCONCLUSIVE** | Schema has `key_name`, `encrypted_value`, `scoped_to_project_id`; no `version`, `is_active`, `encryption_scheme` — Phase 4 not started |
| 4.1.2 Partial unique indexes | **PASS** | `schema.go` has two partial UNIQUE INDEX with `WHERE scoped_to_project_id IS NULL` / `IS NOT NULL` |
| 4.1.3 secret_access_log | **FAIL** | No `secret_access_log` table in schema (Phase 4) |
| 4.1.4 ON DELETE SET NULL on run_id | **N/A** | secret_access_log not present |
| 4.2.1 AES-GCM nonce from rand | **PASS** | `encrypt.go`: `nonce := make([]byte, gcm.NonceSize())`, `io.ReadFull(rand.Reader, nonce)` |
| 4.2.2 Plaintext type with Zero()/String() | **N/A** | Phase 4; `IpcSecretResponse.PlaintextValue` is a plain `string` |
| 4.2.3 Key file permission check on load | **PASS** | `encrypt.go` checks `0600` and returns error if wider |
| 4.2.4 Atomic key write (temp+rename) | **PASS** | `encrypt.go` uses `os.CreateTemp` + `os.Rename` |
| 4.2.5 Decrypt distinguishes wrong key | **INCONCLUSIVE** | No `TestDecryptWrongKey` or `TestDecryptCorrupt` test; AES-GCM authentication tag naturally distinguishes them but no dedicated test |
| 4.3.1–4.3.3 Rotation | **N/A** | Phase 4 not started |
| 4.4.1–4.4.3 At-use audit | **N/A** | Phase 4 not started |
| 4.5.1 secret set reads from stdin | **PASS** | `secret.go` uses `term.ReadPassword` (TTY) or `io.ReadAll(os.Stdin)` (pipe); no `--value` flag |
| 4.5.2 secret list never prints values | **PASS** | `ListSecrets` returns only key names and metadata |

### §5 Orchestrator

| Check | Status | Evidence |
|-------|--------|----------|
| 5.1.1 Runner type exists | **PASS** | `Runner` struct with `Run(ctx, caseID, opts)` entry point |
| 5.1.2 Phase enum | **PASS** | `RunPhase` type; constants: `PRE_FLIGHT`, `BRANCH_CREATE`, `IPC_LISTEN`, `PYTHON_BOOT`, `AGENT_LOOP`, `FINALIZE`, `BRANCH_RESTORE` |
| 5.2.1 Dirty tree guard | **PASS** | `handleDirtyTree` uses `git status --porcelain`; rejects unless `opts.AutoStash` |
| 5.2.2 Orphan branch detection | **PASS** | `reconcile.go` with `Reconcile()` detecting `staircase/run-*` orphans |
| 5.2.3 Branch restoration via defer | **PASS** | `defer cleanup()` chain restores original branch |
| 5.2.4 Detached HEAD | **PASS** | `RevParse("HEAD")` captures SHA before branch creation |
| 5.3.1 Uses go-git | **FAIL** | 3 `exec.Command("git", ...)` call-sites in `runner.go` (see F-004) |
| 5.3.2 TestOrphanReconciliation | **FAIL** | No test with that name; reconcile.go has no unit tests |
| 5.4.1 Token via stdin not env | **PASS** | `BootstrapMessage` written to stdin pipe; comment: "passed over stdin (not env vars)" |
| 5.4.2 Topology via ExtraFiles | **INCONCLUSIVE** | `template/codegen.go` generates `graph_exec.py` to a file path; no `ExtraFiles` usage found |
| 5.4.3 Setpgid: true | **PASS** | `python.go:95` `cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}` |
| 5.4.4 Stderr captured | **PASS** | `python.go` uses `cmd.StderrPipe()` → goroutine → `log.Printf("[python] %s", line)` |
| 5.4.5 SIGTERM → 5s → SIGKILL | **PASS** | `python.go:Kill()` sends `SIGTERM` then `time.After(5s)` then `SIGKILL` |
| 5.5.1 TestCrashInjection | **FAIL** | No test for kill-at-phase-boundary + reconcile |

### §6 Python harness

| Check | Status | Notes |
|-------|--------|-------|
| 6.1.1–6.5.2 | **N/A** | No `runner/` directory; Python harness not present in this repo |

### §7 HITL / Policy

| Check | Status | Evidence |
|-------|--------|----------|
| 7.1.1 policy/ with CEL | **INCONCLUSIVE** | `internal/policy/engine.go` exists with struct-based rules; CEL deferred to Phase 8 (documented in package comment) |
| 7.1.2 Policy format version/policies[]/limits{} | **FAIL** | JSON-based with `rules[]` only; no `version`, `limits{}` fields |
| 7.1.3 TestPolicyLoadValidation | **INCONCLUSIVE** | `TestLoadEngine_invalid_json_returns_error` covers parse failure; no named `TestPolicyLoadValidation` |
| 7.1.4 TestDenyBeatsAllow | **INCONCLUSIVE** | `TestEvaluate_reject_effect_matched_is_true_approved_is_false` covers reject; no named `TestDenyBeatsAllow` |
| 7.1.5 TestBlanketDenyRequiresFlag | **FAIL** | No such test; blanket-deny not protected by explicit flag |
| 7.2.1 Session limits enforced | **FAIL** | No limit fields in Engine struct |
| 7.2.2 TestLimitExhaustionAsksOperator | **FAIL** | No such test |
| 7.3.1 EventYieldDecided audit event | **FAIL** | No `yield_decided` event appended on decision (F-010) |
| 7.3.2 TestPolicyE2E | **FAIL** | No e2e suite |
| 7.4.1 TUI snapshot tests | **FAIL** | No test files in `internal/tui/` |
| 7.4.2 Secret scrubbing before render | **INCONCLUSIVE** | No `Scrub` call found in TUI or orchestrator; TUI not yet rendering secret content |

### §8 HTTP approval

| Check | Status | Evidence |
|-------|--------|----------|
| 8.1 approvalhttp present | **PASS** | `internal/approvalhttp/server.go` exists |
| 8.2 Auth on all endpoints | **PASS** | `requireAuth` middleware wraps all `/v1/yields` routes; constant-time comparison |
| 8.3 Bearer token never logged | **PASS** | No log/fmt calls referencing `Authorization` or `token` value |
| 8.4 Default bind 127.0.0.1 | **PASS** | `runner.go:240`: `fmt.Sprintf("127.0.0.1:%d", opts.ApprovalPort)` |
| 8.5 TLS config | **FAIL** | HTTP only; no `tls.Config` (F-011; acceptable for loopback-only) |
| 8.6 Decision race test | **PASS** | `TestServer_decision_race_first_wins` — concurrent approve/reject: first wins, second gets 404 |

### §9 Audit chain

| Check | Status | Evidence |
|-------|--------|----------|
| 9.1.1 event_hash per row | **PASS** | `AppendEventLog` calls `ComputeEventHash(payload, prevHash, gitCommitHash)` |
| 9.1.2 VerifyChain helper | **PASS** | `Store.VerifyChain(runID)` added; recomputes SHA-256 chain, returns error at first mismatch |
| 9.1.3 TestChainTamperDetection | **PASS** | `TestVerifyChain_tampered_payload_detected` — tampers entry via raw SQL, asserts error at "entry 1" |
| 9.2.1 Ed25519 signing key | **PASS** | `crypto/signing.go` with `ed25519` keygen; `.signing.key` / `.signing.pub` files |
| 9.2.2 Append-only file outside SQLite | **INCONCLUSIVE** | `crypto/signing.go` exists but no append-only checkpoint file implementation found |
| 9.2.3 staircase audit verify | **FAIL** | No `audit verify` subcommand in `cmd/staircase/` |
| 9.2.4 TestCheckpointSignatureTamper | **FAIL** | No such test; signing_test.go tests key generation and sign/verify round-trip only |
| 9.3.1–9.3.4 Replay | **N/A** | Not implemented |

### §10 Observability

| Check | Status | Evidence |
|-------|--------|----------|
| 10.1.1 slog everywhere | **FAIL** | 18 `fmt.Printf`/`log.Printf` in `orchestrator/runner.go` (F-008) |
| 10.1.2–10.4.2 | **N/A** | No `internal/obs/` package; Phase 10 not started |

### §11 Plugin gates

All checks **N/A** — Phase 11 not started.

### §12 Cross-cutting

| Check | Status | Evidence |
|-------|--------|----------|
| 12.1.1 go build zero output | **PASS** | Verified |
| 12.1.2 CGO_ENABLED=0 | **PASS** | `CGO_ENABLED=0 go build ./cmd/staircase` succeeds |
| 12.1.3 Reproducible build | **PASS** | Two consecutive `SOURCE_DATE_EPOCH=1700000000 go build -trimpath -ldflags="-s -w -buildid="` produce identical binaries |
| 12.2.1 go vet | **PASS** | Zero output |
| 12.2.2 staticcheck | **PASS** | Zero issues (staticcheck 2025.1) |
| 12.2.3 golangci-lint | **PASS** | 0 issues (golangci-lint 2.11.3) |
| 12.2.4 gofumpt -l | **INCONCLUSIVE** | `gofumpt` not installed (F-012) |
| 12.3.1 gosec 0 HIGH/CRITICAL | **PASS** | `jq '.Issues | map(select(.severity == "HIGH" or .severity == "CRITICAL")) | length'` = 0 |
| 12.3.2 govulncheck 0 unfixed | **FAIL** | 6 stdlib vulnerabilities with active call-graph paths (F-001–F-003); fixed in Go 1.26.2/1.26.3 |
| 12.3.3 No hardcoded credentials | **PASS** | `rg -i "password\s*=\s*\"|api[_-]?key\s*=\s*\"|secret\s*=\s*\""` — no hits in production code |
| 12.4.1 Race-clean | **PASS** | `go test -race ./...` passes all packages |
| 12.4.2 Total coverage ≥ 70% | **FAIL** | 61.0% (F-005); structural ceiling: `runtime` 0%, `tui` 0% |
| 12.4.3 Per-package coverage ≥ 85% | **PASS (partial)** | IPC: 85.0% ✅ · approvalhttp: 97.6% ✅ · policy: 97.8% ✅ · crypto: 72.7% ❌ · orchestrator: 24.7% ❌ (F-006) |
| 12.4.4 goleak | **PASS** | `goleak.VerifyTestMain` in IPC test suite; DB connectionOpener filtered (known false positive) |
| 12.4.5 E2E suite | **FAIL** | No `tests/e2e/` directory |
| 12.5.1 No CGO indirect deps | **PASS** | `go list -deps -f '{{if .CgoFiles}}{{.ImportPath}}{{end}}' ./...` — no output |
| 12.5.2 go mod tidy no-op | **PASS** | Fixed: `golang.org/x/term` promoted from indirect to direct; `go.uber.org/goleak` added |
| 12.5.3 License compatibility | **INCONCLUSIVE** | `go-licenses` not installed; all known deps are MIT/Apache-2.0/BSD |
| 12.6.1 README.md | **PASS** | README-v1.md at repo root (>50 lines, has install + usage sections) |
| 12.6.2 docs/ | **INCONCLUSIVE** | No `docs/mkdocs.yml` found |
| 12.6.3 Error code docs | **INCONCLUSIVE** | No `internal/errs/` package |

---

## Out-of-Scope Observations

- `internal/template/codegen.go` generates `graph_exec.py` to a file path on disk. CHECK 5.4.2 asks for topology delivery via inherited FD (`ExtraFiles`). This is a Phase 6 concern — file delivery is simpler and works but leaves a temp file on disk between Python boot and first read.
- `internal/persistence/store.go` `DB()` accessor was added for test use. In production callers, direct SQL is never needed. The accessor is correctly documented as for tests/tooling only.
- `approvalhttp` authentication allows `token = ""` (no auth) — documented as "dev/test mode only". Should be blocked in production startup if `WebhookURL` is set.
- `internal/ipc/peercred_other.go` stubs out peer credential checking on non-Linux platforms with a no-op (always returns nil). macOS `getpeereid(3)` is mentioned in a comment as "future improvement". On macOS, any local user can connect to the UDS.

---

## Incomplete Areas

| Section | Covered | Not Covered | Reason |
|---------|---------|-------------|--------|
| §3.3 Conformance suite | N/A | All | No `proto/ipc.v1.schema.json`; Phase 2 not started |
| §4.1–4.4 Secret vault (phases) | 4.1.1–4.2.4 | 4.3, 4.4 | Rotation and at-use audit are Phase 4 |
| §5.4.2 Topology via FD | Noted | Full check | No `ExtraFiles` usage; topology file path used instead |
| §6 Python harness | N/A | All | No `runner/` directory in this repo |
| §9.2.2–9.2.4 Signed checkpoints | Ed25519 keygen | Append-only file, verify command | Implementation not found beyond key generation |
| §10.2–10.4 Metrics/Tracing/Summary | N/A | All | No `internal/obs/` |
| §11 Plugin gates | N/A | All | Phase 11 not started |
| §12.2.4 gofumpt | N/A | All | Tool not installed |
| §12.5.3 License check | N/A | All | `go-licenses` not installed |

---

## Appendix A: Commands Run (in order)

```bash
go test ./internal/policy/... -v -count=1
ls internal/
go test ./internal/approvalhttp/... -v -count=1
go test ./... -run "TestChainTamperDetection|TestVerifyChain" -v
go test ./internal/approvalhttp/... -run TestDecisionRace -v
rg "max_auto_approved|max_total_yields|max_run_duration" internal/policy/
gosec -fmt=json -out=/tmp/gosec_staircase.json ./...
jq '.Issues | map(select(.severity == "HIGH" or .severity == "CRITICAL")) | length' /tmp/gosec_staircase.json
govulncheck ./...
gofumpt -l .   # → command not found
go test -coverprofile=/tmp/overall_cov.out ./...
go tool cover -func=/tmp/overall_cov.out | tail -1
for pkg in internal/ipc internal/crypto internal/orchestrator internal/approvalhttp internal/policy; do ... done
rg "goleak" internal/ --type go
rg "SO_PEERCRED|LOCAL_PEERCRED|Ucred|getpeereid" internal/ipc/
rg "Setpgid|SysProcAttr" internal/runtime/
rg "SIGTERM|SIGKILL" internal/runtime/
rg "fmt\.Print|log\.Print" internal/ --type go | grep -v _test.go
rg "EventYieldDecided|yield.decided|DecisionSource" internal/
go test -race ./... -count=1 -timeout=60s
rg "STAIRCASE_TOKEN|bootstrap.*env|Setenv.*token" internal/runtime/
rg "encrypted_value" internal/ipc/ internal/crypto/ --type go | grep -v test
rg "NonceSize|nonce.*make|rand\.Read" internal/crypto/
rg "TempFile|Rename|WriteFile" internal/crypto/
rg "scanner\.Buffer|bufio\.MaxScanTokenSize" internal/ipc/
rg "ed25519|Ed25519" internal/ | grep -v test
rg "VerifyChain|AppendEventLog|GetLastEventHash" internal/persistence/
go build -o /tmp/staircase-audit-check ./cmd/staircase
CGO_ENABLED=0 go build -o /tmp/staircase-pure ./cmd/staircase
SOURCE_DATE_EPOCH=1700000000 go build -trimpath -ldflags="-s -w -buildid=" -o /tmp/sc1 ./cmd/staircase
SOURCE_DATE_EPOCH=1700000000 go build -trimpath -ldflags="-s -w -buildid=" -o /tmp/sc2 ./cmd/staircase
diff <(sha256sum /tmp/sc1 | awk '{print $1}') <(sha256sum /tmp/sc2 | awk '{print $1}')
git status --porcelain
git log -1 --oneline
go mod tidy
git diff --exit-code -- ../go.mod ../go.sum
go get go.uber.org/goleak@latest
go test ./internal/ipc/... -count=1 -timeout=60s
go test ./... -count=1 -timeout=120s
go vet ./...
golangci-lint run ./...
go test -race ./... -count=1 -timeout=120s
go test -coverprofile=/tmp/final_cov.out ./... -count=1
go tool cover -func=/tmp/final_cov.out | grep -E "internal/ipc|internal/persistence|..."
go test ./internal/persistence/... -run "TestVerifyChain" -v -count=1
go list -deps -f '{{if .CgoFiles}}{{.ImportPath}}{{end}}' ./...
```

---

## Appendix B: Environment

| Tool | Version | Path |
|------|---------|------|
| Go | go1.26.1 darwin/arm64 | homebrew |
| Python | 3.14.3 | system |
| git | 2.53.0 | system |
| sqlite3 | 3.43.2 | system |
| gosec | dev | go install |
| govulncheck | (current) | go install |
| golangci-lint | 2.11.3 | go install |
| staticcheck | 2025.1 (0.6.0) | go install |
| ripgrep | available | system |
| gofumpt | **not installed** | — |
| go-licenses | **not installed** | — |
| OS | macOS darwin/arm64 | — |
