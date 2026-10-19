# ADR 0013: CEL conditions in policy rules (design)

- Status: proposed (2026-10-18), for the owner to decide. Nothing here is built, and the dependency it names is not added: accepting
  it is one of the owner's decisions.
- Related: the policy file's promise in [compatibility](../compatibility.md#the-six-contracts), [approvals](../approvals.md#approving-automatically-with-rules),
  ADR 0001 (section 3, who may decide), ADR 0008 (decision 1.3, tool calls), ADR 0009 (declared commands).

## Context

A policy rule (`internal/policy/engine.go`) has four conditions, all of which must match: `action_types`, `min_confidence`,
`allowed_extensions` and `agent_names`, and an `effect`, `approve` or `reject`. Rules are tried in order and the first that matches
decides. That covers "approve Markdown", but not "approve Markdown under `docs/` only", "at most 40 changed lines", "new files only" or
"never when a file is deleted". Each of those would be one more struct field.

What the policy file already guarantees:

- **Strict parsing.** `parseEngine` decodes with `DisallowUnknownFields`, refuses an effect other than `approve` or `reject`, and
  refuses a reject rule with no condition unless `allow_blanket_deny` is set. A file that does not parse stops the run before it starts.
- **One read, bound.** `LoadSnapshot` reads `policy.json` once: the engine, the SHA-256 of those exact bytes (on the chain as
  `policy_snapshot`, in the certificate as `policy`), and whether they carry a valid signature (`policy.json.sig`). A run saves the
  bytes, and a continuation decides under them (`SnapshotFromBytes`), never under today's file.
- **Where the policy sits.** In `deciders.rule` (`orchestrator/decide.go`) the order is: the orchestrator refuses what it cannot apply;
  a command or tool the owner declared for an away run is approved from the run's terms; drift supervision halts or sends to a person;
  a change to a path that holds a check's definition (`Terms.ProtectedPaths`) goes to a person, parked while away; a limit, drift or a
  guard sends to a person; only then the policy's rules; then the agreed task (where a sensitive path goes to a
  person), evidence, the validator, a person. A signal can only turn an automatic approval into a person's decision. The final review
  never reaches the engine.
- **Commands and tool calls.** `Engine.Evaluate` returns "no match" for `shell_exec` and `tool_call` before it looks at any rule:
  "shell commands must always reach a human operator", and an MCP tool acts outside the sandbox too.
- **The same engine everywhere.** `staircase policy test` (replay of past proposals) and `policysim` (scenarios through the real
  admission code) decide with it.

The package's own comment already plans this: CEL expressions, "deferred", with the upgrade "additive (new Rule field `Expression
string`; old conditions stay for backwards compatibility)". The file's `version` field is read but never checked. The promise in
`docs/compatibility.md` is: "A valid policy file keeps its meaning."

## Decision (proposed)

### 1. A rule may carry a condition, which can only narrow it

A rule gains an optional `condition`: one CEL expression of type `bool`. A rule with a condition matches when its other conditions
match **and** the expression is true. A rule without one is exactly today's rule. The effect stays the rule's own static field: the
expression decides whether the rule applies, never what it does.

```json
{ "action_types": ["file_edit"], "effect": "approve",
  "condition": "proposal.files.all(f, f.path.startsWith('docs/') && f.ext == '.md' && !f.deleted) && proposal.lines <= 40" }
