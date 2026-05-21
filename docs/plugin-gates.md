# Plugin Gates

stAirCase supports **script-based plugin gates** that run alongside the built-in
quality gates. Plugin gates execute as isolated subprocesses and communicate
via JSON over stdin/stdout, so they can be written in any language.

## Quick start

1. Write a script that reads a JSON line from stdin and writes a JSON line to stdout.
2. Make it executable (`chmod +x`).
3. Register it in `$STAIRCASE_DIR/gates.json`.
4. Run `staircase gate <case-id>` — your gate appears in the report.

## `gates.json` format

Place this file at `$STAIRCASE_DIR/gates.json`:

```json
[
  {
    "name":            "custom.my-check",
    "category":        "custom",
    "severity":        "WARN",
    "script":          "/absolute/path/to/my_check.sh",
    "timeout_seconds": 30
  },
  {
    "name":            "custom.python-check",
    "category":        "security",
    "severity":        "BLOCK",
    "script":          "/absolute/path/to/check.py",
    "timeout_seconds": 60
  }
]
```

| Field | Required | Description |
|-------|----------|-------------|
| `name` | yes | Unique dot-separated identifier, e.g. `custom.my-check` |
| `category` | yes | Display category (shown in gate report) |
| `severity` | yes | `BLOCK` (prevents run) or `WARN` (advisory) |
| `script` | yes | Absolute path to an executable file |
| `timeout_seconds` | no | Default: 30. Gate FAIL if exceeded. |

A missing or empty `gates.json` is not an error — zero plugin gates are loaded.

## Plugin protocol

### Input (stdin)

The gate runner writes one JSON line to the plugin's stdin:

```json
{"case_id": 42, "ws_dir": "/home/user/.staircase-workspace"}
```

| Field | Type | Description |
|-------|------|-------------|
| `case_id` | integer | ID of the case being gated |
| `ws_dir` | string | Staircase workspace directory path |

### Output (stdout)

The plugin must write one JSON line to stdout before exiting:

```json
{"status": "PASS", "message": "all checks passed"}
```

| Field | Type | Values |
|-------|------|--------|
| `status` | string | `PASS`, `WARN`, `FAIL`, or `SKIP` |
| `message` | string | Human-readable explanation shown in the gate report |

### Exit behaviour

| Exit code | Stdout | Outcome |
|-----------|--------|---------|
| 0 | valid JSON | status from JSON |
| 0 | malformed | `FAIL` — "plugin malformed output: …" |
| non-zero | any | `FAIL` — stderr included in message |
| timeout | — | `FAIL` — "plugin error: signal: killed" |

## Sandbox

Plugin scripts run in a strict sandbox (CHECK 11.3, 11.6):

- **Empty environment**: no `PATH`, no `STAIRCASE_DIR`, no API keys or secrets.
  Use absolute paths for any external tools.
- **Fresh working directory**: a new temporary directory, deleted after execution.
  Do not rely on cwd persisting between runs.
- **Timeout enforced**: the process is killed after `timeout_seconds`.

## Examples

See [`plugins/gates/`](../plugins/gates/) in this repository for:

- `example_check.sh` — minimal shell plugin
- `example_check.py` — minimal Python plugin

## Security notes

- Plugin scripts receive `ws_dir` (the workspace path) via JSON stdin so they
  can inspect project state. They do **not** receive secrets, API keys, or
  database credentials — the sandbox blocks environment inheritance and the IPC
  socket is not available to plugins.
- `ws_dir` is intentionally shared so gates can read topology/config files; treat
  any world-readable workspace content as accessible to gate scripts.
- Scripts are executed with the OS user that ran `staircase gate`. Ensure
  plugin scripts are owned and writable only by trusted users.
- The `severity: BLOCK` setting will prevent `staircase run` when the gate
  fails, so test plugins thoroughly before promoting them to BLOCK severity.
