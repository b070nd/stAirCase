# CLI reference

<!-- Generated from the command tree by TestCLIReference. Do not edit by hand:
     UPDATE_DOCS=1 go test ./src/cmd/staircase -run TestCLIReference -->

Every `staircase` command, as `staircase <command> --help` prints it. For what the
commands are for, start with [Getting started](../QUICKSTART.md) and [Concepts](concepts.md).

Global flag, accepted by every command:

```
      --dir string   Workspace directory (default: ~/.staircase-workspace)
```

The workspace directory can also be set with the `STAIRCASE_DIR` environment variable.

## staircase audit export

Export a signed audit checkpoint for a completed run

```
staircase audit export <run-id> [flags]
```

Flags:

```
      --anchor             Also anchor the signed checkpoint in a Rekor transparency log (external witness). This uploads the whole record — reasoning, paths, proposed changes — to a public, permanent log
      --rekor-url string   Rekor server URL used by --anchor / --check-anchor (default "https://rekor.sigstore.dev")
```

## staircase audit verify

Verify the integrity and signature of an audit checkpoint file

```
staircase audit verify <checkpoint-file> [flags]
```

Flags:

```
      --check-anchor       Also verify each record against its Rekor anchor sidecar (<file>.anchor)
      --rekor-url string   Rekor server URL used by --anchor / --check-anchor (default "https://rekor.sigstore.dev")
```

## staircase blueprint import

Import <dir>/blueprint.yaml as an immutable snapshot identified by its content hash

```
staircase blueprint import <dir>
```

## staircase blueprint list

List imported blueprints

```
staircase blueprint list
```

## staircase case delete

Flag a Case for deletion (removed on next 'staircase clean --aggressive')

```
staircase case delete <case-id>
```

## staircase case list

List all Cases for a project

```
staircase case list <project-id>
```

## staircase case new

Create a new Case for a project

```
staircase case new <project-id>
```

## staircase case rollback

Discard the latest run of a case: remove its worktree and delete its branch

```
staircase case rollback <case-id>
```

Discards the most recent run of a case: removes its worktree (kept after a
failed run) and deletes its staircase/run-N branch. Your checkout is not
touched — runs never modify it. The run record and its audit chain are kept;
a rolled_back event records who discarded it, and the case returns to PENDING
so it can be run again. A RUNNING run must be stopped first.

## staircase case set-prd

Load a PRD JSON file into a Case

```
staircase case set-prd <case-id> <prd-file>
```

## staircase case status

Show detailed status of a Case

```
staircase case status <case-id>
```

## staircase clean

Remove leftovers of older staircase versions from tmp/

```
staircase clean [flags]
```

staircase clean removes what older staircase versions left in tmp/: compiled
Python scripts (graph_exec_*) and stale IPC sockets. Compiled plans are kept.

--aggressive also removes the Python venv older versions installed and
orphaned staircase/run-* git branches older than 30 days.

--keep-failed preserves branches and logs for FAILED runs (forensic mode).

--dry-run prints what would be removed without deleting anything.

Flags:

```
      --aggressive    Also remove the old Python venv and stale git branches
      --dry-run       Print targets without deleting
      --keep-failed   Preserve branches/logs for FAILED runs
```

## staircase compile

Compile a Case into the plan staircase run executes

```
staircase compile <case-id> [flags]
```

Flags:

```
      --force   Overwrite an existing plan
```

## staircase component add

Register a component within a project

```
staircase component add <project-id> <name>
```

## staircase component delete

Remove a component (cascades agent_node.component_id to NULL)

```
staircase component delete <id>
```

## staircase component list

List components for a project

```
staircase component list <project-id>
```

## staircase component update

Rename a component

```
staircase component update <id> <new-name>
```

## staircase dag viz

Output the project dependency graph in DOT format

```
staircase dag viz [project-id]
```

## staircase doctor

Run diagnostics on the stAirCase workspace

```
staircase doctor
```

## staircase gate

Run pre-flight quality gates for a case and generate a report

```
staircase gate <case-id> [flags]
```

Executes all registered quality gates against a case before running.

Gates are grouped by category (structural, security, runtime, dependency).
BLOCK gates must pass for 'staircase run' to proceed.
WARN  gates are advisory — they surface issues but do not block execution.

Exit codes:
  0  all BLOCK gates passed (overall PASS or WARN)
  1  one or more BLOCK gates failed (overall FAIL)
  2  usage error

Flags:

```
      --json         Emit machine-readable JSON report
      --out string   Write report to file (default: stdout)
```

## staircase gate sign

Sign gates.json with the workspace signing key

```
staircase gate sign
```

Sign $STAIRCASE_DIR/gates.json with the workspace Ed25519 signing key
and write $STAIRCASE_DIR/gates.json.sig.

Re-run after any edit to gates.json. staircase gate warns when the
signature is absent and blocks execution when the signature is invalid.

## staircase gate verify

Verify the gates.json signature

```
staircase gate verify
```

Verify $STAIRCASE_DIR/gates.json.sig against the current gates.json
content and the workspace public key (.signing.pub).