```

### 2. What the expression sees

One declared variable, `proposal`, built by Go from the request as the orchestrator completed it and from the state it derived, which
is what a person would be shown:

| Field | Type | From |
|---|---|---|
| `proposal.action` | string | the action type |
| `proposal.agent` | string | the agent's name, as `agent_names` reads it |
| `proposal.confidence` | double | the agent's own score, as `min_confidence` reads it |
| `proposal.files` | list of `{path, ext, new, deleted, lines}` | the derived files: path, extension, created, deleted, lines changed |
| `proposal.lines` | int | lines added or removed, counted as `changedLines` counts them |

Not exposed: the agent's reasoning, the file contents and the search and replace text. They are written by the agent, and a rule that
reads them becomes a pattern denylist over text the agent controls.

### 3. Compiled once, bounded, deterministic, failing closed

- **Compiled when the file is parsed**, in `parseEngine`, so `LoadSnapshot`, `SnapshotFromBytes` and `LoadEngineFile` all apply it.
  An expression that does not parse, does not type-check to `bool`, names anything but `proposal` and the standard functions, or is
  longer or deeper than the parser's limits refuses the whole file, as an unknown field does today.
- **A closed environment**: CEL's standard definitions and the one variable; no extension library, no function that reads the clock,
  the environment, the network or the file system, no function of stAirCase's own. The same expression on the same proposal always
  gives the same answer, so a replay and a continuation decide as the run did.
- **A cost limit** on each evaluation (cel-go's runtime cost limit). Going over it is an error.
- **An error sends the proposal to a person**, and no later rule is tried. A failed condition on a reject rule must not let a later
  approve rule, the agreed task or evidence approve the proposal. `PolicyDecision` gains this third outcome, which the deciders route
  as they route a limit: to a person, parked while away.
- **Bound like the rest of the file.** The expression is part of `policy.json`'s bytes, so the digest, the saved copy and the
  signature already cover it.
- A reject rule whose only condition is an expression is not a blanket deny.

### 4. Why it cannot weaken "commands and tool calls reach a person"

- **The code path, not the policy.** `Evaluate` returns before its loop over the rules for `shell_exec` and `tool_call`; the condition is
  evaluated inside the loop, so it is never evaluated for them. A test asserts it for every rule shape, `"condition": "true"` included,
  and a fuzz test does for generated conditions.
- **A boolean only.** The expression cannot set an effect, name an action, call out or change the proposal.
- **Declared commands and tools are terms, not policy.** What an away run may run or call without asking comes from `--away-command` and
  `--away-tool`, bound on `run_bound`, and is decided before the policy. A policy file cannot add one.
- **Everything before the policy still wins.** Drift, a change to a protected check path, limits and guards send a proposal to a
  person whatever a rule says; the final
  review never reaches the engine; a signal can still escalate a rule's approval.

### 5. A valid policy file keeps its meaning

- A file without conditions decides exactly as today: the struct conditions are unchanged. A test runs the existing `policysim`
  scenarios and `policy test` replays before and after.
- An older stAirCase refuses a file with `condition`, because it does not know the field. It never runs a rule with the condition
  dropped, which would widen an approve rule.
- `docs/compatibility.md` keeps its row and adds one sentence: a condition only narrows the rule it is on.

### 6. The dependency: cel-go

- **What**: `github.com/google/cel-go`, Google's Go implementation of the Common Expression Language (the language is specified in
  `github.com/google/cel-spec`). Kubernetes uses CEL for its admission policies. Expressions are not Turing-complete: macros such as
  `all` and `exists` iterate over their input, and evaluation has a cost estimate and a cost limit.
- **License**: Apache License 2.0, as the project publishes it; to be confirmed against the `LICENSE` file of the pinned version when it
  is added. stAirCase is MIT and already ships Apache-2.0 modules (`github.com/spf13/cobra`, `go.opentelemetry.io/otel`,
  `google.golang.org/grpc`), so it adds no new kind of obligation.
- **What it brings with it** (from cel-go's own `go.mod`, not observed in this repository; to be listed exactly from `go.mod` after the
  `go get`): `cel.dev/expr`, `github.com/antlr4-go/antlr/v4` (its parser's runtime, BSD-3-Clause), `github.com/stoewer/go-strcase`,
  `golang.org/x/exp`, and `google.golang.org/protobuf` and `google.golang.org/genproto/googleapis/api`, which `go.mod` already has as
  indirect dependencies. Each module's license is checked then.
- **Pinned** the way CONTRIBUTING says every dependency is: by hand, to one exact tagged release chosen when the owner accepts it,
  `go get github.com/google/cel-go@vX.Y.Z`, `go mod tidy`, `make check`. `go.sum` pins it and every module it brings by hash;
  `make check` runs `go mod tidy -diff`; `govulncheck` in CI fails on a reachable advisory, which is the signal to update. cel-go is
  published as v0 releases, so its Go API may change between minor versions: an update is a reviewed change with the policy tests.
- **Contained**: the import lives in one file of the policy package (the compiler and the evaluator); the rest of the engine stays
  plain Go. The package's comment "no external dependencies" changes with it.
- No network at run time: the expression is evaluated in the process. Fetching the module is the owner's step, and a change to `go.mod`
  or `go.sum` is one a person decides (the dependency guard).

## Options considered

1. **CEL conditions with cel-go** (proposed): a standard, specified language with a cost model, used for admission policies elsewhere.
2. **More struct fields** (`paths` globs, `max_lines`, `new_files_only`, `no_deletes`): no dependency and covers the common asks, but each
   question is a new field, and combinations (all files under `docs/` *and* none deleted) need more of them.
3. **Rego (Open Policy Agent)**: a policy language with a large engine and its own data model; much more than one boolean per rule needs.
4. **Another Go expression library** (`github.com/expr-lang/expr`, MIT): smaller, but not a cross-language standard with a published
   specification.
5. **Starlark** (`go.starlark.net`): a full language; harder to bound, and more than a condition.
6. **Nothing**: rules stay as they are.

## Consequences

- New: `Rule.Condition`, the compiled program kept beside the rule, the "a person decides" outcome of `PolicyDecision`, and the facts
  Go builds for `proposal` (so `Evaluate` takes them with the request). `staircase policy test` and `policysim` evaluate conditions
  because they use the engine.
- `docs/approvals.md` documents the field, the variable, the limits and the error rule, with examples.
- One more module tree in `go.mod`, and a larger binary, measured when it is added.

## Open questions for the owner

1. **Accept the dependency?** `github.com/google/cel-go` at one exact version, with the modules it brings. Or start with more struct
   fields (option 2) and keep CEL for later?
2. **What the expression sees**: the proposed fields only, or file contents too (for example `f.content.matches(...)`)?
3. **An error in a condition**: send the proposal to a person and stop (proposed), or treat the rule as not matching and go on?
4. **The file's version**: stay at `version: 1` (the change is additive), or require `version: 2` in a file that uses conditions, so the
   intent is explicit? Today the version is not checked.
5. **The field's name**: `condition` (proposed) or `expression`, as the package's comment calls it?
6. **Seen while drafting, separate from CEL**: a policy rule can approve a sensitive path (CI files, `Makefile`, `*.sh`, `.env*`), since
   that check is in the agreed task's branch of `deciders.rule`, after the policy. Should rules be barred from sensitive paths as the
   task and the validator are, before conditions make broad rules easier to write?
