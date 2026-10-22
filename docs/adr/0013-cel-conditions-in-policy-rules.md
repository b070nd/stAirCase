# ADR 0013: Typed conditions in policy rules (CEL deferred)

- Status: accepted (2026-10-20). Typed conditions are built. CEL is deferred, and nothing here adds a dependency.
- Related: the policy file's promise in [compatibility](../compatibility.md#the-six-contracts), [approvals](../approvals.md#approving-automatically-with-rules),
  ADR 0001 (section 3, who may decide), ADR 0008 (decision 1.3, tool calls), ADR 0009 (declared commands).

## Context

A policy rule (`internal/policy/engine.go`) had four conditions, all of which must match: `action_types`, `min_confidence`,
`allowed_extensions` and `agent_names`, and an `effect`, `approve` or `reject`. Rules are tried in order and the first that matches
decides. That covers "approve Markdown", but not "approve Markdown under `docs/` only", "at most 40 changed lines", "new files only" or
"never when a file is deleted". The first proposal of this ADR was one CEL expression per rule. The owner chose to start with typed
fields and to keep CEL for later.

What the policy file already guarantees:

- **Strict parsing.** `parseEngine` decodes with `DisallowUnknownFields`, refuses an effect other than `approve` or `reject`, and
  refuses a reject rule with no condition unless `allow_blanket_deny` is set. A file that does not parse stops the run before it starts.
- **One read, bound.** `LoadSnapshot` reads `policy.json` once: the engine, the SHA-256 of those exact bytes (on the chain as
  `policy_snapshot`, in the certificate as `policy`), and whether they carry a valid signature (`policy.json.sig`). A run saves the
  bytes, and a continuation decides under them (`SnapshotFromBytes`), never under today's file.
- **Where the policy sits.** In `deciders.rule` (`orchestrator/decide.go`) the order is: the orchestrator refuses what it cannot apply;
  a command or tool the owner declared for an away run is approved from the run's terms; drift supervision halts or sends to a person;
  a change to a path that holds a check's definition (`Terms.ProtectedPaths`) goes to a person, parked while away; a file edit that
  touches a sensitive path (CI, build, dependency, secret and shell files, `CODEOWNERS`) does too, ahead of the limits and the policy
  (so it reaches a person even when a `reject` rule would have rejected it); a limit, drift or a guard sends to a person; only then the policy's rules; then the agreed task, evidence, the validator, a person. A signal can only
  turn an automatic approval into a person's decision. The final review never reaches the engine.
- **Commands and tool calls.** `Engine.Evaluate` returns "no match" for `shell_exec` and `tool_call` before it looks at any rule:
  "shell commands must always reach a human operator", and an MCP tool acts outside the sandbox too.
- **The same engine everywhere.** `staircase policy test` (replay of past proposals) and `policysim` (scenarios through the real
  admission code) decide with it.

The promise in `docs/compatibility.md` is: "A valid policy file keeps its meaning."

## Decision

### 1. A rule may carry typed conditions, which can only narrow it

A rule gains five optional conditions, AND-ed with the existing four:

| Field | Meaning |
|---|---|
| `paths` | every changed file, a deleted one too, matches one of these globs. Matching is the existing path matcher (`**` crosses directories) and ignores letter case, as the protected and sensitive paths do |
| `max_lines` | the proposal adds or removes at most this many lines |
| `max_files` | the proposal changes at most this many files |
| `new_files_only` | every changed file is absent from the run's base commit |
| `no_deletions` | no changed file is deleted |

`max_lines` and `max_files` of 0 (or absent) are no limit. A rule without any of them is exactly today's rule. The effect stays the
rule's own static field: the conditions decide whether the rule applies, never what it does.

```json
{ "action_types": ["file_edit"], "effect": "approve", "paths": ["docs/**"], "max_lines": 40, "no_deletions": true }
```

### 2. What the conditions see

`Evaluate(req, facts)` takes the request and a `Facts` value built by the decider (`deciders.facts`) from the state the orchestrator
derived, which is what a person would be shown: each changed file with its path, whether the base commit has it, whether it is
deleted, and the lines this proposal adds or removes in it, and the total. The conditions never read the agent's own claims, its
reasoning, the file contents or the search and replace text. `staircase policy test` (replay) rebuilds the facts from the recorded
edits (lines and new files are estimates there) and the scenarios run the real deciders.

### 3. Fail closed, bound by the file's digest

- **Load.** A glob that is not valid (or is empty) and a negative `max_lines` or `max_files` refuse the whole file, as an unknown
  field does. A typed condition cannot fail at evaluation: it is a comparison on facts, so there is no error outcome to route.
- **Bound like the rest of the file.** The fields are part of `policy.json`'s bytes, so the digest, the saved copy and the signature
  already cover them.
- A reject rule whose only conditions are typed ones is not a blanket deny.

### 4. Why it cannot weaken "commands and tool calls reach a person"

- **The code path, not the policy.** `Evaluate` returns before its loop over the rules for `shell_exec` and `tool_call`; the typed
  conditions are checked inside the loop, so they are never evaluated for them. A test asserts it for every combination of the
  conditions and every effect.
- **Declared commands and tools are terms, not policy.** What an away run may run or call without asking comes from `--away-command`
  and `--away-tool`, bound on `run_bound`, and is decided before the policy. A policy file cannot add one.
