# Contributing to stAirCase

Thanks for your interest. stAirCase is a security-sensitive control plane, so the
bar for changes is "every line traces to a reason, and security-relevant behavior
is proven by a test."

## Getting set up

Requirements: Go 1.26+ and `git`. Then:

```bash
go build ./...          # build everything
make demo               # the offline HITL + audit walkthrough (no API key)
```

## Before you open a PR

Run the same gate CI runs (needs `golangci-lint` and `bats`):

```bash
make check                   # lint, gofmt, tidy, Windows build, full + race tests, bats, demo (3 modes)
GOOS=windows go build ./...  # the Windows build must keep compiling
```

## Project norms

- **Every fix lands with a test.** Bug fixes get a regression test that fails
  before and passes after; security fixes get an adversarial test that proves the
  attack is blocked. See `docs/testing.md`.
- **The Go control plane is the trust boundary.** Security decisions
  (approval, path sandboxing, secret delivery, audit logging) belong in Go, never
  delegated to agent tools: a tool may only apply what the orchestrator
  approved. See `SECURITY.md`.
- **Surgical changes.** Touch only what your change requires; match the
  surrounding style. Don't reformat or refactor unrelated code in the same PR.
- **Conventional commits.** Follow the existing history, e.g.
  `fix(orchestrator): …`, `feat(audit): …`, `docs: …`. Co-authorship and a clear
  body explaining the *why* are appreciated.
- **No new dependencies without discussion.** `go.mod` is the supply-chain
  surface — model clients use the standard library, and it should stay that way.

## Reporting security issues

Do not use public issues for vulnerabilities — see [SECURITY.md](SECURITY.md).

## Conduct

By participating you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).
