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

## staircase acknowledge

Say you looked at a run whose agent was lost, without launching anything

```
staircase acknowledge <run-id>
```

A run whose agent's launch has no established end (its process was killed, crashed or lost
its machine, or the agent did not stop when asked) is parked: nobody knows what the agent did,
outside the worktree included, so it is not launched again blindly. acknowledge records, on the
run's audit chain, that a person looked: it writes the launch's end (unknown_after_restart) if the
launch is still open, then the acknowledgement, naming the exact launch (its attempt and
generation), so it cannot cover a later loss. It launches nothing and continues nothing.

Afterwards the run can be continued: with staircase resume, or by a hosted runner's own resume
(a hosted resume never acknowledges by itself, because the acknowledgement attests that a person
looked). staircase resume --ack-unknown does both in one step.

A run with no open or lost launch is refused, and calling it again on an acknowledged launch
changes nothing. Acknowledging does not lift the limit: a run whose launches were lost 3 times
is not launched again; recover what it approved with staircase recover.

## staircase attach

Stop plain git commits in this checkout while an agent works in it (use staircase seal)

```
staircase attach [flags]
```

Installs a pre-commit hook in this repository that refuses git commit with the
way to seal the changes instead. It guards against committing an agent's work
by accident; it is not a wall (git commit --no-verify gets through, and your CI
check stays the second line). staircase attach --off removes it. An existing
pre-commit hook of your own is never overwritten.

Flags:

```
      --off   Remove the hook
```

## staircase audit anchor

Anchor a run's change certificate in a Rekor transparency log (digests only)

```
staircase audit anchor <run-id> [flags]
```

Puts the run's change certificate in a Rekor transparency log, an outside
witness that it existed at this time. Only the certificate is sent: commit
hashes, digests, counts and the level, never code, prompts or reasoning.
The log keeps its hash, the signature and your public key.

Check it later with: staircase verify <commit> --check-anchor

Flags:

```
      --rekor-url string   Rekor server URL (default "https://rekor.sigstore.dev")
```

## staircase audit export

Export a signed audit checkpoint for a completed run

```
staircase audit export <run-id> [flags]
```

Flags:

```
      --anchor             Also anchor the signed checkpoint in a Rekor transparency log (external witness). The whole record is sent to the Rekor service; its public, permanent log keeps the record's SHA-256, the signature and your public key
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

## staircase case briefing

Show the exact text an agent is told for a compiled case

```
staircase case briefing <case-id>
```

Prints what the agent of a compiled case is told: the project's instructions (from its profile), then the task (the PRD, the stories with
their scope, and the reviewers' lessons), then what the agent's own harness adds. It is the text the agents are given, composed by the same
function. An away run puts its own brief first and a continuation adds the task as it stood: those depend on the run and are not shown.

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
touched - runs never modify it. The run record and its audit chain are kept;
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

## staircase claude

Run Claude Code on a task in this repository, with every change decided by you

```
staircase claude <task> [flags]
```

Runs Claude Code on the task in a separate worktree of the git repository you
are in. Every file change and command it wants to make comes to you first. At
the end, exactly the approved changes are committed on a new branch,
staircase/run-N; your checkout is not touched.

No setup is needed: the workspace, a project for this repository and a case for
the task are created when missing. Claude Code must be installed and logged in.

--allow limits the paths the task may change; changes elsewhere come to you as
drift (see docs/drift.md).

Flags:

```
      --allow stringArray          A path (glob) the task may change; repeat for more
      --allow-shell-exec           Let Claude Code propose shell commands (each still needs your approval)
      --approval-port int          Decide from another terminal or a script through the local approval API on this port (0 = in this terminal)
      --approval-token string      Token for the approval API (default: a new one, printed)
      --approve-in-scope           Approve changes inside the --allow scope as part of the agreed task instead of one by one: 1 in 5, sensitive files and anything outside still come to you, and you approve the whole change at the end
      --approve-on-evidence        Approve changes inside the --allow scope only when the evidence is there: your --check commands pass on the state the change produces, and the --validator models agree. Otherwise the change comes to you with what was missing. Replaces the 1-in-5 sampling; needs --check and/or --validator
      --away                       Work while you are away: nobody is asked; a change is approved only when your --check commands pass on it, anything that needs you is parked, and the run ends waiting for your review (staircase decide). Needs --check and --allow
      --away-command stringArray   An exact command an away run may run without asking (repeat for more): your decision made in advance. It needs --away, --allow-shell-exec and a sandbox without network (--sandbox docker:image=... or required); it runs only as written, in the worktree's root
      --away-tool stringArray      An exact MCP tool (mcp__<server>__<tool>) an away run may call without asking (repeat for more); any other tool call is parked
      --check stringArray          A command (such as your tests) to run on the commit once it is made, in the sandbox; its result goes into the change certificate, and verify fails a failed check. Repeat for more
      --check-timeout duration     How long one --check may run, wherever checks run (approving on evidence, the definition of done, after the commit); a check that takes longer counts as not passed. Default 15m
      --deliver string             How an away run's approved change reaches its branch: review (it waits for your review: staircase decide) or policy (it is committed to the run branch, with no review, when only rules of your signed policy approved it and your checks pass; no person reviews the whole change). Policy needs --away, --check, --deliver-path, --sign-approvals, a signed policy (staircase policy sign), and a --protect path or checks that name a file of their own; it never pushes (default "review")
      --deliver-path stringArray   A path or pattern (from the repository's root) where --deliver policy may deliver, such as docs/**: a change anywhere else waits for your review. Repeat for more
      --model string               Model for Claude Code (default: its own)
      --pass-env stringArray       Name of an environment variable Claude Code may inherit, to give it its own credential (for example CLAUDE_CODE_OAUTH_TOKEN); it, and any command it runs, can read the value. Repeat for more
      --protect stringArray        A path or pattern (from the repository's root) where a --check is defined, such as tests/**: a change to it always comes to you, whatever the policy says, and is parked while away. What the checks name (the script they run, make's Makefile) is protected without it. Repeat for more
      --require-evidence           Fail the run (exit non-zero) when the commit it made has no signed change certificate and ledger; the commit stays on its branch and is reported as delivered without evidence
      --require-signed-approvals   Refuse a person's decision unless it carries an SSH signature of a signer in the workspace's allowed_signers
      --sandbox string             Where approved commands and checks run: auto (in the sandbox when this machine has one: macOS sandbox-exec, Linux bwrap or Landlock), required (refuse commands that cannot be sandboxed), off, or docker:image=<image>[,memory=<n>m|g][,cpus=<n>] (a container that sees only the worktree, without network; the image is never pulled for you) (default "auto")
      --sign-approvals string      Sign each decision you make with this SSH key (a private key file, or a public key file for ssh-agent); a key that needs a touch makes it a presence check
      --sign-as string             The name you sign decisions as (default: git user.email)
      --signal string              Evaluation model (e.g. typesafe-ai/jev via the LLM gateway) asked about every change approved without you; it can only send a change to you (risky, off the stories, or no answer), never approve one
      --signal-url string          Ask a TypeSafe-compatible server instead of the gateway, for example a local Laya (laya-serve) at http://127.0.0.1:8000; the change is sent to it (a key, if it needs one: staircase secret set SIGNAL_API_KEY)
      --unreadable stringArray     A path or pattern (from the repository's root) the agent must not read, such as auth.json, for this run, on top of the project's profile: the built-in runtime and Claude Code refuse to read it, and an agent that cannot be stopped from reading it (Codex, Gemini CLI, OpenCode) is not started. Repeat for more
      --validator stringArray      Model that reviews in-scope file edits the policy leaves open (e.g. openai/gpt-6-astra via the LLM gateway); a human approves the run's final change once. Repeat for a panel: the models must agree, otherwise a human decides
      --visible stringArray        A directory or file of your checkout that the repository ignores and the checks need (vendor/, generated/): the checks see it, read-only, in the checkout of the commit they run in. The agent's worktree never has it, and it is not part of what is approved. Repeat for more (default: the project's profile)
  -y, --yes                        Start without asking to confirm the task (needed without a terminal)
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

Aggressive cleaning never throws evidence away silently: a run branch that is
not merged into another branch is kept (it holds the only copy of the commit),
audit rows and flagged cases are written to archive/ before they are deleted (and
not deleted if that fails), and a file named legal-hold in the workspace stops
every such deletion.

--dry-run prints what would be removed without deleting anything.

Flags:

```
      --aggressive    Also remove the old Python venv and stale git branches
      --dry-run       Print targets without deleting
      --keep-failed   Preserve branches/logs for FAILED runs
```

## staircase codex

Run Codex on a task in this repository, with every change decided by you

```
staircase codex <task> [flags]
```

Runs OpenAI's Codex CLI on the task in a separate worktree of the git
repository you are in. Every file edit it wants to make comes to you first.
Its shell commands run in Codex's sandbox (no network, writes only in the
worktree), and the files a command changes come to you afterwards: kept if
you approve, reverted if not. At the end, exactly the approved changes are
committed on a new branch, staircase/run-N; your checkout is not touched.

