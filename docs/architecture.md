# Architecture

This page is for contributors and reviewers: how stAirCase is built inside. To
*use* stAirCase you do not need it; start with [Concepts](concepts.md).

stAirCase is one Go binary (`staircase`, no CGo) with a local SQLite workspace
(`$STAIRCASE_DIR`). It keeps the automation that changes a product - agents,
prompts, plans, run evidence - **outside** the product's repository, and it is
the only path by which an agent's change reaches that repository.

```
 blueprint repo ──import──▶ ┌──────────────────── staircase ─────────────────────┐
 (yaml + prompts)           │ workspace (SQLite): projects, blueprints, cases,     │
                            │ stories, topologies, secrets (AES-GCM), runs, chain  │
                            │                                                      │
        compile ──▶ plan.json + sha256 ──▶ gates ──▶ run                          │
                            │   agent (in-process) ──proposal──▶ decision loop     │
                            │     ▲ tools write only          refusal · drift      │
                            │     │ approved bytes            policy · validator   │
                            │     └──────── decision ◀─────── human (TUI/API/hook) │
                            │   finalize: verify worktree ▶ commit approved bytes  │
                            └──────────────────────────────────────────────────────┘
 product repo: untouched checkout  +  git worktree per run  +  branch staircase/run-N
```

## Flow of a run

A run is an explicit state machine: `runMoves` in `internal/orchestrator/runner.go`
lists every phase and the phases it may move to, and the runner refuses any other
move. The path a run took is recorded as its last audit event (`run_path`).

1. **Blueprint** (`internal/blueprint`) - `blueprint import` reads a directory's
   `blueprint.yaml` strictly, resolves prompt/PRD files confined to it
   (`os.Root`), and stores canonical JSON named by its SHA-256. `project bind`
   creates a new topology version, cases and stories in one transaction.
   Projects can also be set up with the imperative CLI (`topology`, `case`,
   `story`).
2. **Compile** (`cmd/staircase/compile.go`, `internal/plan`) - the case's PRD,
   stories with their scope, topology, blueprint and limits, and a repository
   map (`internal/engine`) become `tmp/plan_case<N>.json` with a sha256 sidecar.
   A run executes exactly this plan.
3. **Gates** (`internal/gate`) - pre-run checks: structure, provider secrets,
   the plan (present, unmodified, and for a bound case exactly its blueprint's -
   `runtime.plan_pinned`), git, dependencies, plus signed plugin gates
   (`gates.json`).
4. **Run** (`internal/orchestrator`) - records `run_bound` (base commit, plan
   digest, blueprint, topology version) as the first chain event, creates a git
   worktree on `staircase/run-N` at the base commit, and starts the agent in a
   goroutine:
   - the built-in runtime (`internal/agent`, models over `internal/llm`): a
     supervisor and its agents, one step at a time, routed by `ROUTE:` lines;
   - or Claude Code (`agent.ClaudeCode`, experimental), whose tool calls reach
     the same loop through PreToolUse/PostToolUse hooks.
5. **Decision loop** - every edit, file creation or deletion and shell command
   is a proposal. The orchestrator derives the exact resulting bytes from the
   base commit and what was shown (`approval.go`), then decides in order:
   refusal (bad path, text not found, too large) → drift halt → human for drift
   or limits (`policy.Supervisor`) → policy rules → validator → human. The
   decision is appended to the chain **before** the agent sees it; tools then
   write exactly the derived bytes.
6. **Finalize** - the worktree must equal the approved state (no other change,
   no index edits, no agent commits, the branch where it started); the commit is
   built from the approved bytes with git plumbing on a private index and
   `update-ref` compare-and-swap. A human approves the whole change once when the
   validator decided anything. `drift_report` and `summary.json` close the run;
   a successful run's worktree is removed, a failed one's kept for inspection.
7. **Evidence** - `story accept` records acceptance and completes the case;
   `audit export` writes an Ed25519-signed checkpoint of the chain to an
   append-only file (`internal/audit`), optionally anchored in a Rekor
   transparency log; `audit verify` checks both.

## Packages

| Package | Role |
|---|---|
| `cmd/staircase` | CLI (cobra); every command is a thin layer over the packages below |
| `orchestrator` | run lifecycle, decision loop, approval derivation, finalize/commit, webhook, validator |
| `agent` | built-in runtime (graph executor, tools), Claude Code hook adapter |
| `llm` | OpenAI-compatible and Anthropic clients over `net/http`, routing by model name, record/replay |
| `plan` | the compiled plan: canonical JSON + sha256, validation, the agents' brief |
| `blueprint` | blueprint parsing, snapshot hash, binding, plan-vs-blueprint check |
| `policy` | auto-approval rules and limits (`policy.json`), drift supervisor |
| `gate` | pre-run gates and plugin gates |
| `persistence` | SQLite schema, migrations, store, event hash chain |
| `crypto` | AES-256-GCM secrets, Ed25519 signing keys, secret scrubbing |
| `audit` | signed checkpoints, Rekor anchoring |
| `approvalhttp` | local HTTP approval API (`--approval-port`) |
| `webhookauth` | HMAC signing of webhook approvals |
| `tui`, `monitor` | terminal approval screen, live run display, token/cost tracking and budget |
| `engine` | repository map for plans, glob matching |
| `obs` | structured logging with redaction, metrics, OpenTelemetry |
| `domain`, `wslock` | shared types, advisory file locks |

## Trust boundaries

- **The agent is untrusted.** It changes nothing except through proposals; the
  orchestrator never trusts what it claims (hashes, what it wrote) and verifies
  the worktree before committing. Tools run as your OS user; approved shell
  commands run in an OS sandbox where the machine has one
  ([SECURITY.md](../SECURITY.md)).
- **Models see** the plan's brief, the repository map, files the agents read,
  and nothing else; keys are decrypted only to call the model, never shown to
  it, and scrubbed from every log and audit record. Shell commands get an
  allowlisted environment.
- **Humans decide** through the TUI, the local approval API (token) or a webhook
  (HMAC, fresh `yield_id` and request hash echoed). The validator is a model
  and bounded accordingly ([approvals](approvals.md#letting-a-model-review-changes-the-validator)).
- **State** stays in the workspace, not the product repository; the product
  repository gains only `staircase/run-N` branches.

See also: [concepts](concepts.md), [safety boundary](safety.md),
[testing](testing.md).
