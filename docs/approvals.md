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
3. **Guards** send a change to a person, whatever the rules say, when it adds
   something risky: a file that is not text and so cannot be reviewed (an image, a
   binary: you are shown its size and digest), hidden Unicode characters that make code read differently from
   how it runs ("Trojan Source") or text only a model can read (invisible tag characters), a file made
   executable (a new script with mode 100755, or `chmod +x`), a change to dependencies (`go.mod`,
   `package.json`, lock files and the like), or what looks like a secret (private
   keys, cloud and API keys, tokens). Only what the change adds counts. The reason
   is shown as `CHECK:` and recorded with the decision.
4. **Your policy** rules (`policy.json`) can approve or reject it automatically.
5. **The agreed task**, with `--approve-in-scope`, approves in-scope file changes
   that are not sensitive (see [below](#approving-the-task-not-every-step)); with
   `--approve-on-evidence` it approves them only when the evidence is there (see
   [below](#approving-on-evidence)).
6. **A validator model** or a panel of them, if you turned one on, can decide
   in-scope file changes.
7. **A person** decides everything else.

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

The agent receives your reason and can try again. Your reason also teaches later
sessions: when a case of the same project is compiled, the plan lists the changes
people rejected before, with their reasons (the ten most recent), and every agent
reads them in its brief. They come from the audit chain, so nothing is stored in
your repository; rejections without a reason are not listed.

### Signing your decisions

A decision can carry your SSH signature, so *who* decided is provable and not just
"someone at the keyboard":

```bash
staircase claude "add a /health endpoint" --sign-approvals ~/.ssh/id_ed25519 --approval-port 8765
```

With `--sign-approvals <key>`, stAirCase signs each decision you make (in the
terminal, the browser or the API) with your key, as `--sign-as` (default: your git
`user.email`). A hardware-backed key (`ed25519-sk`) that needs a touch turns this
into a real presence check. The signature covers the run, a fresh nonce for this one
proposal, the exact request you saw (by its SHA-256) and your decision, so it cannot
be moved to another decision or replayed on a later identical proposal, and a
signature for "approve" is worthless as a rejection.

Someone else can decide through the approval API and sign on their own machine: each
pending request carries a `decision_payload`; they sign that text followed by
`approve` or `reject` with `ssh-keygen -Y sign -n staircase-decision` and post
`{"signer": "<name>", "signature": "<base64 of the armored signature>"}` with the
decision.

stAirCase checks every signature. An invalid one never approves anything. A valid one
is *trusted* when the signer is listed for that key in the workspace's
`allowed_signers` (git's format; [a team gets it from the governance
repository](governance.md)). Trusted signers are named in the change certificate, and
`staircase verify` prints them. With `--require-signed-approvals`, a person's
decision that is unsigned or not from a trusted signer is refused.

This is evidence of who decided each change. It is separate from
[two-person review](audit.md#two-person-review-cal-4), which is what reaches CAL 4.

### In your browser

```bash
staircase claude "add a /health endpoint" --approval-port 8765
```

With `--approval-port`, the run also serves a review page and prints its link,
`http://127.0.0.1:8765/#token=…`. Open it to see each waiting proposal with the
exact change (a line diff for files it rewrites, the search and replace for an
edit, the command for a shell command), any `CHECK:`, drift or reviewer note, and
approve or reject it with feedback for the agent. It works with every command that
takes `--approval-port` (`run`, `claude`, `codex`, `review`, `seal`).

The page is served only to this machine (it refuses any other host name), loads
nothing from elsewhere, and cannot be embedded in another site. The link's key is
in the part after `#`, which browsers never send to a server; the page keeps it for
the tab only and removes it from the address bar.

### One page for every session

```bash
staircase serve
```

When several sessions run at once (in different repositories or terminals, each
with its own `--approval-port`), `staircase serve` shows all their waiting proposals
on one page, each labelled with its project and run, and sends every decision to
the session it belongs to. Open the printed link; the page's key is the only one
your browser sees, and the sessions' own keys stay in the server. Sessions announce
themselves in `~/.staircase-workspace/sessions/` while they run (files only you can
read) and are removed when they end. Only sessions on this machine are ever
contacted. Sessions using another workspace (`--dir`) need their own `serve`.

### From another terminal or a script: the approval API

```bash
staircase run 1 --approval-port 8765
```

The run starts a small HTTP server on `127.0.0.1:8765` (this machine only) and prints
a token. Pass `--approval-token` to choose the token yourself.

| Request | What it does |
|---|---|
| `GET /v1/yields` | list waiting proposals (`id`, `request`, `request_sha256`, `created`) |
| `GET /v1/yields/{id}` | one waiting proposal |
| `POST /v1/yields/{id}/approve` | approve; optional body `{"feedback": "…", "request_sha256": "…"}` |
| `POST /v1/yields/{id}/reject` | reject; optional body `{"feedback": "…", "request_sha256": "…"}` |

Every request needs the header `Authorization: Bearer <token>`. A proposal's `id` is
never reused, by a later proposal or a later run. Send its `request_sha256` back with
the decision to be sure you are answering the request you read: a decision naming a
different request is refused with `409`.

```bash
curl -s -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8765/v1/yields
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  -d '{"feedback":"looks good"}' http://127.0.0.1:8765/v1/yields/<id>/approve
```

A proposal waits for you only as long as its run lives. If the run is cancelled, or
its `max_run_secs` limit passes, while a proposal is waiting (through the terminal, the page, the API
or a webhook), the wait ends, the proposal is withdrawn and a late answer is refused
(`409`); it is never recorded as an approval. When the end comes is defined by one point: an
approval is **consumed** when its decision is on the audit chain (it is kept in the journal first,
and the agent is told after). The run's limit and cancellation are checked once more under the
chain's write lock, right before that write; an approval whose run has ended by then is recorded as
a rejection, marked `unconsumed`, and never committed. A write that has begun is finished: it is
not interrupted (an interrupted write could leave you unsure whether it happened), so an approval
is consumed even if the run ends during the write, and `staircase recover` then commits it, once,
with exactly its bytes. The run's final review, asked after the
agent has finished, ends only when the run is cancelled. In the terminal dialog, **Ctrl-C**
rejects the change and stops the run like a Ctrl-C anywhere else (on Windows it rejects the
change and the run goes on to its next step; stop it from the console).

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

Any error, timeout, unreadable answer or answer that is not HTTP 2xx counts as a
rejection, and a redirect is never followed. If a secret is stored for the project
but cannot be read, the run stops rather than carry on unsigned.

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

Test a rule on your history before you use it. `staircase policy test` replays every
proposal recorded in the workspace against a policy file and shows what it would have
decided differently - above all, changes a person rejected that the rule would approve:

```bash
staircase policy test new-policy.json
```

```
42 past proposal(s) replayed against new-policy.json
  17 would be approved by the policy, as a person or the validator did before
  24 stay as they were

⚠️  a person REJECTED these, the policy would APPROVE them:
  run #7: docs/setup.md by coder
```

Shell commands, refused proposals and proposals that drift or a guard sent to a person
are never the policy's to decide, so they stay as they were. The replay applies the
rules, not the per-run limits.

Replay shows history; it does not fail. To keep a policy from drifting, write the outcomes
you require as scenarios and run them in CI:

```bash
staircase policy test policy.json --scenarios policy-scenarios.json
```

```json
{"scenarios": [
  {"name": "docs are approved without a person",
   "proposals": [{"edits": [{"file": "docs/setup.md", "content": "# Setup\n"}], "expect": "approve"}]},
  {"name": "a secret in a doc still goes to a person",
   "proposals": [{"edits": [{"file": "notes.md", "content": "AKIAABCDEFGHIJKLMNOP\n"}],
                  "expect": "human", "reason_contains": "secret"}]}
]}
```

Each scenario runs through the real admission code with this policy, so limits
(`max_auto_approved`), guards and a story's `scope` apply, in order, as in a run.
`expect` is `approve` (a rule, no person), `reject`, `refuse` (the orchestrator refuses the
edit) or `human` (a person is asked). A scenario may set `base` (files that already exist),
`scope` (the story's paths), `approve_in_scope` and `approve_on_evidence` (the run options of the
same names), `checks` (the commands that are the evidence) and `human_answer` (what a person asked about a
proposal answers, `approve` or `reject`, default `reject`) and `final_review_answer` (their answer at the final
review of the whole change, default `approve`).
The command exits non-zero on any outcome that differs, and on a file that asserts nothing.

What a scenario asserts: by default the decision of each proposal, who made it. A proposal may also say what
the agent was finally told (`"then": "approved"` or `"rejected"`, which for a proposal that went to a person is
their answer). To assert that a workflow was delivered, and not only decided, say so: `expect_delivered` lists
the files the run branch must differ from the base in (an empty list: nothing delivered), read from Git, and
`expect_certificate` requires that a change certificate was issued. For example a checkpoint
(`"limits": {"checkpoint_every": 3}` in the policy) sends every third proposal to a person, a check that fails
sends an in-scope change under `approve_on_evidence` to a person, and a final review that is rejected delivers
nothing: each is a scenario with its expected decisions and its expected delivery.

Protect the file against silent edits by signing it:

```bash
staircase policy sign     # writes policy.json.sig; runs then refuse a changed file
staircase policy verify
```

## Approving the task, not every step

```bash
staircase claude "add a /health endpoint" --allow "src/**" --approve-in-scope
```

When you agree to a task with its scope (`--allow`), you can approve the task
itself instead of each change. Changes inside the scope are then approved as part
of the agreed task, and recorded as decided by `task`. You still see:

- one in every five changes, as a spot check;
- anything outside the scope, and anything a guard flags (dependencies, hidden
  Unicode, secrets) or a limit stops;
- sensitive files (CI, build, dependency, `.env` and shell files);
- shell commands;
- **the whole change once, at the end**, before anything is committed. Rejecting it
  commits nothing.

Your policy rules still come first. This works with `claude`, `codex` and `review`.

## Approving on evidence

```bash
staircase claude "add a /health endpoint" --allow "src/**" --approve-on-evidence \
  --check "go vet ./..." --check "go test ./..." --validator openai/gpt-6-astra
```

`--approve-in-scope` approves the task and samples one change in five for you, which is
a guess about which ones need you. `--approve-on-evidence` replaces the guess with
evidence. A change inside the scope (not a sensitive file, not flagged by a guard, not
stopped by a limit) is approved only when:

- every `--check` passes, run in the sandbox on the exact state the change would produce:
  the base commit, the changes approved so far, and this one; and
- the `--validator` models, if you named any, agree.

Each check runs for at most 15 minutes, or `--check-timeout`; one that takes longer counts as
not passed, so the change comes to you ("timed out after …") rather than waiting. Set it to what
your checks need: with evidence-based approval they run once per change.

Approved on evidence, the decision is recorded as `evidence`, with each check's command,
exit code and output digest in the record, and the certificate counts it under
`decisions.evidence`. Otherwise the change comes to you with what was missing ("no
evidence to approve on: check "go test ./..." failed"), and a panel that unanimously
rejects rejects. Nothing is approved on the absence of evidence, which is why you need at
least one check or validator. Nothing is sampled for you either: you still see anything
outside the scope, sensitive files, guard hits and shell commands, and **you approve the
whole change once at the end**.

Two things to know. A check must be able to pass on each intermediate state, or you will
be asked about every change: a build, a linter, a type check and fast tests work; a test
suite that fails until three files exist does not. And the evidence is as good as your
checks: with weak tests, evidence-based approval is weak approval. A check reads the files
the agent changes, so an agent that may edit a test (or the script a check runs) can make
the check pass; keep the files your checks read out of the story's scope. The certificate
says how many decisions were made on evidence and how many by people (`staircase verify`
prints them), so a reviewer can see which it was.

## A decision model as a signal

```bash
staircase claude "add a /health endpoint" --allow "src/**" --approve-in-scope --signal typesafe-ai/jev
```

`--signal` asks an evaluation model, such as TypeSafe AI's Jev through the LLM
gateway (`LLM_GATEWAY_API_KEY`), about every change that would be approved without
you: by a rule, the agreed task or a validator. It answers typed questions with
probabilities, not text: is the change risky, does it serve a story, what kind of
change is it. It can only **send the change to you**: when it rates the change risky,
doubts it serves a story, or cannot answer. It never approves or rejects anything.
Every rating is recorded (`signal_rated`).

**A local model instead of the gateway.** [Laya](https://huggingface.co/convaiinnovations/laya)
is an open model (Apache 2.0) with a Jev-shaped server; run it yourself and nothing
leaves your machine:

```bash
pip install "laya[serve]"
LAYA_API_KEY=some-secret laya-serve            # port 8000
staircase secret set SIGNAL_API_KEY            # the same secret, read from stdin
staircase claude "…" --signal laya --signal-url http://127.0.0.1:8000
```

`--signal-url` makes stAirCase speak TypeSafe's own request shape (`POST /v1/systemone`)
to that address instead of the gateway's. `laya-serve` listens on all interfaces and
accepts anyone unless `LAYA_API_KEY` is set, so set it and keep the port closed to
your network. The change is sent to the address you give, so give one you trust. A key
is sent only when you stored `SIGNAL_API_KEY`; the gateway key is never sent there.

The model sees the stories and the change, the same as a validator. Measure it on
your own kind of changes before you rely on it: `make eval-jev` (for a local Laya:
`go run ./src/tools/jeveval -model laya -base http://127.0.0.1:8000 -api systemone`) runs 24 labelled
changes (secrets, exfiltration, weakened tests, prompt injection, Trojan Source,
drift) and reports what it missed and what it flagged needlessly.

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

**Two models that must agree.** Repeat `--validator` to make a panel, ideally from
different vendors, so one model's blind spot or one prompt injection that fools it
is not enough:

```bash
staircase review pr-42 --by "Copilot coding agent" \
  --validator openai/gpt-oss-20b --validator anthropic/claude-haiku-4.5
```

Each model reviews the change on its own. It is approved only if all of them
approve and rejected only if all of them reject; when they disagree, you decide and
see each model's reason. The decision is recorded as made by all of them
(`validator:<model>+<model>`), also in the change certificate. `--validator` works
with `run`, `claude`, `codex` and `review`.