No setup is needed. Codex must be installed (the ChatGPT app for macOS
includes it) and logged in.

Flags:

```
      --allow stringArray          A path (glob) the task may change; repeat for more
      --approval-port int          Decide from another terminal or a script through the local approval API on this port (0 = in this terminal)
      --approval-token string      Token for the approval API (default: a new one, printed)
      --approve-in-scope           Approve changes inside the --allow scope as part of the agreed task instead of one by one: 1 in 5, sensitive files and anything outside still come to you, and you approve the whole change at the end
      --approve-on-evidence        Approve changes inside the --allow scope only when the evidence is there: your --check commands pass on the state the change produces, and the --validator models agree. Otherwise the change comes to you with what was missing. Replaces the 1-in-5 sampling; needs --check and/or --validator
      --away                       Work while you are away: nobody is asked; a change is approved only when your --check commands pass on it, anything that needs you is parked, and the run ends waiting for your review (staircase decide). Needs --check and --allow
      --away-command stringArray   An exact command an away run may run without asking (repeat for more): your decision made in advance. It needs --away, --allow-shell-exec and a sandbox without network (--sandbox docker:image=... or required); it runs only as written, in the worktree's root
      --away-tool stringArray      An exact MCP tool (mcp__<server>__<tool>) an away run may call without asking (repeat for more); any other tool call is parked
      --check stringArray          A command (such as your tests) to run on the commit once it is made, in the sandbox; its result goes into the change certificate, and verify fails a failed check. Repeat for more
      --check-timeout duration     How long one --check may run, wherever checks run (approving on evidence, the definition of done, after the commit); a check that takes longer counts as not passed. Default 15m
      --deliver string             How an away run's approved change reaches its branch: review (it waits for your review: staircase decide) or policy (it is committed to the run branch, with no review, when only rules of your signed policy approved it and your checks pass; no person reviews the whole change). Policy needs --away, --check, --deliver-path, --sign-approvals, a signed policy (staircase policy sign), and a --protect path or checks that name a file of their own; it never pushes (default "review")
      --deliver-path stringArray   A path or pattern (from the repository's root) where --deliver policy may deliver, such as docs/**: a change anywhere else waits for your review. Repeat for more
      --model string               Model for Codex (default: its own)
      --protect stringArray        A path or pattern (from the repository's root) where a --check is defined, such as tests/**: a change to it always comes to you, whatever the policy says, and is parked while away. What the checks name (the script they run, make's Makefile) is protected without it. Repeat for more
      --require-evidence           Fail the run (exit non-zero) when the commit it made has no signed change certificate and ledger; the commit stays on its branch and is reported as delivered without evidence
      --require-signed-approvals   Refuse a person's decision unless it carries an SSH signature of a signer in the workspace's allowed_signers
      --sandbox string             Where approved commands and checks run: auto (in the sandbox when this machine has one: macOS sandbox-exec, Linux bwrap or Landlock), required (refuse commands that cannot be sandboxed), off, or docker:image=<image>[,memory=<n>m|g][,cpus=<n>] (a container that sees only the worktree, without network; the image is never pulled for you) (default "auto")
      --sign-approvals string      Sign each decision you make with this SSH key (a private key file, or a public key file for ssh-agent); a key that needs a touch makes it a presence check
      --sign-as string             The name you sign decisions as (default: git user.email)
      --signal string              Evaluation model (e.g. typesafe-ai/jev via the LLM gateway) asked about every change approved without you; it can only send a change to you (risky, off the stories, or no answer), never approve one
      --signal-url string          Ask a TypeSafe-compatible server instead of the gateway, for example a local Laya (laya-serve) at http://127.0.0.1:8000; the change is sent to it (a key, if it needs one: staircase secret set SIGNAL_API_KEY)
      --unreadable stringArray     A path or pattern (from the repository's root) the agent must not read, such as auth.json, for this run, on top of the project's profile: the built-in runtime and Claude Code refuse to read it, and an agent that cannot be stopped from reading it (Codex, Gemini CLI, OpenCode) is not started. Repeat for more
      --validator stringArray      Model that reviews in-scope file edits the policy leaves open (e.g. openai/gpt-6-astra via the LLM gateway); a human approves the run's final change once. Repeat for a panel: the models must agree, otherwise a human decides
      --visible stringArray        A directory or file of your checkout that the repository ignores and the checks need (vendor/, generated/): the checks see it, read-only, in the checkout of the commit they run in. The agent's worktree never has it, and it is not part of what is approved. Repeat for more (default: the project's profile)
  -y, --yes                        Start without asking to confirm the task (needed without a terminal)
```

## staircase compile

Compile a Case into the plan staircase run executes

```
staircase compile <case-id> [flags]
```

Flags:

