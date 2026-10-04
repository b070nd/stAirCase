# Gates

A **gate** is a check that runs before a run starts. `staircase run` runs every gate
first and stops if any **BLOCK** gate fails. A **WARN** gate only prints a warning.

Run the gates yourself to see what a run would find:

```bash
staircase gate 1            # 1 = the case
staircase gate 1 --json     # the same report as JSON
```

The exit code is `0` when no BLOCK gate failed, `1` when one did, and `2` for a
usage error, so you can use `staircase gate` in scripts and CI.

Each gate reports `PASS`, `WARN`, `FAIL` or `SKIP`. A gate is skipped when it cannot
apply, for example when the case does not exist; another gate then reports the real
problem.

## Built-in gates

| Gate | Severity | Checks that… |
|---|---|---|
| `case.project_exists` | BLOCK | the case exists and belongs to a project |
| `case.has_stories` | BLOCK | the case has at least one pending story |
| `case.has_prd` | WARN | the case has a PRD |
| `deps.no_cycle` | BLOCK | project dependencies have no cycle |
| `deps.deps_completed` | WARN | every project this one depends on has a successful run with its current topology |
| `secret.key_file` | BLOCK | the workspace key `.key` exists, is 32 bytes and only you can read it |
| `secret.provider_keys` | BLOCK | every model in the team has its API key stored ([Models](models.md)) |
| `secret.no_duplicate_keys` | WARN | no secret name is stored twice for the project |
| `topology.exists` | BLOCK | the project has a topology |
| `topology.has_agents` | BLOCK | the topology has agents |
| `topology.supervisor_registered` | BLOCK | the supervisor is one of the agents |
| `topology.edges_valid` | BLOCK | every edge connects two existing agents |
| `topology.runtime_valid` | BLOCK | the topology's runtime type is one stAirCase can run |
| `topology.no_orphan_agents` | WARN | every agent has at least one edge (an agent with none never runs) |
| `runtime.plan_compiled` | BLOCK | the case was compiled, for this case and topology |
| `runtime.plan_pinned` | BLOCK | for a [blueprint](blueprints.md) case: the plan still matches the blueprint |
| `runtime.source_path` | BLOCK | the project's repository folder exists |
| `runtime.git_available` | BLOCK | `git` is installed |
| `runtime.no_concurrent_run` | BLOCK | no other run of the case is active |

A failing gate says how to fix it, for example
`no compiled plan - run 'staircase compile 1'`.

## Add your own gates

You can add checks of your own, in any language. A plugin gate is a program that
reads one line of JSON and writes one line of JSON.

### 1. Write the script

```sh
#!/bin/sh
read -r input                       # {"case_id": 1, "ws_dir": "/home/me/.staircase-workspace"}
echo '{"status": "PASS", "message": "all checks passed"}'
```

Make it executable (`chmod +x`). Full examples, in shell and Python, are in
[`plugins/gates/`](../plugins/gates/).

**Input** (one line on standard input):

| Field | Meaning |
|---|---|
| `case_id` | the case being checked |
| `ws_dir` | the workspace folder |

**Output** (one line on standard output):

| Field | Meaning |
|---|---|
| `status` | `PASS`, `WARN`, `FAIL` or `SKIP` (any letter case). Anything else counts as `FAIL`. |
| `message` | the text shown in the report |

### 2. Register it

List your gates in `gates.json` in the workspace (`~/.staircase-workspace/gates.json`):

```json
[
  {
    "name": "custom.license-header",
    "category": "custom",
    "severity": "WARN",
    "script": "/home/me/gates/license_header.sh",
    "timeout_seconds": 30
  }
]
```

| Field | Required | Meaning |
|---|---|---|
| `name` | yes | a unique name, e.g. `custom.license-header` |
| `category` | yes | the group shown in the report |
| `severity` | yes | `BLOCK` (stops the run) or `WARN`, in any letter case; one that is not recognised counts as `BLOCK` |
| `script` | yes | the absolute path of the program |
| `timeout_seconds` | no | default 30. The first start of a new script can take seconds on macOS; leave a margin. |

No `gates.json`, or an empty one, means no plugin gates. A `gates.json` that cannot be read
or is not valid JSON is a blocking gate that says so, not "no gates".

Start new gates as `WARN` and change them to `BLOCK` once they work reliably.

### 3. Sign the list

```bash
staircase gate sign      # writes gates.json.sig
staircase gate verify
```

Without a signature, `staircase gate` warns. With a signature that does not match
(because `gates.json` changed), the gates block the run. Sign again after each edit.

### What counts as a failure

| The script… | Result |
|---|---|
| exits 0 with valid JSON | the status it wrote |
| exits 0 with anything else | `FAIL` (malformed output) |
| exits with another code | `FAIL`, with its error output in the message |
| runs longer than `timeout_seconds` | `FAIL` (killed) |

### What the script can see

- **No environment variables** - no `PATH`, no API keys, no secrets. Use absolute
  paths for any program you call.
- **An empty, temporary working folder**, deleted afterwards.
- **Your user account.** The script runs as you and can read the workspace folder
  it is given. Only use scripts you trust, and keep them writable only by you.
