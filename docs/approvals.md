# Approvals

Every change an agent wants to make is a **proposal**: create, edit or delete a file,
or run a shell command. The agent stops and waits until the proposal is decided.

## What an approval means

stAirCase does not trust what the agent says it will do. For every file proposal it
computes **itself** what the file will contain after the change, starting from the
commit the run began on. You approve those exact bytes. When the run ends,
stAirCase checks that the worktree holds exactly what was approved and commits
only that. Anything else - a file nobody approved, a change after the approval, a
commit made by the agent - fails the run, and nothing is committed.

An edit changes only the lines it shows you, in the file's own line endings (LF,
or CRLF for a Windows-style file). The one exception is a file that already mixes
both: there an edit also turns every line ending into LF, a change the proposal
screen does not show line by line.

## Who decides, in order

1. **stAirCase refuses** a proposal it cannot apply as shown: a path outside the
   repository or inside `.git`, a symlink, text to replace that is not in the file,
   or a new file larger than 200 KiB.
2. **Drift supervision** can stop the run, or send the proposal to a person - for a
   change outside the stories' scope, too many files, or a checkpoint. See
   [Drift supervision](drift.md).
3. **Your policy** rules (`policy.json`) can approve or reject it automatically.
4. **A validator model**, if you turned one on, can decide in-scope file changes.
5. **A person** decides everything else.

Shell commands always go to a person.

Each decision is written to the [audit chain](audit.md) **before** the agent learns
it. If the record cannot be written, the proposal is rejected and the run stops.

## Deciding as a person

A person can decide in one of three ways. Pick one per run.

### In the terminal (default)

The run shows each proposal: the agent's reasoning, and for each file the text it
replaces and the new text. Press:

| Key | Result |
|---|---|
| `y` | approve |
| `n` (or `q`, `Esc`) | reject: type a reason and press `Enter`; `Esc` goes back |

The agent receives your reason and can try again.

### From another terminal or a script: the approval API

```bash
staircase run 1 --approval-port 8765
```

The run starts a small HTTP server on `127.0.0.1:8765` (this machine only) and prints
a token. Pass `--approval-token` to choose the token yourself.

| Request | What it does |
|---|---|
| `GET /v1/yields` | list waiting proposals (`id`, `request`, `created`) |
| `GET /v1/yields/{id}` | one waiting proposal |
| `POST /v1/yields/{id}/approve` | approve; optional body `{"feedback": "…"}` |
| `POST /v1/yields/{id}/reject` | reject; optional body `{"feedback": "…"}` |

Every request needs the header `Authorization: Bearer <token>`.

```bash
curl -s -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8765/v1/yields
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  -d '{"feedback":"looks good"}' http://127.0.0.1:8765/v1/yields/<id>/approve
```

### From a service: a webhook

stAirCase can send each proposal to your service and use its answer:

```bash
staircase project set-webhook 1 https://review.example.com/staircase   # 1 = the project
staircase project set-webhook 1                                        # remove it again
```

For each proposal, stAirCase sends a `POST` with the proposal as JSON - the same
fields the approval API shows, plus a fresh `yield_id`. Your service must answer
**within 30 seconds** with:

```json
{ "approved": true, "feedback": "optional text for the agent" }
```

Any error, timeout or unreadable answer counts as a rejection.

**Sign the webhook.** Without a shared secret, anyone who can reach or intercept the
connection could forge an approval. Store a secret for the project:

```bash
printf %s "$WEBHOOK_SECRET" | staircase secret set __webhook_hmac_secret__ --project 1
```

With the secret set:

- every request has the headers `X-Staircase-Timestamp` (Unix seconds),
  `X-Staircase-Signature` (`sha256=` + hex HMAC-SHA256 of
  `timestamp + "." + body`, keyed with the secret) and
  `X-Staircase-Request-SHA256` (hex SHA-256 of the body);
- your answer must be signed the same way, be no older than 5 minutes, and repeat
  the request's `yield_id` and `request_sha256` in its body. An answer that does not
  match is rejected, so a recorded approval cannot be replayed for another proposal.

## Approving automatically with rules

Put a `policy.json` in the workspace (`~/.staircase-workspace/policy.json`). Rules
are checked in order; the first rule that matches decides. A proposal no rule matches
goes on to the validator or a person.

```json
{
  "version": 1,
  "rules": [
    { "action_types": ["file_edit"], "allowed_extensions": [".md"], "effect": "approve" },
    { "agent_names": ["intern"], "effect": "reject" }
  ],
  "limits": { "max_auto_approved": 20, "max_total_yields": 50 }
}
```

| Rule field | Meaning (all given fields must match) |
|---|---|
| `action_types` | `file_edit` (files; shell commands are never auto-approved) |
| `allowed_extensions` | every file in the proposal has one of these extensions |
| `agent_names` | the proposal comes from one of these agents |
| `min_confidence` | the agent's confidence is at least this (0–1) |
| `effect` | `approve` (default) or `reject` |

`limits.max_auto_approved` and `limits.max_total_yields` send every later proposal to
a person once reached. The drift limits also live here - see
[Drift supervision](drift.md#limits).

The file is strict: if it does not parse, or has a field stAirCase does not know,
the run stops before it starts. A reject rule with no conditions at all would reject
everything; it is refused unless you set `"allow_blanket_deny": true`.

Protect the file against silent edits by signing it:

```bash
staircase policy sign     # writes policy.json.sig; runs then refuse a changed file
staircase policy verify
```

## Letting a model review changes: the validator

```bash
staircase run 1 --validator claude-haiku-4-5
```

The validator is a model (any model stAirCase supports, with its key stored as a
secret) that decides file changes the policy leaves open, in place of a person.

- It sees only the change (each file before and after) and the case's stories -
  never the agent's own explanation.
- Its rejection goes back to the agent as review feedback.
- A person decides instead - and sees the validator's note - for drift, shell
  commands, sensitive files (CI, build, dependency, `.env` and shell files), files
  over 32 KiB, an unclear answer, after two rejections in a row, and for every fifth
  validator approval, as a spot check.
- If the validator approved anything, a person approves the **whole** change of the
  run once, at the end, before it is committed. Rejecting it commits nothing.

The validator is a model and can be wrong or misled by what it reads. It is a way to
save your time on small in-scope changes, not a replacement for your final review.