```
      --agent string   Who runs the case: built-in (the project's topology) or an agent harness: claude-code, codex, gemini, opencode, review (default "built-in")
      --force          Overwrite an existing plan
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

## staircase decide

Review what your away runs left for you: accept, reject, defer or clarify

```
staircase decide [run-id] [flags]
```

Lists the runs that ended while you were away (staircase claude --away ...) and wait for your review, as a tree: each
run's change, the checks that ran on exactly that change, and the proposals it parked because they needed you. Nothing of it
is committed until you decide:

  Y or I   accept: the change is committed on the run's branch, with its certificate, without starting the agent again (an empty change, from a run
           that parked everything, delivers nothing: adopt a parked proposal first to have one)
  N or O   reject: nothing is committed
  D        defer, and show the alternatives: what was parked and how to see the whole change; the run keeps waiting
  Z        clarify: a read-only session (Claude Code's plan mode) in a separate directory that holds only this change, its
           evidence and what was parked; nothing said there decides anything, you come back to Y, N or D
  P        prototype a parked change: see what it would do on top of the change (its diff and your checks on the result,
           in a checkout that is thrown away); one that passes can be adopted into the change, as your approval, and you
           then decide on the new review

Without a terminal, decide one run with --accept or --reject. A decision is made on the review entry you were shown; if the
run's review is no longer that one (--shown), it is refused.

Each run is shown with its status, read from its verified audit chain: its outcome (running, awaiting review, blocked or
partial, failed, delivered, or delivered with incomplete evidence) and why, how many times its agent was started, and the
time, tokens and estimated cost it used against what is left. Your other away runs that are still running, or blocked, are
listed below the tree; staircase decide <run-id> on a run that does not await review shows its status.

Flags:

```
      --accept           Accept the run's change: commit it, with its certificate
      --adopt int        Adopt the parked proposal with this number (as listed) into the change, if your checks pass with it: a new review follows
  -m, --message string   Why, for the audit chain
      --reject           Reject the run's change: nothing is committed
      --shown string     The review entry (as listed) the decision is made on; refused if the run's review is another
