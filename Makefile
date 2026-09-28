.PHONY: check test test-integration test-e2e test-ci race build lint coverage vuln demo smoke smoke-claude smoke-codex fuzz

# ─── The gate: every change must pass this locally and in CI ─────────────────
# Full tests (no -short: includes the run integration tests), race, CLI smoke
# tests, the offline demo, formatting, tidy and the Windows build.
check: lint
	@test -z "$$(gofmt -l src tests demo)" || { gofmt -l src tests demo; echo "✗ gofmt needed"; exit 1; }
	go mod tidy -diff
	GOOS=windows go build ./...
	go test ./... -count=1 -timeout=600s
	$(MAKE) race
	bats tests/integration.bats
	$(MAKE) demo

# ─── Core unit tests ──────────────────────────────────────────────────────────
test:
	go test ./src/internal/... -count=1 -short

# ─── L3 integration ───────────────────────────────────────────────────────────
test-integration:
	go test ./src/... -count=1  # everything, including the run integration tests -short skips

# ─── L4 E2E (subprocess-level) ────────────────────────────────────────────────
test-e2e:
	go test ./src/internal/orchestrator/ -run "TestRun_integration" -v -count=1 -timeout=120s

# ─── Full CI: unit + audit-named checks ───────────────────────────────────────
test-ci: test
	go test ./src/internal/persistence/ -run "TestSecretRoundTrip|TestAtUseAuditCounts" -v -count=1
	go test ./src/internal/crypto/ -run "TestPlaintextRedaction" -v -count=1
	go test ./src/internal/orchestrator/ -run "TestCrashInjection|TestKillAtPhase|TestCleanupChainOnPanic" -v -count=1 -short

# ─── Race detector ────────────────────────────────────────────────────────────
race:
	go test -race ./src/internal/... -count=1

# ─── Build ────────────────────────────────────────────────────────────────────
build:
	go build ./...

# ─── Coverage (without -short so integration tests are included) ──────────────
# Use this target for the audit coverage check; 'make test' skips integration
# tests with -short, which drops orchestrator coverage from 85% to 61%.
coverage:
	go test ./src/internal/... -count=1 -cover -timeout=120s 2>&1 | tee /tmp/staircase_cov_summary.txt
	@echo "--- coverage summary ---"
	@grep -E "coverage:" /tmp/staircase_cov_summary.txt | sort

# ─── Vulnerability scan ───────────────────────────────────────────────────────
vuln:
	@which govulncheck >/dev/null 2>&1 || go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./...

# ─── Static analysis ──────────────────────────────────────────────────────────
# golangci-lint runs govet, staticcheck, errcheck, ineffassign and unused with
# .golangci.yaml. Missing or failing linter fails the target: a silently
# skipped linter is not a gate.
lint:
	golangci-lint run ./...

# ─── Offline demo (no API key) ────────────────────────────────────────────────
# The offline end-to-end acceptance run (blueprint → bind → run → approvals →
# commit → checkout untouched → stories accepted → audit verified) on the real
# runtime with a stand-in model; plus the tamper and drift paths. --auto
# approves via curl so it is non-interactive in CI.
demo:
	./demo/run-demo.sh --auto
	./demo/run-demo.sh --auto --tamper
	./demo/run-demo.sh --auto --drift

# ─── Fuzzing the trusted core ─────────────────────────────────────────────────
# `go test` (and check) runs the seeds; this explores. A failing input is saved
# under testdata/fuzz/ - commit it as a regression test.
FUZZTIME ?= 1m
fuzz:
	@for t in FuzzDerive FuzzCleanApprovedPath FuzzUniversalNewlines; do \
		go test ./src/internal/orchestrator -run '^$$' -fuzz "^$$t\$$" -fuzztime $(FUZZTIME) || exit 1; \
	done

# ─── Real-agent smoke runs (opt-in: cost money, need credentials) ────────────
# Skipped with a message when the credentials are missing. Not part of check.
smoke:
	./demo/smoke.sh model
smoke-claude:
	./demo/smoke.sh claude
smoke-codex:
	./demo/smoke.sh codex
