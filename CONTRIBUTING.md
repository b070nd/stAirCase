# Contributing to stAirCase

Thanks for your interest. stAirCase is a security-sensitive control plane, so the
bar for changes is "every line traces to a reason, and security-relevant behavior
is proven by a test."

## Getting set up

Requirements: Go 1.26+, `git`, and `python3` (only for the integration/demo tests
that spawn a runtime). Then:

```bash
go build ./...          # build everything
make demo               # the offline HITL + audit walkthrough (no API key)
```

## Before you open a PR

Run the same checks CI runs:

```bash
make test-ci                 # unit + conformance + audit checks
make race                    # race detector (Linux/macOS)
GOOS=windows go build ./...  # the Windows build must stay green
gofmt -l src/                # formatting (should print nothing for files you touched)
go vet ./...
```

For a change that touches the demo or runtime end to end:

```bash
make demo                    # must exit 0
```

## Project norms

- **Every fix lands with a test.** Bug fixes get a regression test that fails
  before and passes after; security fixes get an adversarial test that proves the
  attack is blocked. See `docs/testing.md`.
- **The Go control plane is the trust boundary.** Security decisions
  (approval, path sandboxing, secret delivery, audit logging) belong in Go, never
  delegated to the Python runtime. See `SECURITY.md` and `docs/architecture.md`.
- **Surgical changes.** Touch only what your change requires; match the
  surrounding style. Don't reformat or refactor unrelated code in the same PR.
- **Conventional commits.** Follow the existing history, e.g.
  `fix(orchestrator): …`, `feat(audit): …`, `docs: …`. Co-authorship and a clear
  body explaining the *why* are appreciated.
- **No unhashed dependencies, no new external runtime deps without discussion.**
  The Python `requirements.txt` is the supply-chain surface; keep it tight.

## Reporting security issues

Do not use public issues for vulnerabilities — see [SECURITY.md](SECURITY.md).

## Conduct

By participating you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).
