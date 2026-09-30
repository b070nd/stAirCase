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
      --check stringArray          A command (such as your tests) to run on the commit once it is made, in the sandbox; its result goes into the change certificate, and verify fails a failed check. Repeat for more
      --model string               Model for Claude Code (default: its own)
      --require-evidence           Fail the run (exit non-zero) when the commit it made has no signed change certificate and ledger; the commit stays on its branch and is reported as delivered without evidence
      --require-signed-approvals   Refuse a person's decision unless it carries an SSH signature of a signer in the workspace's allowed_signers
      --sign-approvals string      Sign each decision you make with this SSH key (a private key file, or a public key file for ssh-agent); a key that needs a touch makes it a presence check
      --sign-as string             The name you sign decisions as (default: git user.email)
      --signal string              Evaluation model (e.g. typesafe-ai/jev via the LLM gateway) asked about every change approved without you; it can only send a change to you (risky, off the stories, or no answer), never approve one
      --signal-url string          Ask a TypeSafe-compatible server instead of the gateway, for example a local Laya (laya-serve) at http://127.0.0.1:8000; the change is sent to it (a key, if it needs one: staircase secret set SIGNAL_API_KEY)
      --validator stringArray      Model that reviews in-scope file edits the policy leaves open (e.g. openai/gpt-6-astra via the LLM gateway); a human approves the run's final change once. Repeat for a panel: the models must agree, otherwise a human decides
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
      --check stringArray          A command (such as your tests) to run on the commit once it is made, in the sandbox; its result goes into the change certificate, and verify fails a failed check. Repeat for more
      --model string               Model for Codex (default: its own)
      --require-evidence           Fail the run (exit non-zero) when the commit it made has no signed change certificate and ledger; the commit stays on its branch and is reported as delivered without evidence
      --require-signed-approvals   Refuse a person's decision unless it carries an SSH signature of a signer in the workspace's allowed_signers
      --sign-approvals string      Sign each decision you make with this SSH key (a private key file, or a public key file for ssh-agent); a key that needs a touch makes it a presence check
      --sign-as string             The name you sign decisions as (default: git user.email)
      --signal string              Evaluation model (e.g. typesafe-ai/jev via the LLM gateway) asked about every change approved without you; it can only send a change to you (risky, off the stories, or no answer), never approve one
      --signal-url string          Ask a TypeSafe-compatible server instead of the gateway, for example a local Laya (laya-serve) at http://127.0.0.1:8000; the change is sent to it (a key, if it needs one: staircase secret set SIGNAL_API_KEY)
      --validator stringArray      Model that reviews in-scope file edits the policy leaves open (e.g. openai/gpt-6-astra via the LLM gateway); a human approves the run's final change once. Repeat for a panel: the models must agree, otherwise a human decides
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

## staircase doctor

Run diagnostics on the stAirCase workspace

```
staircase doctor
```

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

Run Gemini CLI on a task in this repository, with every change decided by you (work in progress)

```
staircase gemini <task> [flags]
```

Runs Google's Gemini CLI on the task in a separate worktree of the git repository
you are in. Every file change and command it wants to make comes to you first
(commands need --allow-shell-exec); every other tool is refused. At the end,
exactly the approved changes are committed on a new branch, staircase/run-N;
your checkout is not touched.

Work in progress: built from Gemini CLI's documentation and tested against a
stand-in, not yet against a real login. Gemini CLI must be installed and logged
in. Its commands run without a sandbox, so a run with commands reaches CAL 2.
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
      --check stringArray          A command (such as your tests) to run on the commit once it is made, in the sandbox; its result goes into the change certificate, and verify fails a failed check. Repeat for more
      --model string               Model for Gemini CLI (default: its own)
      --require-evidence           Fail the run (exit non-zero) when the commit it made has no signed change certificate and ledger; the commit stays on its branch and is reported as delivered without evidence
      --require-signed-approvals   Refuse a person's decision unless it carries an SSH signature of a signer in the workspace's allowed_signers
      --sign-approvals string      Sign each decision you make with this SSH key (a private key file, or a public key file for ssh-agent); a key that needs a touch makes it a presence check
      --sign-as string             The name you sign decisions as (default: git user.email)
      --signal string              Evaluation model (e.g. typesafe-ai/jev via the LLM gateway) asked about every change approved without you; it can only send a change to you (risky, off the stories, or no answer), never approve one
      --signal-url string          Ask a TypeSafe-compatible server instead of the gateway, for example a local Laya (laya-serve) at http://127.0.0.1:8000; the change is sent to it (a key, if it needs one: staircase secret set SIGNAL_API_KEY)
      --validator stringArray      Model that reviews in-scope file edits the policy leaves open (e.g. openai/gpt-6-astra via the LLM gateway); a human approves the run's final change once. Repeat for a panel: the models must agree, otherwise a human decides
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
  gemini       Gemini CLI's system settings.json (work in progress)

