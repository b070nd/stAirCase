# Drift supervision

Agents sometimes wander: they "fix" a file nobody asked about, or keep going long
after the job is done. Drift supervision keeps a run on its stories.

It never approves anything itself. It only decides two things:

- which proposals **a person must see**, even if a rule or a validator would
  otherwise decide them;
- when a run **must stop**.

## Scope: where a story may make changes

Give each story the paths it may change:

```bash
staircase story scope 1 --allow 'src/**' --allow README.md --max-files 5   # 1 = the story
```

In a [blueprint](blueprints.md), the same goes in each story's `scope:`.

- `**` matches any number of folders; `*` matches within one folder.
- The run's scope is all the `--allow` paths of the case's open stories (the ones
  not yet accepted), together.
- Agents are told their stories and scope in their first message.
- If no story of the case has a scope, there is no scope check.

## When a person must decide

A proposal goes to a person, marked as **drift** with the reason, when:

- it changes a path outside the scope;
- the run would then change more different files than `max_files_changed`, or than
  the `--max-files` of the open stories added up;
- it is a checkpoint: every `checkpoint_every`-th proposal.

The person's decision, with the drift reason, is written to the
[audit chain](audit.md), so every override is on record.

## When a run stops

A run stops (status `KILLED`) when:

- more than `max_scope_violations` proposals reached outside the scope, or
- it ran longer than `max_run_secs` seconds.

The event `drift_halt` records why. The case then does not run again until you
confirm that you looked at what happened:

```bash
staircase run 1 --ack-drift
```

The confirmation is recorded in the new run's first audit event.

## Limits

Set the limits in the blueprint (`limits:`), or in `policy.json` in the workspace:

```json
{
  "rules": [],
  "limits": {
    "checkpoint_every": 5,
    "max_files_changed": 10,
    "max_scope_violations": 2,
    "max_run_secs": 1800
  }
}
```

| Limit | Meaning |
|---|---|
| `checkpoint_every` | every Nth proposal goes to a person |
| `max_files_changed` | more different files than this go to a person |
| `max_scope_violations` | more out-of-scope proposals than this stop the run |
| `max_run_secs` | a longer run is stopped |

`0` or no value means no limit. If the blueprint and `policy.json` both set a limit,
the stricter one counts. `policy.json` is strict: a misspelled limit stops the run
before it starts, instead of being silently ignored. The other fields of
`policy.json` are explained in [Approvals](approvals.md#approving-automatically-with-rules).

## The drift report

Every run ends with a `drift_report`: the scope, the files changed inside and
outside it, scope violations, overrides by a person, how many decisions were made
automatically and by a person, and why the run stopped, if it did. You find it on
the audit chain, in `runs/<run-id>/summary.json` in the workspace, and at the end
of the run in the terminal.
