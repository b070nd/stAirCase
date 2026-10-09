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
  attack is blocked. See [docs/testing.md](docs/testing.md).
- **The Go control plane is the trust boundary.** Security decisions
  (approval, path sandboxing, secret delivery, audit logging) belong in Go, never
  delegated to agent tools: a tool may only apply what the orchestrator
  approved. See [SECURITY.md](SECURITY.md).
- **Surgical changes.** Touch only what your change requires; match the
  surrounding style. Don't reformat or refactor unrelated code in the same PR.
- **Conventional commits.** Follow the existing history, e.g.
  `fix(orchestrator): …`, `feat(audit): …`, `docs: …`, with a body that explains
  the *why*.
- **No new dependencies without discussion.** `go.mod` is the supply-chain
  surface - model clients use the standard library, and it should stay that way.

## Writing documentation

The docs are for people who have never seen stAirCase. [docs/README.md](docs/README.md)
lists every page.

- **Plain English.** Short sentences, common words, one idea per paragraph. Explain
  a term the first time you use it, or link to [Concepts](docs/concepts.md).
- **Every claim is true today.** Check a behaviour in the code or by running the
  command before you describe it; show commands the reader can copy.
- **Say what can go wrong**, especially anything that sends data off the machine
  or cannot be undone.
- **The CLI reference is generated.** After changing a command's help text, run
  `UPDATE_DOCS=1 go test ./src/cmd/staircase -run TestCLIReference`. `make check`
  fails if [docs/cli.md](docs/cli.md) is out of date or a relative link points
  nowhere.

## Releasing (maintainers)

1. Move the `[Unreleased]` entries in `CHANGELOG.md` under a new
   `## [X.Y.Z] - date` heading; it becomes the release notes. In the same change set the
   version in the pinned Action example in `docs/audit.md` and the default of
   `demo/ruleset-fixture.sh` to `vX.Y.Z` (a test fails until they name it).
2. Merge to `master` with CI green, then tag from `master`:
   `git tag -a vX.Y.Z -m "stAirCase vX.Y.Z" && git push origin vX.Y.Z`.
3. The release workflow builds the archives reproducibly, signs the
   checksums (cosign), attaches SBOMs and attests build provenance.
4. Update the Homebrew tap with `packaging/homebrew-formula.sh` from the
   release's `checksums.txt`.

Versions follow [Semantic Versioning](https://semver.org/). A breaking change to one of the six
interfaces in [compatibility](docs/compatibility.md) needs a new major version (or a new format version accepted alongside the old
one); everything else may change in a minor version, listed under "Changed".

## Reporting security issues

Do not use public issues for vulnerabilities - see [SECURITY.md](SECURITY.md).

## Conduct

By participating you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).
