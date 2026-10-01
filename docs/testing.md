# Testing

`make check` is the gate: every change passes it locally and in CI
(`.github/workflows/ci.yml` runs it on Ubuntu and macOS).

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
  without `--force`.
- `TestDrill_sigkill_during_secret_rotate` (`src/cmd/staircase`) rotates the workspace key in
  a separate process in a loop and kills it 150 times at random moments against a real
  database. After every kill the key either loads and every secret decrypts under it, or it
  is refused as "interrupted" and a single `secret rotate` repairs it; no secret is ever
  stranded, and no key file is left lying around. The timings come from a fixed seed, and the
  test logs how often each state was hit (no journal, pending, committed). It takes about 20
  seconds; `-short` skips it.

Both fail when the protections they exist for are removed (the first drill iteration of the
rotation drill strands a secret without the "interrupted" refusal).

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