Exits 0 when valid, non-zero otherwise. Useful in CI to confirm that
gates.json has not been modified since it was last signed.

## staircase hook

Pass an agent's hook call to the run that governs it (used by agent adapters)

```
staircase hook <agent> [--governed]
```

Coding agents such as Claude Code call this command before and after each
tool call. It reads the call on standard input, passes it to the stAirCase run
that started the agent, and prints the run's answer.

Exit code 2 blocks the tool call. Every failure blocks: a missing or invalid
session, a run that does not answer or refuses the token, an unknown agent.

--governed is set whenever stAirCase starts the agent: without the run's
session file (STAIRCASE_HOOK_FILE) the call is blocked. Without --governed, as
in a hook installed once for a user or a company, calls pass through when no
governed session is running.

Supported agents: claude-code.

## staircase init

Initialize the stAirCase workspace: database and keys

```
staircase init
```

## staircase inspect log

Show the audit event log of a run and verify its hash chain

```
staircase inspect log <run-id> [flags]
```

Flags:

```
      --full   Print complete payloads without truncation
```

## staircase inspect runs

List runs, optionally filtered by case

```
staircase inspect runs [flags]
```

Flags:

```
      --case int   Filter by case ID
```

## staircase policy sign

Sign policy.json with the workspace signing key

```
staircase policy sign
```

Sign $STAIRCASE_DIR/policy.json with the workspace Ed25519 signing key
(.signing.key) and write $STAIRCASE_DIR/policy.json.sig.

Re-run this command after every policy edit. staircase run warns when the
signature file is absent and refuses to run when the signature is invalid.

## staircase policy verify

Verify the policy.json signature

```
staircase policy verify
```

Verify $STAIRCASE_DIR/policy.json.sig against the current policy.json
content and the workspace public key (.signing.pub).

Exits 0 when the signature is valid, non-zero otherwise.  Useful in CI to
confirm that policy.json has not been modified since it was last signed.

## staircase project add

Register a new project under a vendor

```
staircase project add <vendor-name> <project-name> [flags]
```

Flags:

```
      --source string   Absolute path to the project's source repository
```

## staircase project bind

Bind a project to a blueprint: creates a new topology version and the blueprint's cases

```
staircase project bind <project-id> <blueprint-hash>
```

Bind a project to an imported blueprint (hash or unique prefix).

Binding creates a new topology version and new cases and stories from the
blueprint; existing topologies, cases and stories are not changed. Bound cases
run only as the blueprint defines them: the runtime.plan_pinned gate blocks a
run whose compiled plan differs from the blueprint. To change a bound case,
change the blueprint, import it and bind again.

## staircase project config set

Set default model or budget cap for a project

```
staircase project config set <project-id> [flags]
```

Flags:

```
      --budget-cap float       Max USD spend per run (0 = no cap)
      --default-model string   Default LLM model for agents in this project
```

## staircase project config show

Show LLM configuration for a project

```
staircase project config show <project-id>
```

## staircase project dep add

Declare that source-project depends on target-project

```
staircase project dep add <source-project-id> <target-project-id>
```

## staircase project list

List projects for a vendor

```
staircase project list <vendor-name>
```

## staircase project set-webhook

Set (or clear) the HITL webhook URL for a project

```
staircase project set-webhook <project-id> <url>
```

Set (or clear) the HITL webhook URL for a project.

To authenticate the webhook channel (strongly recommended — otherwise a network
attacker can forge approvals), store a shared HMAC secret under the reserved key:

    printf '%s' "$SECRET" | staircase secret set __webhook_hmac_secret__ --project <project-id>

When that secret is present, stAirCase HMAC-signs each outbound yield and rejects
any response that is not validly signed with the same secret. The approver must
verify the X-Staircase-Signature request header, sign its response the same way,
and echo the X-Staircase-Request-SHA256 header value as "request_sha256" in the
signed response body (so an approval cannot be replayed for another request).

## staircase push

Push a completed run's branch and open a GitHub PR

```
staircase push <run-id> [flags]
```

Push the staircase/run-{ID} branch to the remote and open a GitHub
pull request targeting the base branch recorded for that run.

A GitHub personal access token is required to create the PR. Pass it via
--github-token or the GITHUB_TOKEN environment variable. The token needs
the 'repo' scope.

If the remote is not github.com the branch is still pushed but PR creation
is skipped and the PR URL is printed for manual use.

Flags:

```
      --draft                 Open PR as a draft
      --github-token string   GitHub PAT (default: $GITHUB_TOKEN)
      --remote string         Git remote name (default "origin")
```

## staircase replay

Print the approval decisions of a finished run, after verifying its audit chain

```
staircase replay <run-id>
```

Verify the audit chain of a run and print every approval decision in order.

Replay refuses to proceed if the hash chain is broken — this prevents
replaying a tampered run log.

## staircase run

Run a compiled case: agents work, you approve, the approved change is committed

```
staircase run <case-id> [flags]
```

Flags:

