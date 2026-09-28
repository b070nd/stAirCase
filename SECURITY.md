# Security Policy

stAirCase is a security tool: its entire purpose is to keep autonomous agents
from making unreviewed, unprovable changes to your code. We take vulnerabilities
in it seriously.

## Reporting a vulnerability

**Please do not open a public issue for security vulnerabilities.**

Report it privately through
[GitHub's private vulnerability reporting](https://github.com/b070nd/stAirCase/security/advisories/new)
on this repository, or by email to **botond.biro.dev@gmail.com**. Include a description, reproduction steps, and the affected
version or commit. We aim to acknowledge within a few business days and will
coordinate a fix and disclosure timeline with you.

## Security model

The model is the untrusted party. Agents run inside the `staircase` process and
act only through its tools: reads are confined to the run's worktree, and every
change is a proposal the orchestrator decides and audits - the file tools can
write nothing but the bytes it derived and approved. The remaining uncontained
path is an **approved shell command**, which runs as your OS user without an OS
sandbox. See the [safety boundary](docs/safety.md).

- **Trust boundary at the orchestrator.** Model output never executes directly:
  tool calls become proposals (edits, or shell commands with
  `--allow-shell-exec`), refused, decided by policy or by a human, and recorded
  before the agent learns the answer. Finalize independently checks the worktree
  and builds the commit, so even a tool bug cannot commit unapproved bytes.
- **Content-bound approval.** The orchestrator derives the exact bytes each
  `file_edit` approval produces - from the run's base commit and precisely the
  edits the operator is shown - and never trusts the runtime's own claims
  (`content_hash` is advisory). A proposal that cannot be applied as shown (bad
  path, search text not found, file over 200 KiB) is refused before policy or a
  human sees it. Each decision, with the approved file digests, is on the audit
  chain before the runtime receives it. At finalize the worktree must hold
  exactly the approved state on the base commit - no other change in the files
  or the index, no commits of the agent's own - or the run fails
  (`approval_content_mismatch`, `unapproved_worktree_change`, `run_branch_moved`).
  The commit is built from the approved bytes, never read back from disk, and
  the run branch moves only if it still points at the base commit.
- **Repository path sandbox.** Approved paths must be plain files inside the
  run's worktree: absolute paths, `..`, any `.git` component, directories and
  paths through symlinks are refused at approval and checked again at finalize
  (recording `approval_path_escape`).
- **Webhook approvals.** With a project webhook secret, requests and responses
  are HMAC-signed within a 5-minute window, and a response must echo the
  request's fresh `yield_id` and `request_sha256`, so a captured approval cannot
  be replayed onto another request. Without a secret the channel is
  unauthenticated.
- **Tamper-evident audit chain.** Every run event is hash-chained, and each
  decision is on the chain before the agent sees it. `audit export` writes an
  Ed25519-signed checkpoint of the chain to an append-only file. Checkpoints can be anchored in a public Rekor transparency log
  (`audit export --anchor`) and re-verified (`audit verify --check-anchor`) for an
  external, append-only witness independent of the workspace key.
- **Secret handling.** Secrets are stored AES-256-encrypted and decrypted only
  when a run needs them - model API keys, to call the provider; they are never
  put in front of the model. Every access is logged, secrets of other projects
  and reserved (`__`-prefixed) keys are refused, and delivered values are
  scrubbed from every log and audit record. Approved shell commands run with a
  minimal environment (no SSH agent, cloud or model keys).
- **Shell execution off by default.** `run_shell` is not offered unless a run is
  started with `--allow-shell-exec`, a shell proposal is refused without it, and
  policy never auto-approves one; rejected attempts are audited.
- **Data leaving the machine.** A real run sends the PRD, the repository map and
  the files agents read to the configured model provider. The offline demo and
  `--replay-llm` runs send nothing. `audit export --anchor` sends the **whole**
  signed record - reasoning, paths and proposed changes - to the Rekor service;
  its public, permanent log keeps only the record's SHA-256, the signature and
  your public key. To keep the record from any third party, use your own Rekor
  instance (`--rekor-url`).
- **Supply chain.** Releases ship an SBOM and a cosign (keyless, Sigstore OIDC)
  signature over the checksums; GitHub Actions are SHA-pinned.

### Known limitations (pre-1.0)

These are documented, not hidden:

- Approved shell commands (`--allow-shell-exec`) run as the orchestrator's OS user
  **without** an OS-level sandbox: they can read and change anything that user
  can. Files they change inside the worktree fail the run unless proposed as
  edits, but effects elsewhere are not contained. Use a restricted container or
  VM for untrusted workloads.
- Each run works in its own git worktree on its own branch, so the developer's
  checkout is never touched - but a worktree is not a permission boundary for
  shell commands.
- The agent runtime runs in the orchestrator's process: its tools are part of
  the trusted code, tested but not isolated.
- The audit chain uses raw Ed25519 over canonical JSON, not yet DSSE envelopes;
  Rekor anchoring proves log inclusion and hash and signature match but not (yet) full Merkle
  inclusion-proof verification.
- Agents are named in audit records but not separately authenticated;
  per-agent identity and per-tool credential scoping are on the roadmap.
- Claude Code (`staircase claude`, `--agent claude-code`) is experimental: its
  tool calls are governed through its hooks, which reach the run through
  `staircase hook` and block on every failure; the run's token sits in a file
  only your user can read, never on a command line. Claude Code is started with
  only stAirCase's settings (no user, project or local settings and no MCP
  servers), so a repository's own `.claude` hooks do not run. Settings a
  company manages centrally still apply.
- Codex (`staircase codex`, `--agent codex`) is experimental: its edits are
  decided before they are applied, but its shell commands run without a
  decision inside Codex's own sandbox (no network, writes only in the worktree
  and temporary folders). Files they change are reviewed afterwards (CAL 2).
  The sandbox does not restrict reading, so a command can read files outside
  the repository and send them to OpenAI. Codex is started with
  `--dangerously-bypass-hook-trust`, which also runs the user's own unreviewed
  Codex hooks for that session; a run whose hooks never report in fails.
- The `--validator` reviewer is a model and can be misled by what it reviews;
  it only ever decides in-scope, non-sensitive edits, and a human approves the
  run's final change whenever it decided anything.
- Drift supervision checks paths, file counts and time, not what a change
  means: an in-scope edit can still do something no story asked for.

## Supported versions

stAirCase is pre-1.0; security fixes are applied to the latest release and `master`.
