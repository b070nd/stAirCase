# Testing

`make check` is the gate: every change passes it locally and in CI
(`.github/workflows/ci.yml` runs it on Ubuntu and macOS).

In CI (and in the release workflow's test step) the command runs through
`packaging/test-report.sh`, which passes its output and exit status through and, when
it fails, adds an error annotation that names the failing tests. A job log can be read
only when signed in; an annotation is served by the public check-runs API, so a failure
on a runner can be diagnosed without one.

| Stage of `make check` | What it catches |
|---|---|
| `golangci-lint run` | govet, staticcheck, errcheck, ineffassign, unused (`.golangci.yaml`) |
| `gofmt -l src tests demo` | formatting |
| `go mod tidy -diff` | module drift |
| `GOOS=windows go build ./...` | platform build breaks (Windows is built, not run) |
| `go test ./... -count=1` | every test, including the run integration tests and the docs tests |
| `make race` | `go test -race` over `src/internal` |
| `bats tests/integration.bats` | the built CLI from a shell |
| `make demo` | the offline end-to-end acceptance run, in three modes |

Other targets: `make test` (fast: `-short` skips the integration tests),
`make test-integration` (everything), `make test-e2e`, `make test-ci` (named
audit checks), `make coverage`, `make vuln` (govulncheck), and the opt-in
`make smoke` / `make smoke-claude` (real agents; see below), and `make fuzz`
(explores the trusted core; `FUZZTIME=5m` per target).

## Layers

**Unit tests** live next to the code (`src/internal/<pkg>/*_test.go`): the
approval derivation, plan and blueprint parsing and hashing, policy rules and
the drift supervisor, model clients against `httptest` servers, record/replay,
gates, the event hash chain, crypto and redaction.

**Fuzz tests** (`src/internal/orchestrator/fuzz_test.go`) state the trusted
core's rules as properties and check them on arbitrary input: an accepted path is a
canonical file path inside the worktree with no `.git` part and no symlink on the
way; an edit changes exactly the first occurrence of its search text or is refused;
a new file is its content byte for byte. `go test` runs the seeds; `make fuzz`
explores, and a failing input it saves under `testdata/fuzz/` is committed as a
regression test.

**Run integration tests** drive a whole run in-process:
`orchestrator/runtest.Run` creates a throwaway workspace and git repository and
runs a scripted Go agent (`orchestrator.AgentFunc`) through the real runner -
decision loop, worktree, finalize, commit, audit chain (verified on every run).
Operators are played by a webhook `httptest` server or the approval API.
Adversarial cases are agents that write files directly, run git, commit on their
own, change bytes after approval, or escape the worktree
(`runner_approval_test.go`, `runner_adversarial_test.go`,
`runner_integrity_test.go`, `drift_test.go`, `validator_test.go`). The developer
checkout's invariance is checked on a fixture repository with staged and
unstaged work (`runner_isolation_test.go`, `tests/fixtures/repos/`).

**Agent runtime tests** (`src/internal/agent`) run the graph executor against a
scripted model, and the tools and the Claude Code adapter through real runs -
the adapter with a fake `claude` (the test binary re-executed) that calls the
real hook commands.

**CLI tests** (`src/cmd/staircase/*_test.go`) call command handlers directly
against a temporary workspace: compile, gates, blueprint import → bind →
compile → pinned gate, story scope, audit export/verify; `tests/integration.bats`
runs the built binary.

**Acceptance** - `demo/run-demo.sh` (`make demo`) is the offline end-to-end run
on the real binary and runtime with a stand-in model (`demo/demotool`): it
checks the run branch holds exactly the approved create/edit/delete, the
developer's checkout is byte-identical, the stories complete the case and the
audit chain verifies; `--tamper` must put back the file an approved command changed, and `--drift`
must halt.

**Docs tests** (`src/cmd/staircase/docs_test.go`) keep the documentation honest:
`TestCLIReference` fails when [cli.md](cli.md) no longer matches the commands' help
text, and `TestDocLinks` fails on a relative link or `#anchor` in the Markdown files
that points nowhere. After changing a command's help, regenerate the reference:

```bash
UPDATE_DOCS=1 go test ./src/cmd/staircase -run TestCLIReference
```

**Kill drills** end real processes with `kill -9`, so no cleanup code runs, and check what
a crash leaves behind:

- `tests/kill_drill.bats` runs a governed session with a stand-in agent that has two changes
  approved and then hangs, kills `staircase` itself, and checks that `staircase recover
  --force` commits exactly those two changes, that the commit verifies (CAL 2, since the run
  did not finish) and rebuilds, and that a run still recorded as RUNNING is not recovered
  without `--force`. A second test kills `recover` itself 60 times at random moments (it is
  killed before finishing in most of them), each time starting from the same dead session,
  and a second `recover` must always finish: the same commit, a certificate that verifies, a
  ledger that rebuilds, and the run's record.
- `TestDrill_sigkill_during_secret_rotate` (`src/cmd/staircase`) rotates the workspace key in
  a separate process in a loop and kills it 150 times at random moments against a real
  database. After every kill the key either loads and every secret decrypts under it, or it
  is refused as "interrupted" and a single `secret rotate` repairs it; no secret is ever
  stranded, and no key file is left lying around. The timings come from a fixed seed, and the
  test logs how often each state was hit (no journal, pending, committed). It takes about 20
  seconds; `-short` skips it.

Both fail when the protections they exist for are removed (the first drill iteration of the
rotation drill strands a secret without the "interrupted" refusal).

**Named boundaries** (`tests/barrier_drill.bats`): the random kills above say nothing about
*where* a process died. This drill builds `staircase` with `-tags barriers`, which holds the
process at a named point and writes a marker file when it gets there (a normal build has
no such code: `src/internal/barrier`). The test waits for the marker, so a point that is
never reached fails instead of passing quietly, kills the process with SIGKILL, and reopens
what was left in a fresh process. The points, in order, and what must hold afterwards:

| Killed at | A fresh process finds |
|---|---|
| `journal-synced`: the approval is in the journal, not on the audit chain | `recover` commits nothing: what only the journal knows was never decided on the chain |
| `audit-committed`: the decision is on the chain, the agent not yet answered | `recover` commits that change and no other |
| `consumed`: the approval is recorded and released | `recover` commits every approved change |
| `commit-prepared`: the commit exists and is named on the chain, no branch holds it | `recover` makes its own commit; the named one is never on the branch |
| `git-cas`: the branch moved to the run's commit, no evidence | `recover` keeps that commit (no second one) and writes the ledger and certificate |
| `evidence-published`: certificate and ledger written, the run's record not | `recover` completes the record and leaves the run's own certificate as it was |
| `db-completed`: the run's record is complete | nothing to recover; the commit verifies and rebuilds |
| `recover-op-recorded`, `recover-committed`: `recover` itself killed | a second `recover` delivers one commit and repairs its evidence |

Each case ends with `staircase verify` and `staircase rebuild` of the run branch. SIGKILL
ends a process without any cleanup; it is not a power loss, which also loses what the
operating system had not yet written to disk, and is not tested here.

**Nothing passes by saying nothing.** The independent conformance scripts
(`docs/spec/*_vectors.py`) fail when their vector directory is missing or empty, or holds
fewer vectors than the floor written in the script, and write `--json` results
(`tests/spec_vectors.bats`). Tests that need a program the machine may not have
(`ssh-keygen`, `node`, `python3`) skip with a message on a developer's machine and fail when
`STAIRCASE_REQUIRE_DEPS` is set, as it is in CI; the OS sandbox engines work the same way
through `STAIRCASE_REQUIRE_SANDBOX`.

**Release evidence.** `make evidence` (`packaging/evidence.sh`) runs the canonical release
checks, which are `make check`, a count of every Go test (run, passed, failed, skipped), the
kill drills, the three conformance scripts, the packaged verifier on a fresh mirror of the
repository (verify and rebuild), and the vulnerability scan. It writes `evidence/evidence.json`: the
commit and whether the tree was clean, the tool versions, the platform, a SHA-256 for every
conformance fixture, and each step's status, counts and log digest. It says `passed` only when
nothing failed and nothing was skipped; a limited run (`EVIDENCE_STEPS`) says `partial`. Runs
of real agents need credentials only their owner has, so they are recorded as `not_run` and
are never counted as passed. The record holds no path of the machine that made it.

**Which CI run releases.** The release workflow's gate (`packaging/release-gate.sh`) accepts
only a successful run of the CI workflow that was a `push` to the default branch of this
repository for the tagged commit. A pull request run, a run of another branch or a run from a
fork can carry the same commit but is not what merging tested; each is ignored
(`tests/release_gate.bats`).

**The terminal dialog on a real pseudo-terminal** (`tests/pty_ctrl_c.bats`): a Python
harness runs `staircase claude` under a pty with a stand-in agent and no rules, so every
change goes to the person, and types the keys a person types: `y` for the first change,
Ctrl-C at the second. The run must stop and record KILLED, the journal must hold exactly the
approved change, and `staircase recover` must commit that change and not the one interrupted
at. With Ctrl-C handling taken out of the dialog both tests fail ("the process did not end
after the last keys").

## Real agents (opt-in)

`demo/smoke.sh` needs credentials and costs money, so `make check` never runs
it; both modes skip with a message when credentials are missing.

- `make smoke` - a real model through the LLM gateway (`AI_GATEWAY_API_KEY`,
  model `SMOKE_MODEL`) records a small run; a second run replays the recording
  with the gateway unreachable and must commit the same bytes; the recording
  must not contain the key.
- `make smoke-claude` - the same case with `--agent claude-code`; the first
  approval is held 40 s, past Claude Code's default hook timeout.

## Conventions

- Test first: a change comes with the test that fails without it; for a
  security control, also check the test fails when the control is removed.
- No network in `make check`: model providers, webhooks and gateways are
  `httptest` servers or the demo's stand-in.
- Tests that start runs are skipped by `-short`; they must still pass under
  `-race`.
- Fixture repositories are built by `tests/fixtures/repos/build.sh`
  (`SOURCE_DATE_EPOCH` for reproducible commits; no macOS `._*` files).