- **Everything before the policy still wins.** Drift, a change to a protected check path, limits and guards send a proposal to a
  person whatever a rule says; the final review never reaches the engine; a signal can still escalate a rule's approval.

### 5. A valid policy file keeps its meaning

- A file without the fields decides exactly as before. A test runs the existing `policysim` scenarios and `policy test` replays.
- The file's `version` stays 1: the fields are additive. An older stAirCase refuses a file that uses them, through its existing
  `DisallowUnknownFields` parse, and never runs a rule with the conditions dropped, which would widen an approve rule. A fixture of the
  previous release's parser pins that.
- A path list can be checked by a program ("can this rule ever approve a file under `.github/`?"), which an arbitrary expression
  cannot answer. That is what a later delivery by policy needs.

### 6. Deferred: CEL

CEL (the Common Expression Language) is not accepted. The findings are recorded so the question is not researched again from scratch
(read and measured 2026-10-19):

- **The module moved.** The project is `cel.dev/cel-go` (repository `github.com/cel-expr/cel-go`). `github.com/google/cel-go` is a
  read-only compatibility copy that is to be removed: its path stops at v0.30.0, and later tags (v0.32.0 is the current release)
  fail to resolve under it ("module declares its path as: cel.dev/cel-go but was required as: github.com/google/cel-go"). The first
  draft of this ADR named the old path.
- **Measured cost of the current release** (v0.32.0, goreleaser flags, go1.26.9, darwin/arm64): the binary grows by 4,532,000 bytes
  (+13.8%), the build by about 0.5 to 0.9 seconds, and four modules are new in the binary: `cel.dev/cel-go`, `cel.dev/expr`,
  `github.com/antlr4-go/antlr/v4` and `golang.org/x/exp`. Licences are Apache-2.0 and BSD-3-Clause, so the release archive would also
  need the added modules' notices.
- **A one-way door.** Once a release accepts expressions, users' signed `policy.json` files contain CEL text, and the compatibility
  promise ties the meaning of every such file to the library. The library is v0, its cost-model estimates changed on the main branch
  in October 2026 (with a way to pin the old version), and removing CEL later would break every file that uses it. Typed fields have
  none of that.
- **One past advisory.** GHSA-gcjh-h69q-9w9g (GO-2026-6094), in its native-types extension (`ext.NativeTypes` with `ParseStructTag`),
  fixed before the current release. It is filed under the old module path, so `govulncheck` coverage of `cel.dev/cel-go` would need
  checking again before relying on it.
- **Reopen it when** policy portability with other policy tooling is needed; or three real rule requests arrive that the typed
  fields cannot express (an OR of two path sets is the first likely one, and a list of alternative `paths` sets would solve that
  without CEL); or a rule must condition on check results.
- **If it is ever accepted,** the conditions are: the canonical module `cel.dev/cel-go` at an exact pinned tag; the `proposal`
  variable declared with typed fields and the result checked to be boolean at compile time (a bare map field compiles without error
  because map values are dynamic); parser and cost limits (probed at expression size 512, recursion 32, 200 nodes, cost 10000, to be
  tuned on real rules); a static worst-case cost check at load that refuses a rule over the limit; no `ext.NativeTypes`; the cost
  model version pinned in the file; and the added modules' notices in the release archive. An error would send the proposal to a
  person and stop, so a failed condition on a reject rule cannot let a later approve rule decide.

## Options considered

1. **CEL conditions with cel-go**: a standard, specified language with a cost model, used for admission policies elsewhere.
   Deferred (section 6).
2. **Typed fields** (`paths` globs, `max_lines`, `max_files`, `new_files_only`, `no_deletions`): no dependency and covers the
   common asks. Each new question is a new field, and combinations beyond AND are not expressible. **Chosen.**
3. **Rego (Open Policy Agent)**: a policy language with a large engine and its own data model; much more than one condition per
   rule needs.
4. **Another Go expression library** (`github.com/expr-lang/expr`, MIT): small and with no dependencies, but with two published
   denial-of-service advisories (parser memory, builtin recursion), no cost model, and not a published standard.
5. **Starlark** (`go.starlark.net`): a full language; harder to bound, and more than a condition.
6. **Nothing**: rules stay as coarse as `allowed_extensions`.

## Consequences

- New: `Rule.Paths`, `MaxLines`, `MaxFiles`, `NewFilesOnly`, `NoDeletions`; `policy.Facts` and `policy.File`; `Evaluate` takes the
  facts with the request. `staircase policy test` and `policysim` use the same conditions because they use the engine.
- `docs/approvals.md` documents the five fields, with an example.
- No new module, no change to `go.mod`, no change to the binary's size worth measuring.

## Answers to the open questions of the first draft

1. **Accept the dependency?** No. Typed fields first, CEL deferred with the reopening conditions above.
2. **What the conditions see:** the derived facts only, never file contents.
3. **An error in a condition:** impossible for the typed fields; an invalid glob refuses the file at load.
4. **The file's version:** stays 1; an older stAirCase refuses the fields.
5. **The field's name:** not applicable until expressions return.
6. **Policy rules and sensitive paths:** decided separately in the owner decisions spec (a sensitive-path bar before the policy for
   every run); this ADR does not depend on it.