--bin is where staircase is installed on those machines (default: this
program). See docs/managed.md.

Flags:

```
      --bin string   Path of staircase on the managed machines (default: this program)
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

Run OpenCode on a task in this repository, with every change decided by you (work in progress)

```
staircase opencode <task> [flags]
```

Runs OpenCode on the task in a separate worktree of the git repository you are
in. Every file edit, patch and command it wants to make comes to you first
(commands need --allow-shell-exec); every other tool is refused. At the end,
exactly the approved changes are committed on a new branch, staircase/run-N;
your checkout is not touched.

Work in progress: built from OpenCode's documentation and tested against a
stand-in that runs the generated plugin, not yet against a real login. OpenCode
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
      --check stringArray          A command (such as your tests) to run on the commit once it is made, in the sandbox; its result goes into the change certificate, and verify fails a failed check. Repeat for more
      --model string               Model for OpenCode (default: its own)
      --require-evidence           Fail the run (exit non-zero) when the commit it made has no signed change certificate and ledger; the commit stays on its branch and is reported as delivered without evidence
      --require-signed-approvals   Refuse a person's decision unless it carries an SSH signature of a signer in the workspace's allowed_signers
      --sign-approvals string      Sign each decision you make with this SSH key (a private key file, or a public key file for ssh-agent); a key that needs a touch makes it a presence check
      --sign-as string             The name you sign decisions as (default: git user.email)
      --signal string              Evaluation model (e.g. typesafe-ai/jev via the LLM gateway) asked about every change approved without you; it can only send a change to you (risky, off the stories, or no answer), never approve one
      --signal-url string          Ask a TypeSafe-compatible server instead of the gateway, for example a local Laya (laya-serve) at http://127.0.0.1:8000; the change is sent to it (a key, if it needs one: staircase secret set SIGNAL_API_KEY)
      --validator stringArray      Model that reviews in-scope file edits the policy leaves open (e.g. openai/gpt-6-astra via the LLM gateway); a human approves the run's final change once. Repeat for a panel: the models must agree, otherwise a human decides
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
staircase policy test <policy-file>
```

Replays every proposal recorded in this workspace's runs against the rules of
a policy file (the same format as policy.json) and shows where it would have
decided differently: above all, changes a person rejected that the policy would
approve. Run it before you put a new rule into policy.json.

