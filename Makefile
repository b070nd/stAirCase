.PHONY: test test-conformance test-integration test-e2e test-ci race build lint coverage vuln

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
lint:
	go vet ./...
	@which staticcheck >/dev/null 2>&1 && staticcheck ./... || true
