# Compatibility

Agents, models and stAirCase's own internals change often. Six interfaces are what
other tools, scripts and your CI build on, so they change carefully. This page says
what each one is and what you can rely on.

## Before and after 1.0

stAirCase is before 1.0 (see [CHANGELOG](../CHANGELOG.md)).

- **Now:** the change certificate v1 is stable. stAirCase will keep producing it,
  or keep accepting it after a v2 exists. The other five may still change in a
  minor version. Every change to them is listed in the CHANGELOG with what to do.
- **From 1.0:** all six follow semantic versioning. A breaking change needs a new
  major version, or a new version of the format that is accepted alongside the old
  one for at least one major version.

## The six contracts

| Contract | What it is today | What you can rely on |
|---|---|---|
| **Change certificate** | in-toto Statement v1 in a DSSE envelope, predicate type `…/0002-change-certificate.md#v1` ([specification](spec/certificate-v1.md), [test vectors](spec/vectors)) | Stable now. New optional fields may be added; verifiers ignore unknown fields. A change of meaning gets `#v2`, and v1 stays accepted. |
| **Hook bridge** | `staircase hook <agent> [--governed \| --require]`: the agent's hook JSON on stdin, the answer on stdout, exit 2 blocks; the session is found through `STAIRCASE_HOOK_FILE` ([ADR 0003](adr/0003-hook-bridge.md)) | Command, flags, exit codes and the environment variable keep working; new agents are added as new `<agent>` names. |
| **Proposal and decision records** | The audit chain's events, such as `yield_request` and `yield_decided`, and the signed checkpoint `staircase audit export` writes ([audit evidence](audit.md)) | A checkpoint that verifies today verifies with later versions. Events may gain fields; existing fields keep their meaning. |
| **Decision-model interface** | Typed questions (`boolean`, `choice`, `score`) about a change, answered with probabilities, through the gateway's `/v1/evaluate` (`--signal`) | Signals can only send a change to a person. That rule does not loosen without a new, opt-in flag. |
| **Verifier results** | `checks` in the certificate: command, exit code, sandboxed, output digest (`--check`) | Existing fields keep their meaning. Linking full test reports as in-toto test-result attestations is planned, as an addition. |
| **Policy file** | `policy.json`: rules that approve or reject proposals, and limits ([approvals](approvals.md)) | A valid policy file keeps its meaning. `staircase policy test` shows what a policy decides on your past runs before you rely on a change. |

Everything else, including the command-line output, the workspace layout, the
database and the agents' prompts, can change in any version.

## Which agents have actually been run

A stand-in agent that speaks an agent's hook protocol proves stAirCase's side of the
bridge. It says nothing about what a real version of that agent does. This is what
has been exercised against the real thing:

| Agent | Tested against the real program | Not yet |
|---|---|---|
| Codex CLI | 0.155 and 0.158: two paid runs (an edit decided before it ran, a command's changes reviewed after, certificate CAL 2 verified); all flags accepted | a refused Stop (definition of done) in a real turn |
| Claude Code | 2.1.236, logged out: the settings stAirCase passes, and that a repository's own hooks are ignored | a logged-in run; the sandbox settings end to end (`make smoke-claude`) |
| Gemini CLI | nothing: a stand-in, built from its documentation | everything; work in progress |
| OpenCode | nothing: a stand-in that runs the generated plugin under Node, built from its documentation | everything; work in progress |

Agent versions change often. A newer version than the one listed may behave
differently; run the smoke test for your agent (`make smoke-claude`, `make smoke-codex`)
after upgrading it.
