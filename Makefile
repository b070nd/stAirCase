.PHONY: check test test-conformance test-integration test-e2e test-ci race build lint coverage vuln demo

# ─── The gate: every change must pass this locally and in CI ─────────────────
# Full tests (no -short: includes the subprocess runner tests), race, Python,
# CLI smoke tests, the offline demo, formatting, tidy, and schema-copy drift.
check: lint
	@test -z "$$(gofmt -l src tests)" || { gofmt -l src tests; echo "✗ gofmt needed"; exit 1; }
	go mod tidy -diff
	GOOS=windows go build ./...
	cmp proto/ipc.v1.schema.json src/internal/ipc/ipc.v1.schema.json
	go test ./... -count=1 -timeout=600s
	$(MAKE) race
	cd runner && python3 -m pytest tests -q
	bats tests/integration.bats
	$(MAKE) demo

# ─── Core unit tests ──────────────────────────────────────────────────────────
test:
	go test ./src/internal/... -count=1 -short

# ─── L1 conformance (Go + Python) ─────────────────────────────────────────────
test-conformance:
	go test ./tests/conformance/... -run TestConformance -v -count=1
	cd runner && python3 -m pytest tests/test_conformance.py -v

# ─── L3 integration ───────────────────────────────────────────────────────────
test-integration:
	go test -tags=integration ./src/... -count=1

# ─── L4 E2E (subprocess-level) ────────────────────────────────────────────────
test-e2e:
	go test ./src/internal/orchestrator/ -run "TestRun_integration" -v -count=1 -timeout=120s

# ─── Full CI: unit + conformance + audit-named checks ─────────────────────────
test-ci: test test-conformance
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
# Runs the full HITL + approval-content-binding + audit-chain walkthrough with a
# stub agent. --auto approves via curl so it is non-interactive in CI.
demo:
	./demo/run-demo.sh --auto
	./demo/run-demo.sh --auto --tamper