```
      --ack-drift               Run a case whose previous run was halted for drift, after reviewing it (recorded on the audit chain)
      --agent string            Agent to run: built-in (the compiled topology) or claude-code (Claude Code with every tool call governed by hooks; experimental) (default "built-in")
      --allow-shell-exec        Enable run_shell for this run — agents may request OS-level shell execution subject to HITL approval. Shell execution is disabled by default; pass this flag to opt in.
      --approval-port int       Start an inbound HTTP approval server on this port (0 = disabled). Exposes GET /v1/yields and POST /v1/yields/{id}/approve|reject for async HITL.
      --approval-token string   Bearer token required by the approval HTTP server. If empty and --approval-port is set, a random token is generated and printed at startup.
      --debug                   Log every agent message (proposals, usage) to $STAIRCASE_DIR/log/
      --dry-run                 Validate and print the execution plan without running
      --metrics-addr string     Expose Prometheus metrics on this address (e.g. 127.0.0.1:9090). Empty = disabled.
      --otel-endpoint string    OTLP/gRPC endpoint for OpenTelemetry traces (e.g. localhost:4317). Empty = disabled.
      --reconcile               Inspect orphan staircase/run-* branches (never delete them) and reconcile stale RUNNING records
      --record-llm string       Record every model exchange of this run to this file (JSON lines) for offline replay.
      --replay-llm string       File path to replay recorded LLM exchanges instead of calling the real API.
      --skip-gates              Bypass quality gate pre-flight (use with care)
      --validator string        Model that reviews in-scope file edits the policy leaves open (e.g. openai/gpt-6-astra via the LLM gateway); a human approves the run's final change once
```

## staircase secret list

List stored secret key names (values are never shown)

```
staircase secret list
```

## staircase secret rotate

Re-encrypt all secrets under a new AES-256 workspace key

```
staircase secret rotate
```

Generates a fresh AES-256 key, re-encrypts every stored secret in a
single atomic DB transaction, then replaces the old key file.

The operation holds an exclusive non-blocking advisory lock on the workspace
key file.  It fails fast (does not wait) if any concurrent process — an active
run or a 'secret set' command — already holds a shared lock on the key.

## staircase secret set

Store an AES-256-GCM encrypted secret in the workspace (reads value from stdin)

```
staircase secret set <key-name> [flags]
```

Reads the secret value from stdin to prevent it appearing in shell history.

Pipe the value in non-interactively:
  printf 'my-secret' | staircase secret set MY_KEY

Or enter it interactively (input will not be echoed):
  staircase secret set MY_KEY

Setting a key that exists in the same scope replaces its value (for example a
rotated API key) and counts its version.

Flags:

```
      --project int   Scope secret to a specific project ID (0 = global)
```

## staircase story accept

Record operator acceptance after independently verifying a story

```
staircase story accept <story-id>
```

## staircase story add

Add a User Story to a Case

```
staircase story add <case-id> <description>
```

## staircase story invalidate

Mark a User Story as INVALIDATED (it must be re-verified and accepted again)

```
staircase story invalidate <story-id>
```

## staircase story list

List all User Stories for a Case

```
staircase story list <case-id>
```

## staircase story scope

Set the paths a story's runs may change (drift supervision)

```
staircase story scope <story-id> [flags]
```

Set the paths a story's runs may change. A proposal touching any other path
goes to a human instead of the policy, and more than the policy's
max_scope_violations such proposals halt the run. --allow takes globs relative
to the repository root (** spans directories) and may repeat; --max-files caps
the distinct files the story's runs change before a human must look. With no
flags the scope is cleared. Stories of a case bound to a blueprint take their
scope from the blueprint: change it there.

Flags:

```
      --allow stringArray   Glob of paths the story may change (repeatable)
      --max-files int       Most distinct files the story's runs change before a human must look (0 = no cap)
```

## staircase topology agent add

Add an agent node to a topology

```
staircase topology agent add <topology-id> <name> <role> [flags]
```

Flags:

```
      --model string   LLM model identifier (default: claude-sonnet-4-6)
```

## staircase topology edge add

Add a directed edge between two agent nodes

```
staircase topology edge add <topology-id> <from-node> <to-node> [flags]
```

Flags:

```
      --condition string   Conditional routing expression
```

## staircase topology register

Create a new version of a project's agent topology

```
staircase topology register <project-id> <supervisor-name> [flags]
```

Flags:

```
      --checkpoint string   Checkpoint type (memory or sqlite) (default "memory")
      --runtime string      Kept for compatibility: staircase runs every topology with its built-in runtime (default "langgraph")
```

## staircase topology show

Show the latest agent topology of a project

```
staircase topology show <project-id>
```

## staircase topology tool add

Register a tool for an agent node

```
staircase topology tool add <agent-id> <tool-name> [flags]
```

Flags:

```
      --config string   JSON tool configuration
```

## staircase vendor add

Register a new vendor namespace

```
staircase vendor add <name>
```

## staircase vendor list

List all registered vendors

```
staircase vendor list
```

## staircase version

Print the version of stAirCase

```
staircase version
```
