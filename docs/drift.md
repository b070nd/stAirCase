# Drift supervision

Drift supervision keeps a run on its stories. It never approves anything by
itself: it decides which proposals a human must see, and when a run must stop.

## Scope

Each story may name the paths its runs may change:

```bash
staircase story scope <story-id> --allow 'docs/**' --allow README.md --max-files 5
```

(or `scope:` in a [blueprint](blueprints.md) — stories of a bound case take
their scope from the blueprint). A run's scope is the union of the `--allow`
globs of the case's open stories (not yet accepted); `**` spans directories.
Agents are told their stories and scope in their first message. A case whose
stories set no scope has no scope check.

## What sends a proposal to a human

A proposal that the policy would otherwise decide goes to the operator, with a
`DRIFT:` reason shown in the TUI and as `drift` in the approval API and webhook
payloads, when:

- it touches a path outside the scope;
- the run's approved changes would then touch more distinct files than
  `max_files_changed`, or than the sum of the open stories' `--max-files`;
- it is a checkpoint: every `checkpoint_every`-th proposal.

The operator's decision is audited in `yield_decided` with the drift reason, so
every override is on the chain. Shell commands always go to the operator.

## What halts a run

- More than `max_scope_violations` proposals reaching outside the scope;
- the run lasting longer than `max_run_duration` seconds (`max_run_secs` in a
  blueprint).

The run is stopped (`KILLED`), `drift_halt` is audited with the reason, and the
case does not run again until the operator has reviewed it:

```bash
staircase run <case-id> --ack-drift
```

The acknowledgement is recorded in the new run's `run_bound` event.

## Limits

Limits come from the blueprint of a bound case and from `limits` in
`$STAIRCASE_DIR/policy.json`; where both set one, the stricter applies (zero or
absent = no limit):

```json
{
  "rules": [{"action_types": ["file_edit"], "effect": "approve"}],
  "limits": {"checkpoint_every": 5, "max_files_changed": 10,
             "max_scope_violations": 2, "max_run_duration": 1800,
             "max_auto_approved": 20, "max_total_yields": 50}
}
```

`policy.json` fails closed: a file that does not parse, or has a field
staircase does not know (a misspelt limit would silently not apply), stops the
run before the agent starts.

## The drift report

Every run ends with a `drift_report` event — scope, files changed in and
outside it, scope violations, human overrides, automatic and human decisions,
and why the run halted — also written to `runs/<id>/summary.json` and
summarized in the terminal.

## Budget

A model without a known price (a new model, a gateway name the table does not
know, Claude Code) is counted at the highest known rate, so a budget cap still
stops the run; `provider/model` names are priced by their model.