Shell commands, proposals refused by the orchestrator and proposals that drift
or a guard sent to a person are never the policy's to decide, so they stay as
they were. The replay applies the rules only, not the per-run limits.

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
      --by string                  Who made the changes, for the Assisted-by trailer and the certificate (default: an external agent)
      --check stringArray          A command (such as your tests) to run on the commit once it is made, in the sandbox; its result goes into the change certificate, and verify fails a failed check. Repeat for more
      --require-evidence           Fail the run (exit non-zero) when the commit it made has no signed change certificate and ledger; the commit stays on its branch and is reported as delivered without evidence
      --require-signed-approvals   Refuse a person's decision unless it carries an SSH signature of a signer in the workspace's allowed_signers
      --sign-approvals string      Sign each decision you make with this SSH key (a private key file, or a public key file for ssh-agent); a key that needs a touch makes it a presence check
      --sign-as string             The name you sign decisions as (default: git user.email)
      --signal string              Evaluation model (e.g. typesafe-ai/jev via the LLM gateway) asked about every change approved without you; it can only send a change to you (risky, off the stories, or no answer), never approve one
      --signal-url string          Ask a TypeSafe-compatible server instead of the gateway, for example a local Laya (laya-serve) at http://127.0.0.1:8000; the change is sent to it (a key, if it needs one: staircase secret set SIGNAL_API_KEY)
      --validator stringArray      Model that reviews in-scope file edits the policy leaves open (e.g. openai/gpt-6-astra via the LLM gateway); a human approves the run's final change once. Repeat for a panel: the models must agree, otherwise a human decides
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
      --allow-shell-exec           Enable run_shell for this run - agents may request OS-level shell execution subject to HITL approval. Shell execution is disabled by default; pass this flag to opt in.
      --approval-port int          Start an inbound HTTP approval server on this port (0 = disabled). Exposes GET /v1/yields and POST /v1/yields/{id}/approve|reject for async HITL.
      --approval-token string      Bearer token required by the approval HTTP server. If empty and --approval-port is set, a random token is generated and printed at startup.
      --check stringArray          A command (such as your tests) to run on the commit once it is made, in the sandbox; its result goes into the change certificate, and verify fails a failed check. Repeat for more
      --debug                      Log every agent message (proposals, usage) to $STAIRCASE_DIR/log/
      --dry-run                    Validate and print the execution plan without running
      --metrics-addr string        Expose Prometheus metrics on this address (e.g. 127.0.0.1:9090). Empty = disabled.
      --model string               Model for an agent harness (claude-code, codex); default: the harness's own
      --otel-endpoint string       OTLP/gRPC endpoint for OpenTelemetry traces (e.g. localhost:4317). Empty = disabled.
      --reconcile                  Inspect orphan staircase/run-* branches (never delete them) and reconcile stale RUNNING records
      --record-llm string          Record every model exchange of this run to this file (JSON lines) for offline replay.
      --replay-llm string          File path to replay recorded LLM exchanges instead of calling the real API.
      --require-evidence           Fail the run (exit non-zero) when the commit it made has no signed change certificate and ledger; the commit stays on its branch and is reported as delivered without evidence
      --require-signed-approvals   Refuse a person's decision unless it carries an SSH signature of a signer in the workspace's allowed_signers
      --sandbox string             Where approved shell commands run: auto (in the sandbox when this machine has one: macOS sandbox-exec, Linux bwrap or Landlock), required (refuse commands that cannot be sandboxed) or off (default "auto")
      --sign-approvals string      Sign each decision you make with this SSH key (a private key file, or a public key file for ssh-agent); a key that needs a touch makes it a presence check
      --sign-as string             The name you sign decisions as (default: git user.email)
      --signal string              Evaluation model (e.g. typesafe-ai/jev via the LLM gateway) asked about every change approved without you; it can only send a change to you (risky, off the stories, or no answer), never approve one
      --signal-url string          Ask a TypeSafe-compatible server instead of the gateway, for example a local Laya (laya-serve) at http://127.0.0.1:8000; the change is sent to it (a key, if it needs one: staircase secret set SIGNAL_API_KEY)
      --skip-gates                 Bypass quality gate pre-flight (use with care)
      --validator stringArray      Model that reviews in-scope file edits the policy leaves open (e.g. openai/gpt-6-astra via the LLM gateway); a human approves the run's final change once. Repeat for a panel: the models must agree, otherwise a human decides
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
      --by string                  Which agent made the changes, for the Assisted-by trailer and the certificate (default: an agent)
      --check stringArray          A command (such as your tests) to run on the commit once it is made, in the sandbox; its result goes into the change certificate, and verify fails a failed check. Repeat for more
  -m, --message string             The commit message (default: Changes by <agent>)
      --require-evidence           Fail the run (exit non-zero) when the commit it made has no signed change certificate and ledger; the commit stays on its branch and is reported as delivered without evidence
      --require-signed-approvals   Refuse a person's decision unless it carries an SSH signature of a signer in the workspace's allowed_signers
      --sign-approvals string      Sign each decision you make with this SSH key (a private key file, or a public key file for ssh-agent); a key that needs a touch makes it a presence check
      --sign-as string             The name you sign decisions as (default: git user.email)
      --signal string              Evaluation model (e.g. typesafe-ai/jev via the LLM gateway) asked about every change approved without you; it can only send a change to you (risky, off the stories, or no answer), never approve one
      --signal-url string          Ask a TypeSafe-compatible server instead of the gateway, for example a local Laya (laya-serve) at http://127.0.0.1:8000; the change is sent to it (a key, if it needs one: staircase secret set SIGNAL_API_KEY)
      --validator stringArray      Model that reviews in-scope file edits the policy leaves open (e.g. openai/gpt-6-astra via the LLM gateway); a human approves the run's final change once. Repeat for a panel: the models must agree, otherwise a human decides
  -y, --yes                        Start without asking to confirm (needed without a terminal)
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
file given with --key: that file is all a reviewer needs.

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
      --ledger string            With --rebuild, read the ledger from this file instead of the git note (one commit only)
      --min-cal int              Fail below this change assurance level (1-4)
      --rebuild                  Also rebuild each commit from its ledger (what 'staircase rebuild' does): the ledger the certificate names is read from the commit's git note (refs/notes/staircase-ledger), and a commit without one, or holding other bytes than it produces, fails
      --require-initiator        CAL 4 counts only when the person who started the run signed the request (--sign-approvals) and is listed in the trusted signers; without it the requester is the git email, which anyone can set
```

## staircase version

Print the version of stAirCase

```
staircase version
```