```

## staircase doctor

Run diagnostics on the stAirCase workspace

```
staircase doctor
```

Checks that the workspace is healthy (directory, keys, database, git), and reports
what stAirCase works without but gains from: ssh-keygen for signed decisions and
reviews (OpenSSH 8.0 or newer), which OS sandbox works on this machine and why another
does not, and which of Claude Code, Codex, Gemini CLI and OpenCode are installed, with
their versions and, where it can be asked without a model call, whether they are logged
in. It lists what each of those adapters supports (native resume, its own sandbox, an
MCP gate, hooks, host mode), as a description and not a grant. It also lists runs that were interrupted before they committed and hold approved
changes (staircase recover), and says when a legal hold is in effect. A missing optional
tool is information, not a failed check.

## staircase gate

Run pre-flight quality gates for a case and generate a report

```
staircase gate <case-id> [flags]
```

Executes all registered quality gates against a case before running.

Gates are grouped by category (structural, security, runtime, dependency).
BLOCK gates must pass for 'staircase run' to proceed.
WARN  gates are advisory - they surface issues but do not block execution.

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

## staircase gemini

Run Gemini CLI on a task in this repository, with every change decided by you (experimental)

```
staircase gemini <task> [flags]
```

Runs Google's Gemini CLI on the task in a separate worktree of the git repository
you are in. Every file change and command it wants to make comes to you first
(commands need --allow-shell-exec); every other tool is refused. At the end,
exactly the approved changes are committed on a new branch, staircase/run-N;
your checkout is not touched.

Experimental: run for real on Gemini CLI 0.46.0 (a governed run, a held approval, a
rejection with its retry, a resume of its own session, and the control for a change
written around the hooks), otherwise tested against a stand-in. Gemini CLI must be
installed and logged in. Its commands run without a sandbox, so a run with commands reaches CAL 2.
Hooks in your own or the repository's .gemini settings still run beside
stAirCase's (Gemini cannot be told to ignore them); a run fails if Gemini never
calls stAirCase's hooks.

Flags:

```
      --allow stringArray          A path (glob) the task may change; repeat for more
      --allow-shell-exec           Let Gemini CLI propose shell commands (each still needs your approval)
      --approval-port int          Decide from another terminal, a script or your browser through the local approval API on this port (0 = in this terminal)
      --approval-token string      Token for the approval API (default: a new one, printed)
      --approve-in-scope           Approve changes inside the --allow scope as part of the agreed task instead of one by one: 1 in 5, sensitive files and anything outside still come to you, and you approve the whole change at the end
      --approve-on-evidence        Approve changes inside the --allow scope only when the evidence is there: your --check commands pass on the state the change produces, and the --validator models agree. Otherwise the change comes to you with what was missing. Replaces the 1-in-5 sampling; needs --check and/or --validator
      --away                       Work while you are away: nobody is asked; a change is approved only when your --check commands pass on it, anything that needs you is parked, and the run ends waiting for your review (staircase decide). Needs --check and --allow
      --away-command stringArray   An exact command an away run may run without asking (repeat for more): your decision made in advance. It needs --away, --allow-shell-exec and a sandbox without network (--sandbox docker:image=... or required); it runs only as written, in the worktree's root
      --away-tool stringArray      An exact MCP tool (mcp__<server>__<tool>) an away run may call without asking (repeat for more); any other tool call is parked
      --check stringArray          A command (such as your tests) to run on the commit once it is made, in the sandbox; its result goes into the change certificate, and verify fails a failed check. Repeat for more
      --check-timeout duration     How long one --check may run, wherever checks run (approving on evidence, the definition of done, after the commit); a check that takes longer counts as not passed. Default 15m
      --deliver string             How an away run's approved change reaches its branch: review (it waits for your review: staircase decide) or policy (it is committed to the run branch, with no review, when only rules of your signed policy approved it and your checks pass; no person reviews the whole change). Policy needs --away, --check, --deliver-path, --sign-approvals, a signed policy (staircase policy sign), and a --protect path or checks that name a file of their own; it never pushes (default "review")
      --deliver-path stringArray   A path or pattern (from the repository's root) where --deliver policy may deliver, such as docs/**: a change anywhere else waits for your review. Repeat for more
      --model string               Model for Gemini CLI (default: its own)
      --pass-env stringArray       Name of an environment variable Gemini may inherit, to give it its own credential (for example GEMINI_API_KEY); it, and any command it runs, can read the value. Repeat for more
      --protect stringArray        A path or pattern (from the repository's root) where a --check is defined, such as tests/**: a change to it always comes to you, whatever the policy says, and is parked while away. What the checks name (the script they run, make's Makefile) is protected without it. Repeat for more
      --require-evidence           Fail the run (exit non-zero) when the commit it made has no signed change certificate and ledger; the commit stays on its branch and is reported as delivered without evidence
      --require-signed-approvals   Refuse a person's decision unless it carries an SSH signature of a signer in the workspace's allowed_signers
      --sandbox string             Where approved commands and checks run: auto (in the sandbox when this machine has one: macOS sandbox-exec, Linux bwrap or Landlock), required (refuse commands that cannot be sandboxed), off, or docker:image=<image>[,memory=<n>m|g][,cpus=<n>] (a container that sees only the worktree, without network; the image is never pulled for you) (default "auto")
      --sign-approvals string      Sign each decision you make with this SSH key (a private key file, or a public key file for ssh-agent); a key that needs a touch makes it a presence check
      --sign-as string             The name you sign decisions as (default: git user.email)
      --signal string              Evaluation model (e.g. typesafe-ai/jev via the LLM gateway) asked about every change approved without you; it can only send a change to you (risky, off the stories, or no answer), never approve one
      --signal-url string          Ask a TypeSafe-compatible server instead of the gateway, for example a local Laya (laya-serve) at http://127.0.0.1:8000; the change is sent to it (a key, if it needs one: staircase secret set SIGNAL_API_KEY)
      --unreadable stringArray     A path or pattern (from the repository's root) the agent must not read, such as auth.json, for this run, on top of the project's profile: the built-in runtime and Claude Code refuse to read it, and an agent that cannot be stopped from reading it (Codex, Gemini CLI, OpenCode) is not started. Repeat for more
      --validator stringArray      Model that reviews in-scope file edits the policy leaves open (e.g. openai/gpt-6-astra via the LLM gateway); a human approves the run's final change once. Repeat for a panel: the models must agree, otherwise a human decides
      --visible stringArray        A directory or file of your checkout that the repository ignores and the checks need (vendor/, generated/): the checks see it, read-only, in the checkout of the commit they run in. The agent's worktree never has it, and it is not part of what is approved. Repeat for more (default: the project's profile)
  -y, --yes                        Start without asking to confirm the task (needed without a terminal)
```

## staircase governance status

Show the pinned governance commit, and whether the source or the workspace changed since

```
staircase governance status
```

## staircase governance use

Install the rules and keys of a governance repository, pinned to its current commit

```
staircase governance use <repository> [flags]
```

Flags:

```
      --ref string   Branch, tag or commit of the governance repository (default "main")
  -y, --yes          Replace without asking (needed without a terminal)
```

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

## staircase hook-template

Print the managed settings that make every agent session on a company's machines go through stAirCase

```
staircase hook-template <claude-code | codex> [flags]
```

Prints settings a company deploys through its device management so that every
Claude Code or Codex session on its machines goes through stAirCase. The hook
they install blocks every tool call outside a governed session (start one with
staircase claude or staircase codex) and governs the calls inside one.

  claude-code  managed-settings.json (it also sets allowManagedHooksOnly, so
               user and repository hooks do not load)
  codex        the [hooks] block of the managed Codex configuration
  gemini       Gemini CLI's system settings.json

--bin is where staircase is installed on those machines (default: this
program). See docs/managed.md.

Flags:

```
      --bin string   Path of staircase on the managed machines (default: this program)
```

## staircase host claude

Stand in for Claude Code: the runner's session, governed as an away run

```
staircase host claude [the runner's Claude Code arguments]
```

## staircase host setup

Set the scope, checks and credential names for hosted sessions in this workspace

```
staircase host setup [flags]
```

Flags:

```
      --allow stringArray          A path (glob) hosted sessions may change; repeat for more
      --away-command stringArray   An exact command hosted sessions may run without asking (ADR 0009); needs a sandbox without network
      --away-tool stringArray      An exact MCP tool (mcp__<server>__<tool>) hosted sessions may call without asking; other tool calls are parked
      --check stringArray          A command (such as your tests) a change must pass to be approved; repeat for more
      --pass-env stringArray       Name of an environment variable Claude Code may inherit, for its credential (for example CLAUDE_CODE_OAUTH_TOKEN); repeat for more
      --sandbox string             Where the checks run, as staircase run --sandbox (for example docker:image=golang:1.26-bookworm)
```

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

## staircase opencode

Run OpenCode on a task in this repository, with every change decided by you (experimental)

```
staircase opencode <task> [flags]
```

Runs OpenCode on the task in a separate worktree of the git repository you are
in. Every file edit, patch and command it wants to make comes to you first
(commands need --allow-shell-exec); every other tool is refused. At the end,
exactly the approved changes are committed on a new branch, staircase/run-N;
your checkout is not touched.

Experimental: run once against the real OpenCode 1.18.35 (one governed run, and a
rejection with its retry, verified in a fresh clone); otherwise tested against a stand-in
that runs the generated plugin, and no control for an ungated change has been run. OpenCode
must be installed and have a provider logged in (opencode auth login). Its
commands run without a sandbox, so a run with commands reaches CAL 2. Plugins in
the repository's .opencode folder still load beside stAirCase's (OpenCode cannot
be told to ignore them); a run fails if OpenCode never calls stAirCase's plugin.

Flags:

```
      --allow stringArray          A path (glob) the task may change; repeat for more
      --allow-shell-exec           Let OpenCode propose shell commands (each still needs your approval)
      --approval-port int          Decide from another terminal, a script or your browser through the local approval API on this port (0 = in this terminal)
      --approval-token string      Token for the approval API (default: a new one, printed)
      --approve-in-scope           Approve changes inside the --allow scope as part of the agreed task instead of one by one: 1 in 5, sensitive files and anything outside still come to you, and you approve the whole change at the end
      --approve-on-evidence        Approve changes inside the --allow scope only when the evidence is there: your --check commands pass on the state the change produces, and the --validator models agree. Otherwise the change comes to you with what was missing. Replaces the 1-in-5 sampling; needs --check and/or --validator
      --away                       Work while you are away: nobody is asked; a change is approved only when your --check commands pass on it, anything that needs you is parked, and the run ends waiting for your review (staircase decide). Needs --check and --allow
      --away-command stringArray   An exact command an away run may run without asking (repeat for more): your decision made in advance. It needs --away, --allow-shell-exec and a sandbox without network (--sandbox docker:image=... or required); it runs only as written, in the worktree's root
      --away-tool stringArray      An exact MCP tool (mcp__<server>__<tool>) an away run may call without asking (repeat for more); any other tool call is parked
      --check stringArray          A command (such as your tests) to run on the commit once it is made, in the sandbox; its result goes into the change certificate, and verify fails a failed check. Repeat for more
      --check-timeout duration     How long one --check may run, wherever checks run (approving on evidence, the definition of done, after the commit); a check that takes longer counts as not passed. Default 15m
      --deliver string             How an away run's approved change reaches its branch: review (it waits for your review: staircase decide) or policy (it is committed to the run branch, with no review, when only rules of your signed policy approved it and your checks pass; no person reviews the whole change). Policy needs --away, --check, --deliver-path, --sign-approvals, a signed policy (staircase policy sign), and a --protect path or checks that name a file of their own; it never pushes (default "review")
      --deliver-path stringArray   A path or pattern (from the repository's root) where --deliver policy may deliver, such as docs/**: a change anywhere else waits for your review. Repeat for more
      --model string               Model for OpenCode (default: its own)
      --protect stringArray        A path or pattern (from the repository's root) where a --check is defined, such as tests/**: a change to it always comes to you, whatever the policy says, and is parked while away. What the checks name (the script they run, make's Makefile) is protected without it. Repeat for more
      --require-evidence           Fail the run (exit non-zero) when the commit it made has no signed change certificate and ledger; the commit stays on its branch and is reported as delivered without evidence
      --require-signed-approvals   Refuse a person's decision unless it carries an SSH signature of a signer in the workspace's allowed_signers
      --sandbox string             Where approved commands and checks run: auto (in the sandbox when this machine has one: macOS sandbox-exec, Linux bwrap or Landlock), required (refuse commands that cannot be sandboxed), off, or docker:image=<image>[,memory=<n>m|g][,cpus=<n>] (a container that sees only the worktree, without network; the image is never pulled for you) (default "auto")
      --sign-approvals string      Sign each decision you make with this SSH key (a private key file, or a public key file for ssh-agent); a key that needs a touch makes it a presence check
      --sign-as string             The name you sign decisions as (default: git user.email)
      --signal string              Evaluation model (e.g. typesafe-ai/jev via the LLM gateway) asked about every change approved without you; it can only send a change to you (risky, off the stories, or no answer), never approve one
      --signal-url string          Ask a TypeSafe-compatible server instead of the gateway, for example a local Laya (laya-serve) at http://127.0.0.1:8000; the change is sent to it (a key, if it needs one: staircase secret set SIGNAL_API_KEY)
      --unreadable stringArray     A path or pattern (from the repository's root) the agent must not read, such as auth.json, for this run, on top of the project's profile: the built-in runtime and Claude Code refuse to read it, and an agent that cannot be stopped from reading it (Codex, Gemini CLI, OpenCode) is not started. Repeat for more
      --validator stringArray      Model that reviews in-scope file edits the policy leaves open (e.g. openai/gpt-6-astra via the LLM gateway); a human approves the run's final change once. Repeat for a panel: the models must agree, otherwise a human decides
      --visible stringArray        A directory or file of your checkout that the repository ignores and the checks need (vendor/, generated/): the checks see it, read-only, in the checkout of the commit they run in. The agent's worktree never has it, and it is not part of what is approved. Repeat for more (default: the project's profile)
  -y, --yes                        Start without asking to confirm the task (needed without a terminal)
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

## staircase policy test

Show what a policy would have decided differently on the proposals of past runs

```
staircase policy test <policy-file> [flags]
```

Replays every proposal recorded in this workspace's runs against the rules of
a policy file (the same format as policy.json) and shows where it would have
decided differently: above all, changes a person rejected that the policy would
approve. Run it before you put a new rule into policy.json.

Shell commands, proposals refused by the orchestrator and proposals that drift
or a guard sent to a person are never the policy's to decide, so they stay as
they were. The replay applies the rules only, not the per-run limits. A rule's
conditions on paths, lines, files, new files and deletions are checked against
the edits each proposal recorded, so line counts and new files are estimates
here; --scenarios checks them exactly.

The replay also applies the paths only a person may change, whatever the policy says:
the paths that hold the checks' definitions (from the terms each run saved) and the
sensitive paths. A change that a rule, the agreed task, a reviewer model or evidence
approved or rejected there, with no person, is listed in its own section as one that
would now go to a person, and a person's earlier decision on such a path is not
counted as a difference the policy makes.

With --scenarios <file> it asserts instead. Each scenario in the file (JSON: a
base tree, an optional scope, and proposals with the outcome you expect:
approve, reject, refuse or human) is run through the real admission code with
this policy, limits and guards included. The command exits non-zero when any
outcome differs or the file asserts nothing, so CI can keep a policy from
approving what it must not, or from escalating what it should approve.

Flags:

```
      --scenarios string   assert expected outcomes from this scenarios file instead of replaying past runs
```

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

## staircase project profile set

Set fields of a project's profile (a field named replaces it; --clear removes one)

```
staircase project profile set <project-id> [flags]
```

Flags:

```
      --check stringArray          A command (such as the tests) run on the commit a run makes; repeat for more
      --clear stringArray          Remove a field: protect, sensitive, scope, visible, unreadable, instructions, checks, sandbox (repeatable)
      --instructions string        What every agent on this project is told before its task (conventions, rules of the house), as text
      --instructions-file string   The same, read from a file now (the profile keeps a copy in the workspace; the file stays where it is)
      --protect stringArray        Paths or patterns, from the repository's root, where a check is defined: a change to one always needs a person
      --sandbox string             Where approved commands and checks run: auto, required, off or docker:image=<ref>
      --scope stringArray          Paths or patterns a run's changes may touch; a change elsewhere comes to a person as drift
      --sensitive stringArray      Paths or patterns a change to which always needs a person, in addition to the built-in list (a contract file, say)
      --unreadable stringArray     Paths or patterns, from the repository's root, that the agent must not read (a tracked auth.json): an agent that cannot be stopped from reading them is not started on the project
      --unreadable-defaults        Add the usual list to the unreadable paths: auth.json, .env files, private keys (it is written into the profile, which you can edit)
      --visible stringArray        Ignored directories or files of the checkout that checks need and see read-only (vendor/, generated/): literal paths, from the repository's root
```

## staircase project profile show

Show a project's profile

```
staircase project profile show <project-id>
```

## staircase project set-webhook

Set (or clear) the HITL webhook URL for a project

```
staircase project set-webhook <project-id> <url>
```

Set (or clear) the HITL webhook URL for a project.

To authenticate the webhook channel (strongly recommended - otherwise a network
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

## staircase rebuild

Check that a certified commit is exactly what its approved proposals produce

```
staircase rebuild <commit> [flags]
```

Rebuilds the commit's files from the run's ledger and compares them with the
commit. The certificate (signed by a trusted key, about exactly this commit) names
the ledger's SHA-256; the ledger lists the base commit and, in order, every
approved proposal. stAirCase replays them on the base commit, with the same rules
a run uses, and the git tree that results must be identical to the commit's tree.

This is stronger than staircase verify: verify shows who signed what was decided;
rebuild shows that what was decided is what is in the commit, byte for byte.

The ledger holds the approved content, so it stays with the author
(<workspace>/audit/run-N.ledger.json); a reviewer needs it, the certificate and
the repository. Pass another location with --ledger.

Flags:

```
      --certificate string   Read the certificate from this file instead of the git note
      --key string           Public signing key to trust (default: the workspace's and its team's)
      --ledger string        The run's ledger file (default: <workspace>/audit/run-N.ledger.json)
```

## staircase recover

Commit what an interrupted run had approved

```
staircase recover <run-id> [flags]
```

A run keeps every approval it gives, before the agent is told the answer. If the
run was interrupted (a crash, a power cut, a killed terminal) before it committed,
recover makes the commit from exactly those approvals: it takes only what the run's
audit chain also records, derives the files again from the base commit, and moves
the run's branch only if it is still where the run started. The change certificate
says the run did not finish (CAL 2 at most), and its ledger is kept, so the commit
can be rebuilt and verified like any other. If the run had already made its own commit
(it names it on its audit chain before the branch moves) and died before the run's record
was complete, recover keeps that commit and the certificate the run had issued, and only
writes what is missing.

When some of the change was approved as part of the agreed task, by evidence or by
reviewer models, you are shown the whole change and approve it once, as a finished
run would have asked, so recover needs a terminal then. The run's worktree is left
as it is. A run owns itself while its process lives (it holds a lock the system
drops when the process ends, a kill included): a run that is still alive is refused,
--force or not; one whose process is gone is recovered without --force. Only a run
started before runs held that lock needs --force, when you know its process is gone.

Recovery writes a record of what it is about to do before it touches git and names
itself in the commit it makes, so a recover that was interrupted, or whose ledger,
certificate, notes or records could not all be written, can be run again: it
finishes the missing evidence on the same commit and says what is still missing.
A commit on the run's branch that neither it nor the run named is never adopted. With
--require-evidence the command exits with an error while the evidence is incomplete.

Flags:

```
      --force              Recover a run whose record still says RUNNING (its process is known to be gone)
      --require-evidence   Fail (exit status 1) when the commit is made but its evidence is incomplete
```

## staircase replay

Print the approval decisions of a finished run, after verifying its audit chain

```
staircase replay <run-id>
```

Verify the audit chain of a run and print every approval decision in order.

Replay refuses to proceed if the hash chain is broken - this prevents
replaying a tampered run log.

## staircase report

Report how agent-written commits were governed, across one or more repositories

```
staircase report [repository...] [flags]
```

Looks at the commits on the current branch of each repository (default: the one
you are in) and reports: how many were written with an agent (an Assisted-by:
trailer or a change certificate), how many of those carry a valid certificate at
each change assurance level, which agents wrote them, and which ones have a
missing or invalid certificate or a failed check.

Certificates are read from git notes (refs/notes/staircase); fetch them first:
  git fetch origin refs/notes/staircase:refs/notes/staircase

The report decides each certificate as staircase verify does, but never fails:
use verify in CI to enforce.

Flags:

```
      --json           Print the report as JSON
      --key string     Public signing key to trust (default: the workspace's .signing.pub)
      --since string   Only commits after this (anything git log --since takes); empty for all (default "90.days")
```

## staircase resume

Continue a run that was interrupted before it committed

```
staircase resume <run-id> [flags]
```

Continues a run that was killed, crashed or stopped before it committed: the same
run, branch and worktree, under the policy, plan and options it started with (it saved
them when it started), from the state its audit chain says it stood in. What it had
approved stays approved; proposal numbers, limits and the time it had used carry on
where they were. What is approved is decided by the audit chain, never by what the agent
remembers: an agent harness continues its own session when it can (see below), and
otherwise a new session takes over, told by the chain what is already approved.

Every check is made first, and any failure refuses with nothing changed: the run's
audit chain must verify, nobody may be running it, it must not have a commit or a
recovery begun, its branch must still be at its base, and its worktree must hold exactly
the approved state. A worktree that holds anything else (what the agent did after its
last approval) refuses and names the files; --discard-unapproved puts it back first. Nothing
is ever staged or adopted. A run halted for drift needs --ack-drift. A run whose time limit
is used up cannot be continued, only recovered ("staircase recover"), and neither can a
run that began before runs saved their terms.

Every launch of the agent is claimed on the audit chain before its process starts, and
its outcome is recorded when it stops. A run whose last launch has no outcome (its
process was killed, crashed or lost its machine) is not launched again blindly: resume
records the outcome as unknown_after_restart, parks the run and says why. Check what the
agent may have done, then say you looked with "staircase acknowledge" (it launches nothing,
and a hosted runner's own resume then proceeds) or continue at once with --ack-unknown,
which does the same acknowledgement first. A run whose launches ended this way 3 times is
not launched again at all, acknowledged or not; recover what it approved instead.

A run that was started with an agent harness continues the harness's own session when it
can: Claude Code, Gemini CLI and Codex have their session id on the audit chain and are
resumed with it, so the agent remembers its conversation. When that cannot be done (the
chain names no session, or the harness cannot resume one: OpenCode) the run is refused,
never continued silently as a new conversation. --fresh-context continues it with a new
session grounded by the audit chain instead, and the chain says so.

Flags:

```
      --ack-drift               Continue a run that was halted for drift, after reviewing it (recorded on the audit chain)
      --ack-unknown             Continue a run parked because its last launch has no known outcome, after checking what its agent may have done: acknowledges the launch like staircase acknowledge, then continues (recorded on the audit chain)
      --approval-port int       Decide from another terminal, a script or your browser through the local approval API on this port (0 = in this terminal)
      --approval-token string   Token for the approval API (default: a new one, printed)
      --debug                   Write every audited message of this segment, scrubbed, to the run's debug log
      --discard-unapproved      Put the worktree back to the approved state when it holds changes nobody approved
      --fresh-context           Continue an agent-harness run in a new session grounded by the audit chain, instead of resuming its own session (the only way to continue one whose session cannot be resumed)
      --model string            Model for an agent harness (default: its own)
      --pass-env stringArray    Name of an environment variable an agent harness may inherit, to give it its own credential; it, and any command it runs, can read the value
```

## staircase review

Review changes made elsewhere (a cloud agent's pull request) and certify what you approve

```
staircase review <branch | commit> [flags]
```

Brings the changes of a branch made elsewhere, for example a pull request
opened by a cloud agent, into a separate worktree of your current branch, one
file at a time. Each changed file comes to you (or your rules) to approve or
reject; rejected files are left out. Exactly the approved files are committed
on a new branch, staircase/run-N, with a change certificate that names who made
the changes (--by) and the exact commit reviewed. The files were changed before
they were decided, so the change reaches CAL 2.

Fetch a pull request first, for example:
  git fetch origin pull/42/head:pr-42
  staircase review pr-42 --by "Copilot coding agent"

Flags:

```
      --allow stringArray          A path (glob) the changes may touch; a file elsewhere comes to you as drift
      --approval-port int          Decide from another terminal or a script through the local approval API on this port (0 = in this terminal)
      --approval-token string      Token for the approval API (default: a new one, printed)
      --approve-in-scope           Approve changes inside the --allow scope as part of the agreed task instead of one by one: 1 in 5, sensitive files and anything outside still come to you, and you approve the whole change at the end
      --approve-on-evidence        Approve changes inside the --allow scope only when the evidence is there: your --check commands pass on the state the change produces, and the --validator models agree. Otherwise the change comes to you with what was missing. Replaces the 1-in-5 sampling; needs --check and/or --validator
      --by string                  Who made the changes, for the Assisted-by trailer and the certificate (default: an external agent)
      --check stringArray          A command (such as your tests) to run on the commit once it is made, in the sandbox; its result goes into the change certificate, and verify fails a failed check. Repeat for more
      --check-timeout duration     How long one --check may run, wherever checks run (approving on evidence, the definition of done, after the commit); a check that takes longer counts as not passed. Default 15m
      --protect stringArray        A path or pattern (from the repository's root) where a --check is defined, such as tests/**: a change to it always comes to you, whatever the policy says, and is parked while away. What the checks name (the script they run, make's Makefile) is protected without it. Repeat for more
      --require-evidence           Fail the run (exit non-zero) when the commit it made has no signed change certificate and ledger; the commit stays on its branch and is reported as delivered without evidence
      --require-signed-approvals   Refuse a person's decision unless it carries an SSH signature of a signer in the workspace's allowed_signers
      --sign-approvals string      Sign each decision you make with this SSH key (a private key file, or a public key file for ssh-agent); a key that needs a touch makes it a presence check
      --sign-as string             The name you sign decisions as (default: git user.email)
      --signal string              Evaluation model (e.g. typesafe-ai/jev via the LLM gateway) asked about every change approved without you; it can only send a change to you (risky, off the stories, or no answer), never approve one
      --signal-url string          Ask a TypeSafe-compatible server instead of the gateway, for example a local Laya (laya-serve) at http://127.0.0.1:8000; the change is sent to it (a key, if it needs one: staircase secret set SIGNAL_API_KEY)
      --unreadable stringArray     A path or pattern (from the repository's root) the agent must not read, such as auth.json, for this run, on top of the project's profile: the built-in runtime and Claude Code refuse to read it, and an agent that cannot be stopped from reading it (Codex, Gemini CLI, OpenCode) is not started. Repeat for more
      --validator stringArray      Model that reviews in-scope file edits the policy leaves open (e.g. openai/gpt-6-astra via the LLM gateway); a human approves the run's final change once. Repeat for a panel: the models must agree, otherwise a human decides
      --visible stringArray        A directory or file of your checkout that the repository ignores and the checks need (vendor/, generated/): the checks see it, read-only, in the checkout of the commit they run in. The agent's worktree never has it, and it is not part of what is approved. Repeat for more (default: the project's profile)
  -y, --yes                        Start without asking to confirm (needed without a terminal)
```

## staircase run

Run a compiled case: agents work, you approve, the approved change is committed

```
staircase run <case-id> [flags]
```

Flags:

```
      --ack-drift                  Run a case whose previous run was halted for drift, after reviewing it (recorded on the audit chain)
      --agent string               Agent to run: built-in (the compiled topology), claude-code or codex (governed through their hooks; experimental) (default "built-in")
      --allow stringArray          A path (glob) this run's changes may touch, on top of the stories' scope: a change elsewhere comes to a person as drift (default: the project's profile)
      --allow-shell-exec           Enable run_shell for this run - agents may request OS-level shell execution subject to HITL approval. Shell execution is disabled by default; pass this flag to opt in.
      --approval-port int          Start an inbound HTTP approval server on this port (0 = disabled). Exposes GET /v1/yields and POST /v1/yields/{id}/approve|reject for async HITL.
      --approval-token string      Bearer token required by the approval HTTP server. If empty and --approval-port is set, a random token is generated and printed at startup.
      --check stringArray          A command (such as your tests) to run on the commit once it is made, in the sandbox; its result goes into the change certificate, and verify fails a failed check. Repeat for more
      --check-timeout duration     How long one --check may run, wherever checks run (approving on evidence, the definition of done, after the commit); a check that takes longer counts as not passed. Default 15m
      --debug                      Log every agent message (proposals, usage) to $STAIRCASE_DIR/log/
      --dry-run                    Validate and print the execution plan without running
      --metrics-addr string        Expose Prometheus metrics on this address (e.g. 127.0.0.1:9090). Empty = disabled.
      --model string               Model for an agent harness (claude-code, codex); default: the harness's own
      --otel-endpoint string       OTLP/gRPC endpoint for OpenTelemetry traces (e.g. localhost:4317). Empty = disabled.
      --protect stringArray        A path or pattern (from the repository's root) where a --check is defined, such as tests/**: a change to it always comes to you, whatever the policy says, and is parked while away. What the checks name (the script they run, make's Makefile) is protected without it. Repeat for more
      --reconcile                  Inspect orphan staircase/run-* branches (never delete them) and reconcile stale RUNNING records
      --record-llm string          Record every model exchange of this run to this file (JSON lines) for offline replay.
      --replay-llm string          File path to replay recorded LLM exchanges instead of calling the real API.
      --require-evidence           Fail the run (exit non-zero) when the commit it made has no signed change certificate and ledger; the commit stays on its branch and is reported as delivered without evidence
      --require-signed-approvals   Refuse a person's decision unless it carries an SSH signature of a signer in the workspace's allowed_signers
      --sandbox string             Where approved commands and checks run: auto (in the sandbox when this machine has one: macOS sandbox-exec, Linux bwrap or Landlock), required (refuse commands that cannot be sandboxed), off, or docker:image=<image>[,memory=<n>m|g][,cpus=<n>] (a container that sees only the worktree, without network; the image is never pulled for you) (default "auto")
      --sign-approvals string      Sign each decision you make with this SSH key (a private key file, or a public key file for ssh-agent); a key that needs a touch makes it a presence check
      --sign-as string             The name you sign decisions as (default: git user.email)
      --signal string              Evaluation model (e.g. typesafe-ai/jev via the LLM gateway) asked about every change approved without you; it can only send a change to you (risky, off the stories, or no answer), never approve one
      --signal-url string          Ask a TypeSafe-compatible server instead of the gateway, for example a local Laya (laya-serve) at http://127.0.0.1:8000; the change is sent to it (a key, if it needs one: staircase secret set SIGNAL_API_KEY)
      --skip-gates                 Bypass quality gate pre-flight (use with care)
      --unreadable stringArray     A path or pattern (from the repository's root) the agent must not read, such as auth.json, for this run, on top of the project's profile: the built-in runtime and Claude Code refuse to read it, and an agent that cannot be stopped from reading it (Codex, Gemini CLI, OpenCode) is not started. Repeat for more
      --validator stringArray      Model that reviews in-scope file edits the policy leaves open (e.g. openai/gpt-6-astra via the LLM gateway); a human approves the run's final change once. Repeat for a panel: the models must agree, otherwise a human decides
      --visible stringArray        A directory or file of your checkout that the repository ignores and the checks need (vendor/, generated/): the checks see it, read-only, in the checkout of the commit they run in. The agent's worktree never has it, and it is not part of what is approved. Repeat for more (default: the project's profile)
```

## staircase seal

Commit the staged changes an agent made in your checkout, file by file decided and certified

```
staircase seal [flags]
```

For agents that edit your checkout directly (Cursor, an IDE assistant, any
tool): stage their changes with git add, then run staircase seal instead of git
commit. Each staged file comes to you (or your rules) to approve or reject, as
in staircase review. Your current branch then moves to one new commit that holds
exactly the approved files, with your message, an Assisted-by trailer and a
change certificate (CAL 2: the files were changed before they were decided).

Rejected changes stay in your working files, uncommitted; edits you did not
stage are left alone.

Flags:

```
      --allow stringArray          A path (glob) the changes may touch; a file elsewhere comes to you as drift
      --approval-port int          Decide from another terminal or a script through the local approval API on this port (0 = in this terminal)
      --approval-token string      Token for the approval API (default: a new one, printed)
      --approve-in-scope           Approve changes inside the --allow scope as part of the agreed task instead of one by one: 1 in 5, sensitive files and anything outside still come to you, and you approve the whole change at the end
      --approve-on-evidence        Approve changes inside the --allow scope only when the evidence is there: your --check commands pass on the state the change produces, and the --validator models agree. Otherwise the change comes to you with what was missing. Replaces the 1-in-5 sampling; needs --check and/or --validator
      --by string                  Which agent made the changes, for the Assisted-by trailer and the certificate (default: an agent)
      --check stringArray          A command (such as your tests) to run on the commit once it is made, in the sandbox; its result goes into the change certificate, and verify fails a failed check. Repeat for more
      --check-timeout duration     How long one --check may run, wherever checks run (approving on evidence, the definition of done, after the commit); a check that takes longer counts as not passed. Default 15m
  -m, --message string             The commit message (default: Changes by <agent>)
      --protect stringArray        A path or pattern (from the repository's root) where a --check is defined, such as tests/**: a change to it always comes to you, whatever the policy says, and is parked while away. What the checks name (the script they run, make's Makefile) is protected without it. Repeat for more
      --require-evidence           Fail the run (exit non-zero) when the commit it made has no signed change certificate and ledger; the commit stays on its branch and is reported as delivered without evidence
      --require-signed-approvals   Refuse a person's decision unless it carries an SSH signature of a signer in the workspace's allowed_signers
      --sign-approvals string      Sign each decision you make with this SSH key (a private key file, or a public key file for ssh-agent); a key that needs a touch makes it a presence check
      --sign-as string             The name you sign decisions as (default: git user.email)
      --signal string              Evaluation model (e.g. typesafe-ai/jev via the LLM gateway) asked about every change approved without you; it can only send a change to you (risky, off the stories, or no answer), never approve one
      --signal-url string          Ask a TypeSafe-compatible server instead of the gateway, for example a local Laya (laya-serve) at http://127.0.0.1:8000; the change is sent to it (a key, if it needs one: staircase secret set SIGNAL_API_KEY)
      --unreadable stringArray     A path or pattern (from the repository's root) the agent must not read, such as auth.json, for this run, on top of the project's profile: the built-in runtime and Claude Code refuse to read it, and an agent that cannot be stopped from reading it (Codex, Gemini CLI, OpenCode) is not started. Repeat for more
      --validator stringArray      Model that reviews in-scope file edits the policy leaves open (e.g. openai/gpt-6-astra via the LLM gateway); a human approves the run's final change once. Repeat for a panel: the models must agree, otherwise a human decides
      --visible stringArray        A directory or file of your checkout that the repository ignores and the checks need (vendor/, generated/): the checks see it, read-only, in the checkout of the commit they run in. The agent's worktree never has it, and it is not part of what is approved. Repeat for more (default: the project's profile)
  -y, --yes                        Start without asking to confirm (needed without a terminal)
```

## staircase secret delete

Remove a stored secret (the one in the scope given by --project, else the global one)

```
staircase secret delete <key-name> [flags]
```

Flags:

```
      --project int   The project the secret is scoped to (0 = global)
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
key file.  It fails fast (does not wait) if any concurrent process - an active
run or a 'secret set' command - already holds a shared lock on the key.

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

## staircase serve

One review page in your browser for every running session

```
staircase serve [flags]
```

Serves a local page that lists the proposals waiting in every session that is
running with --approval-port (staircase claude, codex, review, seal, run), with
the exact change of each, and lets you approve or reject them in one place.

Sessions announce themselves in the workspace; the page needs only its own key,
which is in the link printed here (the sessions' keys never reach the browser).
The page is served only to this machine.

There is one coordinator for a workspace: a second "staircase serve" is refused and
names the first. A session whose process was killed leaves a registration behind;
this removes it, shows nothing of it, and lists the runs that were interrupted
before they committed (recover them with "staircase recover"). It touches only the
workspace, never your checkout.

Flags:

```
      --port int       Port to listen on (127.0.0.1 only) (default 8765)
      --token string   The page's key (default: a new one, in the printed link)
```

## staircase sign

Sign a commit's change certificate as the person who reviewed it (two-party review)

```
staircase sign <commit> [flags]
```

After reviewing a run's change, a second person signs its change certificate
with their SSH key (ssh-keygen -Y sign, as git does for SSH-signed commits).
The signature is added to the certificate in the commit's git note.

staircase verify --allowed-signers <file> then counts it: a CAL 3 change signed
by a trusted person who did not request the run reaches CAL 4. The file has
git's allowed_signers format: "<email> <public key>" per line.

Flags:

```
      --as string    Who is signing, as in allowed_signers (default: git user.email)
      --key string   SSH public key to sign with, its private key in ssh-agent or next to it (default: git user.signingkey, else ~/.ssh/id_ed25519.pub)
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

## staircase verify

Check that a commit, or every agent commit in a range, carries a valid change certificate

```
staircase verify <commit | range> [flags]
```

Checks the change certificate of a commit in the git repository you are in:
it must be signed by the trusted key, be about exactly this commit, and reach
the required change assurance level (--min-cal, see docs/adr/0001).

The certificate is read from the commit's git note (refs/notes/staircase),
which a run writes; fetch notes from a remote with
  git fetch origin refs/notes/staircase:refs/notes/staircase
or pass the certificate file with --certificate.

The trusted key is the workspace's public signing key (.signing.pub), or the
file given with --key: that file is all a reviewer needs. In CI, read it from a
protected ref instead: --trust-ref origin/main --key-path ci/staircase.pub (and
--signers-path for the allowed signers). That ref must not hold a commit being
verified, so a change cannot vouch for itself.

A range (main..HEAD) checks every commit in it that names an agent in an
Assisted-by: trailer; --all checks every commit. This is what a CI check on a
pull request runs.

Flags:

```
      --all                      In a range, require a certificate on every commit, not only on those that name an agent (Assisted-by:)
      --allowed-signers string   git allowed_signers file of trusted reviewers: a CAL 3 change they signed (staircase sign) and did not request reaches CAL 4 (default: the team's, from staircase governance)
      --certificate string       Read the certificate from this file instead of the git note
      --check-anchor             Also check that the certificate is in a Rekor log (see 'staircase audit anchor'); reads <certificate>.anchor, by default from the workspace
      --key string               Public signing key to trust (default: the workspace's .signing.pub and its team's keys, see staircase governance)
      --key-path string          With --trust-ref, the path of the public signing key at that ref (instead of --key)
      --ledger string            With --rebuild, read the ledger from this file instead of the git note (one commit only)
      --min-cal int              Fail below this change assurance level (1-4)
      --policy-uri string        With --vsa-out, the URI that says what the policy digest covers (default: the VSA specification's policy section)
      --rebuild                  Also rebuild each commit from its ledger (what 'staircase rebuild' does): the ledger the certificate names is read from the commit's git note (refs/notes/staircase-ledger), and a commit without one, or holding other bytes than it produces, fails
      --require-initiator        CAL 4 counts only when the person who started the run signed the request (--sign-approvals) and is listed in the trusted signers; without it the requester is the git email, which anyone can set
      --resource-uri string      With --vsa-out, the repository URI, such as git+https://github.com/org/repo (default: from the origin remote)
      --signers-path string      With --trust-ref, the path of the allowed_signers file at that ref (instead of --allowed-signers)
      --trust-ref string         Read the trusted key (--key-path) and signers (--signers-path) from this git ref, such as origin/main or a protected tag, never from the checkout: a ref that is, or contains, a commit being verified is refused, because a change must not vouch for itself
      --verifier-id string       With --vsa-out, the verifier's identity URI (default: https://github.com/b070nd/stAirCase); consumers accept a VSA only from verifiers they know
      --vsa-key string           With --vsa-out, the raw Ed25519 private key file that signs it (default: the workspace's .signing.key)
      --vsa-out string           Write a SLSA Verification Summary Attestation (VSA v1, signed DSSE) for every commit that passes, to <dir>/<commit>.vsa.json; its levels are stAirCase's own (STAIRCASE_CAL_n, STAIRCASE_REBUILT, ...), never a SLSA source level
```

## staircase version

Print the version of stAirCase

```
staircase version
```
