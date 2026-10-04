# Changelog

All notable changes to stAirCase are documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). This project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed (read before upgrading)

- **The hook command ends in `|| exit 2`.** Agents (Claude Code documents it) carry on when a hook fails with any
  exit status but 2, so a hook program that was missing, could not run, crashed or was killed let the tool call
  through. The command every agent calls is now `'<staircase>' hook <agent> --governed || exit 2`. Codex trusts a
  hook by the hash of its definition: it will ask once to trust the new one. If you deployed managed settings
  (`staircase hook-template`), generate them again.
- **The verify Action pins the verifier it installs, and outside a pull request it needs `trust-ref`.**
  The `version` input must be a release tag (`v0.7.1`); `v1.2`, `latest` or `main` used to become "the latest
  release" and are now an error. Without the input the action's own release tag is used; used at a floating ref it
  installs the latest release and warns. The build attestation is checked against this repository's release workflow
  and the tag (`--signer-workflow`, `--source-ref`), not just "some attestation of this repository". Outside a pull
  request without `trust-ref` the action refuses instead of reading the key from the checkout, and the pull request's
  base is fetched whatever `range` is, so a custom range cannot change where the key comes from.

### Fixed

- **A run's time limit also ends the automatic work of a decision, and cancellation reaches signing and recovery.**
  `max_run_secs` ended a person's wait, but a check that decides a change (evidence), a reviewer model, a signal and the
  signing of a decision ran under the run's cancellation only, so the run sat in a long check past its limit; they are now
  bound by the limit too, and an approval is not consumed (journaled, put on the audit chain, answered) once the run is
  over. `ssh-keygen` (signing a decision, a run's initiator request) is stopped when its context ends instead of waiting
  forever for a passphrase or a touch. `staircase recover` now honours Ctrl-C and cancellation: it delivers nothing and
  begins no operation when cancelled before delivery, and an answer to its final review that arrives after cancellation
  counts for nothing. The final review at the end of a run is still bound by cancellation only, not by the agent's limit.
- **A hook answer that is not a decision blocks, and so do hook calls it cannot read.** `staircase hook` printed
  whatever the run answered. An empty, cut-off or unrecognised answer to "may this tool run?" is read by agents as no
  objection, so it is now a block; so is a hook call that is not JSON, names no event, or names an event the agent's
  hook is not registered for (which is not sent to the run at all). A real allow or deny, and the empty or `{}`
  acknowledgement of the other events, pass through unchanged.
- **A tool call that names one argument twice with different values is refused.** The Gemini CLI mapping
  silently overwrote `path` with `dir_path` (and kept both names), OpenCode and Claude Code read whichever
  came first, so what was approved could differ from what the host ran. Names and aliases that agree give one
  deterministic call; ones that disagree give none. A Write or Edit that names no file is refused.
- **A recovery has an identity, and can be repaired.** `staircase recover` writes a record of the
  operation before it touches git and names it in its commit (`Staircase-Recovery:`), so it knows a
  commit is its own by that name, the base as the only parent and the tree: a commit someone else made
  with the same tree and a copied trailer, or an extra parent, is a conflict and no longer skips the
  final review. The final review is recorded on the audit chain before any commit (if it cannot be, nothing
  is committed). If the ledger, the certificate, a note, the audit record, the summary or the run's record
  cannot be written, the commit stays and the command lists what is missing (`--require-evidence` makes
  that a failure); running it again repairs the same commit, where before the run's record naming the commit
  made any repair impossible. Two recoveries of one run cannot run at once. The certificate of a recovered run
  names the policy the run loaded and the signed request of whoever started it (new `policy_snapshot` and
  `initiator_signed` audit records), not the policy file of the day.
- **`staircase recover` refuses a history it cannot fully trust.** It now verifies the run's audit
  chain before believing a row of it, applies the approvals in the order the chain recorded (a
  journal whose lines were reordered gave a different tree), and refuses, committing nothing, when
  an approval the chain holds is missing from the journal or differs from it, when the journal has a
  repeated line, a corrupt line with others after it, or cannot be read, or when the audit history
  is one no run could have written. A last journal line cut short by the crash, or written but never
  audited, is still tolerated, so a legitimately interrupted run remains recoverable.
- **The price table was out of date, and in places below what the providers charge.** A budget cap
  counts tokens at these prices, and `claude-haiku-4-5` was priced at $0.80 / $4 per million
  tokens against the published $1 / $5, so a run on it could spend a quarter more than its cap. The
  table is re-read from Anthropic's, OpenAI's and Google's price pages (2026-10-05), with the Claude 5
  family, current GPT and Gemini models, and the pro reasoning models, which cost far more than
  the rest. Claude Opus 4.5 and 4.6 were priced at $15 / $75 against the published $5 / $25, which
  stopped runs earlier than the cap said. A dated model name
  (`claude-haiku-4-5-20251001`) or `-latest` is priced as the model it pins instead of falling back
  to the ceiling. A model not in the table still counts at $15 / $75.

### Added

- **A policy can be asserted, not only replayed.** `staircase policy test <policy> --scenarios <file>`
  runs scenarios (a tree, a scope, proposals and the outcome expected: approve, reject, refuse or a
  person) through the real admission code, limits and guards included, and exits non-zero when an
  outcome differs or the file asserts nothing. See [approvals](docs/approvals.md).
- **A failing CI test names itself in an annotation.** The CI and release workflows run their tests
  through `packaging/test-report.sh`, which adds an error annotation listing the failing tests, so a
  failure on a runner can be read without signing in to read the job log.

## [0.7.1] - 2026-10-05

Two fixes after v0.7.0.

### Fixed

- **`clean --aggressive` removes whole runs from the audit log, never the front of one.** Beyond a
  million rows it deleted the oldest rows, which could cut a run's chain in the middle and leave one
  that no longer verified. It now removes the rows of the oldest complete runs, archived first as
  before; a run that is still running, or that has an entry newer than the cut (a story accepted
  later), stays whole, so fewer rows than the excess may go.
- **The repository map in a plan describes the last commit, not the working tree.** Agents work
  on the last commit, but the map walked the folder, so it listed files that were never
  committed (a `credentials.json`, scratch files) by name and quoted the `func` lines of edits
  that were not committed, and it missed what a commit holds that the folder no longer does. In
  a git repository with a commit it is now the map of that commit, built with one git process;
  a folder that is not a repository is walked as before.

## [0.7.0] - 2026-10-05

Hardening release: a review of the whole code base, each finding reproduced by a test first, and the audit chain now covers the event type (read "Changed").

### Changed (read before upgrading)

- **The audit chain covers the event type (chain version 2).** The hash of each event was over
  the payload, the previous hash and the commit, so an event could be relabelled, a
  `yield_decided` renamed to something else, without breaking the chain. New events hash the
  event type too, with every part length-prefixed ([ADR 0004](docs/adr/0004-audit-chain-version-2.md),
  [the format](docs/audit.md#the-hash-chain)). Runs written before keep verifying as they did
  (they are version 1 and do not cover the event type), checkpoints already exported and signed
  still verify, and a run may hold both. The database gets a `hash_version` column; an older
  stAirCase will refuse a workspace written by this one. Conformance vectors:
  `docs/spec/audit-chain-vectors.json`, checked by `docs/spec/audit_chain_vectors.py`.

### Added

- **`staircase secret delete <key> [--project N]`.** A stored secret could only be replaced,
  never removed.
- **`--check-timeout`.** How long one `--check` may run (default 15 minutes), wherever checks
  run: approving on evidence, the definition of done and after the commit. A check that takes
  longer counts as not passed: the change goes to a person with "timed out", and the
  certificate records the check as one that could not run. With approve-on-evidence, which runs
  the checks once per change, a short limit stops a hung check holding up a whole session.
- **`staircase doctor` reports the optional tools and interrupted runs.** Besides the
  workspace checks it now lists ssh-keygen (and whether OpenSSH is new enough to sign,
  8.0), which OS sandbox works on this machine and why another does not, and whether Claude
  Code, Codex, Gemini CLI and OpenCode are installed, their versions and (Claude Code and
  Codex, which can say without a model call) whether they are logged in. It points at runs
  interrupted before they committed that hold approved changes (`staircase recover N`), and
  at a legal hold. A missing optional tool never fails the check.
- **Changes to files that are not text.** An image, a Latin-1 source file or any other
  whole new file that is not valid UTF-8 can now be approved, which `seal`, `attach` and
  any command that writes such a file previously could not (the proposal was refused). It
  travels as base64 in `content_b64` (up to 2 MiB), the ledger becomes version 2 only when
  it holds one, and a person is shown its size and SHA-256 instead of unreadable bytes. A
  guard sends it to a person whatever the rules, the agreed task or the models say, and it
  is committed byte for byte and rebuilds like any other change. The rebuild
  specification and its vectors (now 21, checked by the Go code and the independent Python
  implementation) cover it. Search-and-replace edits of a binary file are not supported.

### Fixed

- **The blocking gates that read the database no longer skip when they cannot.** The
  no-concurrent-run and no-dependency-cycle gates answered "skipped", which does not block, when
  the store returned an error; they now fail.
- **The repository map in a plan does not follow symbolic links.** A link to a file outside the
  repository put that file's `func` lines into the plan, which goes to the model.
- **Plugin gates cannot fail open over a spelling or a broken file.** A plugin that answered
  `"fail"` (lower case) or any status gates do not have was counted as skipped and the run
  went on; a `"severity": "block"` in lower case, or one with a typo, only advised; and an invalid
  `gates.json` meant no plugin gates at all. Statuses and severities are read in any letter case,
  an unknown status is a `FAIL`, an unknown severity blocks, and a `gates.json` that cannot be
  loaded blocks the run and says why.
- **What an agent or a repository wrote cannot drive your terminal.** The review screen, the
  output of checks, `staircase verify`, `report` and `inspect log` printed text from agents and
  from commit messages as it was, escape sequences included, which can clear the screen, move the
  cursor or overwrite a line so that it says something else than the proposal. Control characters
  are now shown as symbols (ESC as ␛).
- **A rejection that has no files in the audit log is still a lesson.** The lessons given to the
  next plan began with an empty file list (`: reason`) when the proposal was too large to be
  recorded with its files.
- **The workspace database is readable by its owner only.** It was created with the process's
  umask (usually world-readable) and holds every proposal, decision and ciphertext of the
  workspace. It and its `-wal` and `-shm` files are now `0600`, and an existing workspace is
  tightened the next time a command opens it.
- **A model provider's address cannot redirect your key elsewhere.** The HTTP client followed
  redirects, and a custom key header (`x-api-key`) travels with them. Redirects are not followed.
- **Webhook signatures made with an empty secret are refused.** An HMAC with an empty key is one
  anybody can compute.
- **An oversized audit entry stays valid JSON.** An entry over 64 KiB (a large proposal) was cut
  at a byte, which left text that was not JSON and could end inside a character. It is now a
  small record that says it was truncated, with the original size and SHA-256.
- **Redaction handles a secret that contains another.** Secrets were replaced in the order
  they were delivered, so with `abc` and `abcdef` the first pass left `def` readable. Longest
  first now, in the audit log and in what a person is shown.
- **`staircase init` repairs a missing public signing key.** A crash between writing the private
  and the public key left a workspace that could not verify its own certificates, and init
  (which stops when the private key exists) never fixed it. The public key is the private
  key's second half and is written again.
- **An older stAirCase refuses a workspace written by a newer one.** The database records its
  migrations; one this version does not know now stops the command with "upgrade stAirCase"
  instead of working on a layout it may damage.
- **A sandboxed command cannot signal your other processes.** In the macOS sandbox and with
  bubblewrap a command could stop or kill any process of yours (stAirCase itself, your editor);
  macOS now allows signals only inside the sandbox, and bubblewrap runs the command in its own
  process namespace, where your other processes are not visible (and their environments in
  /proc are not readable). The Landlock engine does not restrict signals before Landlock ABI 6
  (Linux 6.12). The sandbox also hides more credential stores: `~/.vault-token`, cargo, terraform,
  OCI, pass, doppler, glab, stripe, heroku, Maven settings and shell history.
- **Pull requests for repositories with a dot in their name.** `staircase push` read the
  repository name of `github.com/vercel/next.js` as `next` (and `socket.io` as `socket`), so it
  opened the pull request, and sent the GitHub token, to another repository's name.
- **The secret guard knows more credentials.** Stripe live keys, GitLab and npm tokens, Hugging
  Face tokens, SendGrid keys and Slack webhook URLs now send a change to a person like the others.
- **An anchor must be signed by a trusted key, and `audit verify --rekor-url` is honoured.**
  Checking a record against the Rekor log accepted an entry made with any key, so anyone could
  anchor a copy of your record and have it pass; the logged key must now be the workspace's
  (`audit verify`) or a trusted key (`verify --check-anchor`). And the `--rekor-url` flag of
  `audit verify` was ignored in favour of the address in the sidecar file, which anyone can edit.
- **A negative limit no longer beats a real one, and a policy rule with an unknown `effect` is
  refused.** When a blueprint and policy.json both set a limit the stricter applies, but a
  negative number counted as the smaller one and then meant "no limit". Negative limits now
  count as none. A rule whose `effect` was misspelt (`allow`, `Approve`) was read as a
  rejection, the opposite of what it says; loading the policy now fails and names the rule.
- **`project config set --budget-cap` refuses a negative or non-numeric amount.** It stored
  the value and reported "set to $-5.00/run" while capping nothing.
- **A governed Gemini CLI cannot read outside the project with the older argument name.**
  `read_file` called with `absolute_path` (the name older Gemini CLI versions use) had no path
  to check and was allowed; it is now read as `file_path` and checked, and a `read_file` that
  names no file is refused.
- **An empty file added by a Codex patch is empty.** `*** Add File:` with no lines was
  proposed as a file holding one newline, so the approved bytes were not the bytes Codex wrote
  and the run failed at the end.
- **The approval API needs `Authorization: Bearer <key>`, and a broken decision decides nothing.**
  The key alone, without "Bearer ", was accepted. An approve or reject whose JSON body was
  malformed (cut short, say, before the `request_sha256` that names the request) approved
  or rejected anyway; it is now refused with 400 and the proposal stays pending. An empty
  body is still fine.
- **`staircase seal` reports left-out files correctly.** A file with a space or an accent in
  its name was listed as "left out, still in your working files" even though it was sealed.
- **Sensitive files match in any letter case and cover more build and CI files.** A person
  always decides a change to a file that decides what runs, but `makefile` or `DOCKERFILE`
  was not recognised (macOS and Windows treat them as the same file), and `GNUmakefile` (which
  GNU make reads before `Makefile`), `Jenkinsfile`, `.circleci`, `.husky`, `.gitlab`,
  `.gitattributes`, `.gitmodules`, `.npmrc`, `.pre-commit-config.yaml`, `justfile`,
  `.travis.yml` and `azure-pipelines.yml` were not on the list.
- **Guards cover invisible tag characters and a file made executable.** Text hidden in
  Unicode tag characters (U+E0000 to U+E007F), which a person cannot see and a model reads, and
  the other invisible marks (left-to-right and right-to-left marks, invisible math operators),
  now send the change to a person like the other hidden characters. So does a change that
  makes a file executable, which the review of a file's content did not show.
- **A change of a file's mode is part of the change.** In review-after and attach, a new
  executable script was committed as `100644`, and a `chmod +x` with the same content was
  not noticed. A whole-file change now carries `mode` (`100644` or `100755`), the ledger is
  version 2 when any change has one, and the rebuild reproduces the mode (vectors 22-25).
- **A symlink in the worktree is never read.** `review-after` followed a symlink the agent
  left and put the file it pointed to into the proposal and the audit chain; it is now a
  change that is shown by its path and is never read.
- **A blueprint in the governance repository cannot write outside its folder.** A git tree
  may hold an entry named `..`; importing such a blueprint is refused.
- **`staircase recover` ends the case of a crashed run.** The case stayed RUNNING for good.
- **The final review shows a file that is not text by its size and digest.** The whole-change
  review at the end of a run (and of a `recover`) listed such a file as its raw bytes, garbled
  by the encoding and as large as the file. It now shows what the proposal showed.
- **Ctrl-C works in the terminal approval dialog.** In raw mode Ctrl-C is a key, not a
  signal, and the dialog ignored it: an operator could not stop a run that was waiting for
  them. It now rejects the change and stops the run (SIGINT, as outside the dialog). The
  dialog also ends, rejecting the change, when the run is cancelled or its time limit
  passes, which closes the last open part of that finding, and backspace in the feedback
  line removes a character, not a byte.

## [0.6.0] - 2026-10-01

Reproducible, not just signed, and hardened. A commit can be rebuilt from the
proposals that were approved for it, and a CI check can require that. An interrupted
run's approved changes can be recovered. In-scope changes can be approved on evidence
(your checks, reviewer models) instead of a sample, and a verified commit gets a signed
SLSA summary. People can sign their individual decisions and the run itself. Every
finding of an external assurance review that could be reproduced is fixed, and drills
that end processes with `kill -9` back the claims about recovery and key rotation.

What could not be verified is written down: behaviour under a real GitHub ruleset
(squash and rebase merges, forks, merge queues) and on a Linux kernel with old Landlock
were reasoned about, not run. Codex is exercised against the real program (0.159);
Gemini CLI and OpenCode arrive as work in progress, built from their documentation and
tested against stand-ins only, and a logged-in Claude Code run is not verified.

### Changed (read before upgrading)

- **The verify Action's defaults are stricter.** It reads the key and allowed
  signers from the pull request's base branch (or `trust-ref`) instead of the
  checkout, and `all` now defaults to `true`: every commit needs a certificate. Set
  `all: false` for the old advisory behaviour. The documented workflow uses
  `b070nd/stAirCase@v0.6.0`; 0.5.0 of the Action still reads the key from the PR.
- **A certificate that claims `cal` outside 1 to 3 is refused**, however validly it
  is signed. stAirCase itself never wrote one.
- **Proposal ids in the approval API are strings with a random part**
  (`3-x7k2...`), not counting numbers. Treat them as opaque.
- **A proposal whose text is not valid UTF-8 is refused** before it is decided,
  because the ledger could not reproduce it.
- **Linux: a kernel with Landlock older than ABI 3** (before 6.2) no longer counts
  as a sandbox. `--sandbox required` refuses commands there and `auto` runs them
  unsandboxed and says so; install bubblewrap.
- **The workspace database commits with `synchronous=FULL`.**
- **Every approval is journaled first.** A run writes each approved change to
  `journal/run-N.approved.jsonl` in the workspace before the agent is told the answer;
  if it cannot be written, the approval is refused and the run stops. Back the
  directory up with the rest of the workspace.

### Added

- **Team blueprints from the governance repository.** `blueprints/<name>/` in the
  governance repository is checked like `blueprint import` checks a folder (a symlink or
  submodule inside it is refused too) and imported by `staircase governance use` as a
  snapshot whose source commit is the pinned one, so everyone gets the same blueprint
  under the same hash. A bad blueprint stops the whole install; a blueprint removed later
  keeps its snapshot for the runs bound to it. `governance status` lists them.

- **`staircase recover <run-id>`.** A run keeps each approval it gives, written before
  the agent is told the answer; if the run is interrupted before it commits, `recover`
  commits exactly what it had approved. It trusts a journal line only when the audit
  chain records the same approval (the request's hash and who decided), derives the
  files again from the base commit, moves the branch only if it is still at the run's
  base, asks you for the final review when part of the change was approved by the task,
  evidence or reviewer models, and issues a certificate that says the run did not finish
  (CAL 2 at most, ledger included). An approval that cannot be written to the journal is
  refused and the run stops. The agent itself is not resumed.

- **Approve on evidence.** `--approve-on-evidence` (with `--allow`, and `--check`
  and/or `--validator`) approves an in-scope change only when every check passes in the
  sandbox on the exact state the change would produce (the base, the approved changes
  and this one) and the reviewer models agree; otherwise it comes to you with what was
  missing. It replaces the 1-in-5 sampling of `--approve-in-scope`, is recorded as
  `evidence` with each check's result, and the whole change is still approved by you
  once at the end. Nothing is approved on the absence of evidence.

- **SLSA verification summaries.** `staircase verify --vsa-out <dir>` writes a signed
  SLSA Verification Summary Attestation (VSA v1, DSSE) for every commit that passes:
  the commit and its tree, the repository (`--resource-uri`, else the origin remote),
  the verifier (`--verifier-id`), a policy digest over the parameters it ran with, the
  attestations it used (certificate, ledger) and levels of its own
  (`STAIRCASE_CAL_n`, `STAIRCASE_REBUILT`, ...). It claims no SLSA source level, and
  nothing is written for a commit that failed. See `docs/spec/vsa-v1.md`.

- **An authenticated initiator.** A run started with `--sign-approvals` signs its own
  request (who, run, base commit, plan) and the certificate carries that signature as
  `initiator`. `verify` checks it, never lets the initiator (by name, by git email or
  by key) be their own second party, and `--require-initiator` (Action input
  `require-initiator`) makes CAL 4 count only when the initiator proved who they are
  and is trusted. This closes the "two different strings are not two people" gap for
  keys you trust; it is still keys, not humans.

- **Real-agent evidence.** `./demo/smoke.sh codex-stop` runs a real Codex through a
  held (70 s) and rejected approval, its retry, and a Stop refused until a failing
  `--check` passes; every smoke run now also verifies and rebuilds the commit in a
  fresh clone that has only the public signing key. `docs/compatibility.md` has the
  per-agent, per-scenario table of what was actually run (Codex 0.159: all of the
  above) and what was not, with how to repeat it.

- **Evidence outcomes and `--require-evidence`.** A run that makes a commit now says
  whether it is `certified` or `delivered_without_evidence` (and why) in its summary,
  and records an `evidence_failed` event when the ledger or certificate could not be
  written, including when the workspace has no signing key. `--require-evidence`
  makes that case exit non-zero while the commit stays reported as delivered.

- **Protected trust for the verify Action, and `verify --rebuild`.** The Action now
  reads the trusted key (and the new `allowed-signers` input) from the pull request's
  base branch, or `trust-ref`, instead of the pull request's own checkout, and checks
  every commit by default (`all: false` restores the advisory behaviour, and
  `verify` says how many commits it skipped). A run attaches its ledger to the commit
  as a git note (`refs/notes/staircase-ledger`); `staircase verify --rebuild` (Action
  input `rebuild`) replays it and fails a commit whose tree is not the one the
  ledger produces. `docs/audit.md` states the limits: squash and rebase merges, forks,
  who may push notes.

- **`staircase opencode "task"` (work in progress):** OpenCode as the governed agent,
  through a plugin generated per run that posts every tool call to the run and throws
  to block it. Built from its documentation and tested with a stand-in that runs the
  plugin under Node, not yet against a real login. Edits, patches and commands are
  decided before they run, every other tool is refused. `run --agent opencode`.

- **`staircase gemini "task"` (work in progress):** Gemini CLI as the governed agent,
  built from its documentation and tested against a stand-in, not yet against a real
  login. Edits and commands are decided before they run, every other tool is refused,
  and the hook bridge takes `--file` because Gemini sanitizes its hooks' environment.
  `run --agent gemini`, `hook-template gemini`.

- `--signal-url <address>`: ask a TypeSafe-compatible server, such as a local Laya
  (`laya-serve`), instead of the gateway. stAirCase then speaks TypeSafe's own shape
  (`POST /v1/systemone`), sends a key only if `SIGNAL_API_KEY` is stored, and
  `jeveval -api systemone` measures such a server on the same 24 cases.

- **Signed decisions.** `--sign-approvals <ssh key>` (with `--sign-as`) signs each
  decision you make; the approval API also takes a `signer` and `signature`, over
  the request's `decision_payload`. The signature covers the run, the exact request
  and the decision (approve or reject); an invalid one never approves anything,
  and `--require-signed-approvals` refuses unsigned decisions and signers that
  the workspace's `allowed_signers` does not list. The certificate counts signed
  decisions and names the trusted signers, and `verify` prints them.

- **`staircase rebuild <commit>`:** reproducible, not just signed. Every run keeps a
  ledger (the base commit and every approved proposal, in order); the certificate
  carries its SHA-256 (and the digest of the policy in effect). `rebuild` replays it
  on the base commit with the same rules a run uses, and the git tree must be
  identical to the commit's, so a commit that holds anything nobody approved fails
  even with a valid signature. The specification describes the replay, with 14
  conformance vectors and an independent Python implementation run in CI.

### Fixed

- **`recover` can be interrupted and run again.** A drill that kills `recover` itself at
  random moments found a dead end: killed after it made its commit but before it wrote the
  ledger, the certificate and the run's record, a second `recover` failed for good, because the
  branch had already moved. It now recognizes its own commit (on the base, with exactly the
  tree the approvals produce, naming an audit chain head), skips the commit and the final
  review it already had, and finishes the rest, with the commit's own chain head in the
  certificate. The run's record is written last.

- **A killed key rotation no longer leaves key material behind.** A drill that kills a
  rotating process 150 times found that a kill between creating the new key's temp file and
  using it left `.key-rotate-*` files in the workspace. The next `secret rotate` now removes
  them. (The kill drills are in `docs/testing.md`: they also confirm that `recover` commits
  exactly the approved changes after a real `kill -9`, and that no secret is ever stranded
  by a rotation killed at a random moment.)

- **Two sessions starting in one repository at the same time could fail.** Git is
  not safe against two `worktree add` at once (it could fail reading another's
  half-written `.git/worktrees/run-N/commondir`), so one run would end FAILED at
  the start. Creating and removing run worktrees is now serialized, within a
  process and across processes (a lock file in the repository's git directory). It
  showed up as a test that failed about one time in forty.

- **Decisions are durable, and releases need a green build.** The workspace database
  commits with `synchronous=FULL`, so a decision acknowledged to the agent is on disk
  before it is reported (macOS caveat in `docs/governance.md`). The release workflow
  now refuses a tag whose commit is not on the default branch or has no successful CI
  run (`packaging/release-gate.sh`). The governance and compatibility docs state what
  identity, rollout, revocation and agent testing do and do not establish, and a
  restored copy of the workspace is tested to verify.
- **Sandbox confidentiality, stated and tightened.** The agents' own login stores
  (`~/.claude`, `~/.codex`, `~/.gemini`, OpenCode's) are hidden from sandboxed
  commands like `~/.ssh`. On Linux, a kernel whose Landlock is older than ABI 3
  (before 6.2) no longer counts as a sandbox: it cannot stop a command truncating
  files outside its folders, so `--sandbox required` refuses and `auto` runs
  unsandboxed and says so (install bubblewrap). `docs/safety.md` has a
  confidentiality profile table.
- **`clean --aggressive` no longer destroys evidence silently.** It keeps a run branch
  that is not merged into another branch (the only copy of the commit), archives
  flagged cases and pruned audit rows to `archive/` before deleting them (and deletes
  nothing if the archive fails), and stops entirely while a `legal-hold` file is in the
  workspace.
- **Key rotation cannot be left half done unnoticed.** After an interrupted
  `secret rotate` whose outcome is ambiguous, the workspace key is refused (by runs,
  `secret set` and `doctor`) until `secret rotate` resolves it. Before, `secret set`
  kept working with the old key and stored a secret that the finished rotation then
  stranded. Tested with a real database and a simulated kill at both points.
- **A producer cannot claim CAL 4.** A certificate that says `cal` 4 (or anything
  outside 1 to 3) is refused however validly it is signed; level 4 is only ever
  established by a verifier from a trusted reviewer's signature. Conformance vectors
  12 and 13, the independent verifier and the specification say so, and the docs
  no longer describe CAL 4 as proven two-person control: the requester is an
  unauthenticated git email.
- **One read of the policy file.** The rules, the signature check and the digest the
  certificate records come from a single read of `policy.json`, so a file replaced
  while a run is going changes neither the decisions nor the digest.
- **Webhook approvals fail closed.** A stored webhook secret that cannot be looked up
  or decrypted stops the run instead of falling back to an unsigned channel; only an
  HTTP 2xx answer counts (a signed approval body on a 500 no longer approves) and a
  redirect is not followed.
- **A decision answers one proposal.** Proposal ids carry a random part instead of a
  counter that restarts at 1, and the review page sends back the hash of the request
  it showed, so a card left over from an ended session cannot decide a new one. The
  text a person signs has a fresh nonce per proposal, so a captured signature is
  refused on a later identical proposal. (Signed decisions are new since 0.5.0.)
- **A wait for a person ends with the run.** Cancelling a run, or its `max_run_secs`
  passing, while it waits for a person (page, API, webhook or final review) ends
  the wait, withdraws the proposal and refuses a late answer; it is never recorded
  as an approval. A cancelled run ends KILLED and commits nothing.
- **Changes the ledger cannot reproduce are refused.** A proposal whose file name or
  text is not valid UTF-8 is refused before it is decided, instead of being approved
  and then failing `rebuild`.

## [0.5.0] - 2026-09-29

Review where you already work. Approve changes in your browser, on one page for
every running session; commit what a hook-less agent like Cursor did with a
certificate; share a team's rules and keys from one repository; and see whether
approvals were given with attention. The certificate format now has a written
specification with test vectors.

### Added

- **A review page in your browser.** With `--approval-port`, a run serves a page
  on that port and prints its link. It shows each waiting proposal with the exact
  change (a line diff for rewritten files) and its notes, and approves or rejects
  it with feedback. Local only, no outside resources, the key kept out of the
  server's view. A person deciding a change that rewrites a file is shown what it
  replaces, and notes an agent set on a proposal itself are cleared.
- **`staircase serve`:** one review page for every running session of the
  workspace, each proposal labelled with its project and run, decisions forwarded
  to the session they belong to. Sessions register while they run; only this
  machine's sessions are contacted, and their keys never reach the browser.
- **`staircase seal` and `staircase attach`:** for agents that edit your checkout
  directly (Cursor, IDE assistants). Stage their changes and run `seal` instead
  of `git commit`: each staged file is decided, and your branch moves to one
  certified commit (CAL 2) with your message and the approved files; rejected
  changes stay in your working files. `attach` installs a pre-commit hook that
  refuses plain commits while an agent works (`--off` removes it; your own hook is
  never replaced).
- **Review attention:** the change certificate records how people decided
  (decisions, median time, and large changes approved within seconds); `verify`
  prints it and `report` lists certified commits with such quick approvals, so
  rubber-stamping becomes visible.
- **Teams:** `staircase governance use <repository>` installs a team's
  `policy.json`, `allowed_signers` and members' keys (`keys/*.pub`) from a
  governance repository, pinned to an exact commit and checked first;
  `governance status` shows when the source or the workspace changed since.
  `verify` and `report` then accept certificates from every member, and `verify`
  counts the team's reviewers for CAL 4.
- **`staircase report [repository...]`:** how agent-written commits were
  governed across repositories: commits by people and with agents, valid
  certificates by level, agents, and every agent commit with a missing or invalid
  certificate or a failed check. `--json` for dashboards.
- **The change certificate's specification** (`docs/spec/certificate-v1.md`) with
  11 conformance test vectors, checked by stAirCase's tests and, in CI, by an
  independent verifier written from the specification alone. How stAirCase
  relates to SLSA's source track, the OWASP Top 10 for LLM applications and NIST's
  SSDF (`docs/standards.md`), a compatibility promise for the six interfaces other
  tools build on (`docs/compatibility.md`) and a draft in-toto predicate proposal.

### Changed

- README, SECURITY.md and the safety boundary now say precisely what runs where:
  edits and reads only through stAirCase's tools, commands and checks in an OS
  sandbox, the agent program itself as your user. SECURITY.md still said approved
  commands run without a sandbox, which stopped being true in 0.4.0.

## [0.4.0] - 2026-09-29

Scale your attention, not your risk. Approve a task once instead of every step,
let two reviewer models that must agree take the routine changes, and keep
sessions working until their checks pass. Commands now run in an OS sandbox on
macOS and Linux, cloud agents' pull requests can be reviewed file by file, and a
second person can sign a change (CAL 4).

### Added

- **Approve the task, not every step.** Sessions show the task, scope, model and
  budget and start only when you agree (`--yes` without a terminal; recorded as
  `task_agreed`). With `--approve-in-scope` (and `--allow`) on `claude`, `codex`
  and `review`, changes inside the agreed scope are approved as part of the task.
  One in five, anything outside the scope, flagged or sensitive changes and
  commands still come to you, and you approve the whole change once before it is
  committed.
- **Checks and a definition of done.** `--check "<command>"` on `run`, `claude`,
  `codex` and `review` runs a command, such as your tests, on a clean checkout of
  the commit the run made, in the sandbox, and records its exit code and output
  digest in the change certificate; `staircase verify` fails a commit whose check
  failed. In Claude Code and Codex sessions the agent cannot end while a check
  fails: its Stop hook runs the checks on a copy of the approved changes and sends
  the failures back (up to 3 times).
- **Reviewer models that must agree.** `--validator` can be repeated, on `run`,
  `claude`, `codex` and `review`. A change is decided only when every model
  agrees; when they disagree, you decide and see each model's reason.
- **Decision models as signals.** `--signal <model>` (for example
  `typesafe-ai/jev`) asks an evaluation model about every change approved without
  you. It can only send the change to you (risky, off the stories, or no answer),
  never approve or reject it. `make eval-jev` measures a model on 24 labelled
  changes.
- **Guards.** A change that adds hidden Unicode (Trojan Source), changes
  dependencies or writes what looks like a secret goes to a person even when a
  rule or the validator would approve it (shown as `CHECK:`, recorded as `guard`).
- **An OS sandbox for commands.** Approved shell commands of built-in agents and
  checks run in macOS `sandbox-exec`, or on Linux in bubblewrap or Landlock: they
  write only in the worktree and their own temporary folder, have no network, and
  cannot read the stAirCase workspace or common credential folders.
  `staircase run --sandbox auto|required|off`. A run whose commands all ran
  sandboxed keeps CAL 3.
- **Claude Code's own sandbox.** `staircase claude` turns it on strictly: it must
  be available, commands cannot retry outside it, have no network and cannot read
  the workspace or credential folders. Approved commands then count as sandboxed,
  and the session can reach CAL 3.
- **`staircase review <branch | commit> --by "<who>"`** brings changes made
  elsewhere (a cloud agent's pull request) into a worktree of the current branch
  one file at a time, and commits exactly the approved files with a certificate
  naming who made them (CAL 2).
- **Two-person review (CAL 4).** `staircase sign <commit>` adds a reviewer's SSH
  signature to the change certificate; `staircase verify --allowed-signers <file>`
  counts it when the reviewer is trusted and did not request the run
  (`requestedBy` in the certificate).
- **Company-wide governance.** `staircase hook-template claude-code|codex` prints
  managed settings that make every session on a company's machines go through
  stAirCase; `staircase hook <agent> --require` blocks tool calls outside a
  governed session. Each tool call is decided once, even with two hooks.
- `staircase policy test <file>` replays past runs' proposals against a policy
  file and shows what it would decide differently.
- The run is an explicit state machine; each run's evidence ends with `run_path`.
- `make check` fails on hidden Unicode and replacement characters in tracked
  files.

### Changed

- Files an approved shell command changes are decided after it ran (kept or
  reverted) instead of failing the run. The `--tamper` demo shows this: you
  approve the command, reject its change, and only the bytes you approved are
  committed.
- A run drops to CAL 2 only when an approved command ran without a sandbox or
  changes were reviewed after an agent made them.

### Fixed

- Codex from the ChatGPT app for macOS is found again after the app moved it
  (`Resources/codex-cli/bin/codex`, Codex 0.158).

### Security

- Sandboxed commands and checks cannot read the stAirCase workspace (signing key,
  encrypted secrets and their key) or common credential locations (`~/.ssh`,
  `~/.aws`, `~/.gnupg`, `~/.config/gh`, `~/.config/git`, `~/.netrc`, `~/.npmrc`,
  keychains and others).
- On Linux, when bubblewrap is missing or not allowed to run (Ubuntu 24.04
  restricts user namespaces), commands run under Landlock with a seccomp filter
  that refuses sockets instead of without a sandbox. CI fails if the Landlock
  tests are skipped.

## [0.3.0] - 2026-09-28

Put `staircase` in front of your agent. `staircase claude "task"` and
`staircase codex "task"` govern Claude Code and OpenAI's Codex in any git
repository with no setup, and every commit a run makes now carries a signed,
verifiable change certificate that a pull request can be required to have.

### Added

- Lessons from rejections: when a case is compiled, the plan lists the changes a
  person rejected in the project's earlier runs, with their reasons (the ten
  most recent), and every agent reads them in its brief.
- `staircase codex "task"`: run OpenAI's Codex CLI under governance, with no
  setup. Its edits (`apply_patch`) are decided before they are applied; its
  shell commands run in Codex's sandbox (no network, writes only in the
  worktree) and the files they change are reviewed afterwards: kept if
  approved, reverted if not. A Codex run that never calls stAirCase's hooks
  fails. `run --agent codex`, `compile --agent codex`, `make smoke-codex`.
- Review after the fact: changes an agent's command made in the worktree come
  to a decision as one proposal (approved, they are kept; rejected, they are
  reverted). A run that keeps such changes reaches CAL 2.
- `--model` for `staircase claude`, `staircase codex` and `run` with an agent
  harness.
- **Change certificates.** Every run that commits signs a change certificate
  about exactly that commit (in-toto statement in a DSSE envelope, workspace
  Ed25519 key, digests only) and attaches it as a git note
  (`refs/notes/staircase`); the commit message names the agents that helped
  (`Assisted-by:`) and the audit chain's head (`Staircase-Chain:`). The
  certificate states the change assurance level reached (CAL 3, or CAL 2 when
  shell commands were approved).
- `staircase verify <commit>`: checks a commit's certificate against the
  trusted key, that it is about exactly this commit, and `--min-cal`.
- `staircase verify main..HEAD`: checks every commit in a range that names an
  agent (`Assisted-by:`), or every commit with `--all`.
- A GitHub Action, `uses: b070nd/stAirCase@<version>`, that requires valid
  change certificates on a pull request's agent commits. It installs the
  staircase release after checking its build attestation.
- `staircase audit anchor <run-id>`: anchors a run's change certificate in a
  Rekor transparency log. Only digests are sent, never code or reasoning;
  `staircase verify --check-anchor` confirms it.
- `staircase claude "task"`: run Claude Code on a task in the current git
  repository with no setup. The workspace, a project for the repository and a
  case for the task are created when missing; every change still comes to you,
  and exactly the approved changes land on a run branch.
- `staircase compile --agent claude-code`: a plan can name the agent harness
  that runs it. Such a case needs no topology and no provider key; the harness
  is recorded in the run's first audit event.
- `staircase hook <agent>`: one command that passes an agent's hook calls to the
  run that governs it, and blocks the call on every failure (exit code 2).
- `ROADMAP.md` and the first architecture decisions in `docs/adr/`.

### Changed

- Claude Code runs (`--agent claude-code`) call `staircase hook claude-code
  --governed` instead of `curl`. The run's token is no longer on a command line
  (it is in a file only you can read), and curl is no longer needed.

- Release checksums are signed into one Sigstore bundle,
  `checksums.txt.sigstore.json` (the format cosign 3 writes), instead of
  separate `.sig` and `.pem` files; verify with `cosign verify-blob --bundle`.
- The documentation is rewritten in plain English, with an index
  (`docs/README.md`), a page per task, troubleshooting, and a CLI reference
  generated from the program (`docs/cli.md`). `docs/plugin-gates.md` is now
  `docs/gates.md`, and `docs/project-use.md` is now `docs/safety.md`.
- `audit export --anchor` now says in its help what leaves the machine: the
  whole record is sent to the Rekor service, whose public log keeps its SHA-256,
  the signature and your public key.

### Fixed

- `audit verify --check-anchor` could not succeed against the real Rekor log:
  it expected the log to return the record, but the log keeps only the
  record's hash. It now checks the logged hash and signature.
- An edit to a Windows-style (CRLF) file no longer rewrites every line ending
  in the file: it changes only its own lines and keeps the file's line
  endings, so what you approve is what you see.
- A repository's own Claude Code settings (its `.claude` hooks, and user
  settings with their hooks, plugins and MCP servers) no longer load in a
  governed Claude Code run.

- The `runtime.plan_pinned` gate skips a case that does not exist, instead of
  failing with a database error.

## [0.2.0] - 2026-09-25

The Go rewrite (versioned 0.x, starting over from the Bash 1.x line). Highlights
since the first Go commits:

### Changed (breaking)

- **No Python.** Agents run in-process in the `staircase` binary: a supervisor and
  its agents call models over HTTPS (Anthropic, OpenAI, Gemini, xAI, or any model
  through an OpenAI-compatible LLM gateway). The generated LangGraph script, the
  venv and its dependency lock, and the IPC socket are gone. `init --skip-venv`,
  `init --offline-wheels` and `doctor --fix-venv` are accepted but do nothing.
- **`compile` writes a plan** (`tmp/plan_case<N>.json` + sha256), not a script;
  `run` refuses a plan that was edited, is for another case or is of another
  version. Unknown extra tools and models without a provider fail at compile.
- **Routing:** the supervisor starts; each agent ends its turn with
  `ROUTE: <next>` (the supervisor may say `END`); runs stop after 25 agent steps
  or 100 model calls. Documented topologies now finish instead of looping.
- **Every run gets its own git worktree**; the developer's checkout is never
  touched. `run --auto-stash` and `--force` do nothing.
- **Approvals are bound to bytes the orchestrator derives**, decisions are
  audited before the agent sees them, and the commit is built from the approved
  bytes; unapproved changes, index edits or agent commits fail the run.
- **`policy.json` fails closed**: unparsable or unknown fields stop the run
  (they used to be ignored with a warning). `limits.max_run_duration`, never
  enforced before, is now `max_run_secs` (the name blueprints use) and is
  enforced; the old name is refused with that hint. A reject rule scoped by
  `agent_names` no longer counts as a blanket deny.
- **Unpriced models are not free**: they count at the highest known rate
  against the budget cap; `provider/model` names are priced by their model.
- **Webhook approvals** must echo the request's fresh `yield_id` and
  `request_sha256` when a secret is set.

- **Releases** ship Linux and macOS (amd64, arm64) and an experimental,
  untested Windows amd64 build (`run_shell` and `--agent claude-code` need a
  POSIX shell). They are published from `b070nd/stAirCase` (the cosign identity
  in QUICKSTART's verification steps is corrected to match).

### Security

- Built with Go 1.26.8 and updated dependencies with published fixes that
  stAirCase's code reaches: go-git 5.19.2 (path traversal via reference
  names, worktree symlinks, crafted repositories), golang.org/x/crypto 0.56.0
  (SSH client), google.golang.org/grpc 1.83.1 (HTTP/2 transport, used by the
  trace exporter) and goldmark 1.7.17 (terminal approval rendering). Go 1.26.5+
  also fixes an os.Root symlink escape (GO-2026-4970). govulncheck reports no
  reachable vulnerabilities.

### Added

- `--record-llm` / `--replay-llm` record every model exchange of a run and replay
  it offline, failing loudly on any request not in the recording.
- `story accept` records acceptance on the audit chain and completes the case.
- A stuck agent no longer hangs a run (`agent_unresponsive` is audited).
- **Blueprints** (`docs/blueprints.md`): `blueprint import` snapshots a
  blueprint directory by content hash, `project bind` creates its topology and
  cases in a project, and the `runtime.plan_pinned` gate blocks runs of bound
  cases whose plan drifted from the blueprint. Plans (version 2) carry the
  case's stories and blueprint; recompile plans from earlier builds.
- `run_bound` records the plan's sha256 and blueprint, and runs record the
  topology version they executed rather than the latest one.
- **Drift supervision** (`docs/drift.md`): `story scope` (or a blueprint's
  scope) limits the paths a run may change; out-of-scope proposals, file limits
  and checkpoints go to a human with a `DRIFT:` reason; too many scope
  violations or `max_run_secs` halt the run (`drift_halt`) until
  `run --ack-drift`; every run ends with a `drift_report`. Agents are told
  their stories and scope.
- `run --validator <model>`: an automated reviewer decides in-scope file edits
  the policy leaves open, from the derived change and the stories only;
  sensitive paths, rejection streaks, unreadable verdicts and every 5th
  approval go to a human, and a human approves the run's final change once.
- `secret set` on an existing key replaces its value (e.g. a rotated API key)
  and counts its version; it used to fail with a UNIQUE constraint error.
- `make demo` is the offline end-to-end acceptance run: blueprint imported
  from its own repository → bind → run with create, edit and delete approved →
  run branch holds exactly those changes → developer checkout byte-identical →
  stories accepted → audit verified; plus `--tamper` and `--drift` paths.
  `make smoke` / `make smoke-claude` run the same flow against a real model
  (record, then replay offline) and Claude Code; they skip without credentials.
- **Install and verify**: `go install github.com/b070nd/stAirCase/src/cmd/staircase@latest`
  (the module path is now the repository path), Homebrew with bash/zsh/fish
  completions, and release archives that are byte-reproducible from their tag,
  with signed checksums, SBOMs and GitHub build-provenance attestations
  (`gh attestation verify`). `staircase version` reports the module version for
  `go install` builds.
- Project: code of conduct, issue forms, pull request template, Dependabot,
  CodeQL and OpenSSF Scorecard; release notes come from this changelog.
- `run --agent claude-code` (experimental): Claude Code does the work, and
  hooks route every tool call through the same approvals - edits and shell
  commands are proposals, reads stay in the worktree, other tools are denied.

## [1.2.0] - 2026-03-12

### Core model shift

v1.2 drops the per-project `.staircase/` tree entirely. All PRD (context) files now live flat in the workspace under `.staircase/prd/`. Project directories on the filesystem are either symlinks to real source or plain stub directories - never contain `.staircase/` metadata. Multiple cases per project coexist permanently; there is no "active context" concept.

### Added

- **`staircase migrate`** - migrates a v1.1 workspace to v1.2 in-place. Reads `vendor/project/.staircase/tasks/*/context.json` and `active/context.json`, writes them as flat PRD files under `.staircase/prd/`. Reads runner from per-project `config.json` and stores it in the manifest. Converts `activeCase` → `lastCase`. If a project has a source path set, removes the stub directory and creates a symlink. Removes per-project `.staircase/` directories. Bumps manifest and config versions to `"1.2"`.
- **`staircase case delete <v[/p]> <case-id>`** - removes the PRD file for a case. Clears `lastCase` in the manifest if it pointed to the deleted case. Supports `--dry-run`.
- **Vendor-scope cases** - `case new`, `case info`, `case delete`, and `run` all accept a bare vendor name (no `/project` segment). Vendor-level PRDs are stored as `<vendor>.<case-id>.json` and carry `"scope": "vendor"`.
- **`project add <v/p> [source-path]`** - optional second argument: if given, the project directory is created as a symlink `ln -s <canon> workspace/v/p` rather than a stub directory.
- **`.staircase/prd/` directory** - created by `init` and `doctor --fix`.
- **`_prd_dir()`, `_prd_ns()`, `_prd_file()`, `_last_case()`, `_parse_vp_or_v()` helpers** - new internal functions for PRD path construction and vendor-or-project parsing.
- **`_build_prd()` helper** - replaces `_build_context()`. Produces the new PRD schema with `id`, `vendor`, `project` (null for vendor-scope), `scope`, `components`, `created`, `modified`, `stories`, `files`, `gitDiff`.

### Changed

- **Manifest schema** - version bumped to `"1.2"`. `activeCase` replaced by `lastCase` (display-only, not required for running). `runner` field added to project entries (null = inherit workspace default). `source` key absent when not linked.
- **PRD file schema** - replaces `context.json` / `caseId` schema. New fields: `id` (namespaced: `vendor.project.case-id`), `scope` (`"project"` or `"vendor"`), `modified`.
- **`_runner()`** - reads runner from manifest project entry (`.vendors[$v].projects[$p].runner`) instead of per-project `config.json`.
- **`cmd_run`** - case-id is now a required positional argument (`run <v/p|v> <case-id>`). No longer reads `active/context.json`; resolves PRD file from `_prd_file`. Updates `lastCase` in manifest after successful launch. `--config` merges into a temp copy of the PRD (original is never mutated). Accepts bare vendor for vendor-scope runs.
- **`cmd_case_new`** - accepts `<v/p|v>` (vendor-or-project). Writes to `.staircase/prd/` instead of per-project directory. Updates `lastCase` for project-scope cases.
- **`cmd_case_list`** - optional `[v[/p]]` argument. Without argument lists all PRD files. With vendor lists `<vendor>.*` files. With `v/p` lists `<vendor>.<project>.*` files. Marks `lastCase` with `*`.
- **`cmd_case_info`** - requires both `v[/p]` and `case-id` arguments. Reads from PRD file.
- **`cmd_project_add`** - no longer creates per-project `.staircase/` directories or `config.json`. Manifest entry uses `lastCase` instead of `activeCase`.
- **`cmd_project_remove`** - removes all `$v.$p.*.json` PRD files, removes the symlink (`rm -f`) or stub directory (`rm -rf`).
- **`cmd_project_link`** - now also manages filesystem symlink: empty stub dir → replaced with symlink; existing symlink → updated atomically; dir with contents → manifest only + warning.
- **`cmd_project_unlink`** - no longer writes per-project `config.json`. If `$ws/$v/$p` is a symlink, replaces it with a stub directory.
- **`cmd_project_info`** - shows `last case` instead of `active case`. Shows cases count from PRD files. Removed per-project `config.json` runner lookup.
- **`cmd_project_list`** - shows `last:` instead of `case:`.
- **`cmd_component_add`** - no longer writes per-project `config.json`. Skips `mkdir` when project dir is a symlink (component dirs live in the real source).
- **`cmd_component_remove`** - no longer writes per-project `config.json`.
- **`cmd_ls`** - detects symlinks with `-L`, shows `→ target` via `readlink`. Shows cases count per project from PRD dir. Shows vendor-level case count. Shows `last case` instead of `case`.
- **`cmd_status`** - columns updated to `VENDOR  PROJECT  LAST CASE  CASES  SRC  MODIFIED`. CASES = count of PRD files for the project. MODIFIED = mtime of lastCase PRD file.
- **`cmd_doctor`** - checks `.staircase/prd/` exists. Validates each PRD file (valid JSON, id matches filename). Checks symlink targets exist. Warns if per-project `.staircase/` dirs are found (suggests `migrate`). Removed checks for `active/` and `tasks/` dirs. `--fix` creates `prd/` dir and vendor dirs; does NOT remove stale symlinks.
- **`cmd_export`** - reads all `$v.$p.*` PRD files. Output schema: `{vendor, project, exported_at, manifest, cases: {<case-id>: <prd-content>}}`. Tar archives only the filtered PRD files.
- **`staircase --version`** now reports `1.2.0`.

### Removed

- **`staircase case switch`** - removed. Cases are permanent; switch by specifying `case-id` in `run`.
- **Per-project `config.json`** - no longer created or read. Runner moves to manifest project entry.
- **`active/context.json` and `tasks/*/context.json`** - replaced by flat PRD files in `.staircase/prd/`.
- **`_build_context()` helper** - replaced by `_build_prd()`.

### Migration

Workspaces from v1.1 can be migrated with `staircase migrate`. The command is idempotent and non-destructive until it removes the per-project `.staircase/` directories at the end.

---

## [1.1.0] - 2026-03-11

### Added

- **`staircase project link <v/p> <path>`** - associates an external source directory with a project. The path is resolved to its canonical absolute form (`pwd -P`) and stored in both the workspace manifest and the project's `.staircase/config.json`. Supports `--dry-run`.
- **`staircase project unlink <v/p>`** - removes the source link from manifest and project config. Idempotent: safe to call on a project that was never linked. Supports `--dry-run`.
- **`_source_path()` helper** - internal function that reads `.vendors[$v].projects[$p].source` from the manifest, returning an empty string when absent. Used by `run`, `ls`, `status`, `project info`, and `doctor`.
- **`cmd_run` source-aware execution** - when a project has a linked source, `run` `cd`s into the source directory instead of the stub project directory and passes the absolute context path to the runner (`--prd /abs/path/to/.staircase/active/context.json`). Unlinked projects behave identically to v1.0.0.
- **`project info` source display** - shows `source: /path/...` or `(not linked)` as the first field in the project info block.
- **`ls` link indicator** - appends `[→ linked]` (cyan) next to the component count for linked projects.
- **`status` SRC column** - new `SRC` column with `✓` for linked projects and `-` for unlinked.
- **`doctor` stale-source check** - warns when a project's source path is configured but the directory no longer exists. Does not auto-fix (the path may be on an unmounted volume); resolve with `project unlink` or by remounting.

### Changed

- **Manifest schema** - `.vendors[$v].projects[$p]` gains an optional `source` field (string, absolute path). No migration needed; absent field is treated as unlinked.
- **Project config schema** - `vendor/project/.staircase/config.json` gains an optional `source` field, mirroring the manifest.
- **`staircase --version`** now reports `1.1.0`.

---

## [1.0.0] - 2026-03-09

Initial release.

### Workspace

- **`staircase init [--name <n>]`** - scaffolds `.staircase/config.json`, `.staircase/manifest.json`, and `.staircase/tmp/`. Idempotent.
- **`staircase config [<key>] [<value>]`** - get/set workspace config values. `--list` shows all resolved values with their source (config, env, or default).

### Structure

- **`staircase vendor add|remove|list`** - manage vendor namespaces. `remove` requires all projects to be removed first.
- **`staircase project add|remove|list|info`** - manage projects (`vendor/project`). `add` auto-creates the vendor if it doesn't exist. `remove` leaves the directory in place. `info` shows active case, runner, and components.
- **`staircase component add|remove|list`** - manage component subdirectories within a project. `add` accepts multiple component names. `list` marks missing directories with `!`.

### Cases

- **`staircase case new <v/p> <case-id>`** - creates a task directory, writes `active/context.json`, and sets the case as active in the manifest. Case IDs with special characters (quotes, slashes) are handled safely via `jq -n`.
- **`staircase case switch <v/p> <case-id>`** - saves current `active/context.json` to the previous case's directory, loads the target context, and updates the manifest. All writes are atomic (`mktemp` + `mv`).
- **`staircase case list <v/p>`** - lists all cases with `*` marking the active one and last-modified timestamps.
- **`staircase case info <v/p> [case-id]`** - prints the active (or named) context as formatted JSON.

### Agent Runner

- **`staircase run <v/p> [--runner <r>] [--config '{}']`** - resolves the runner through the config cascade, changes into the project directory, and launches `<runner> --prd .staircase/active/context.json`. Optional `--config` JSON is merged into the context before launch.
- **Runner resolution order** (highest wins): `--runner` flag → `STAIRCASE_RUNNER` env → project config → workspace config → `ralph-tui`.
- Post-run hook: fires `hooks.d/99-post-run.sh` if present and executable.

### Inspection

- **`staircase ls`** - color-coded tree view of vendors, projects, active cases, and component counts.
- **`staircase status [--json]`** - tabular view with vendor, project, active case, component count, and last-modified timestamp. `--json` outputs the raw manifest.

### Health

- **`staircase doctor [--fix]`** - checks for missing/invalid config and manifest, missing vendor directories, missing project `.staircase/` directories, active cases without `context.json`, missing component directories, and unwritable tmp. `--fix` repairs everything it can.
- **`staircase export <v/p> [--format json|tar]`** - JSON export includes manifest entry, active context, and all saved case contexts. `--format tar` creates a `.tar.gz` archive.

### Git Hooks

- **`staircase hooks install <v/p>`** - creates `hooks.d/` stubs and installs `pre-commit` (formatter) and `post-merge` (auto-doctor) hooks into the project root and all component repos that have `.git`. Idempotent via guard comments.

### Flags & Environment

- **`--dry-run`** - every command supports dry-run mode, printing intended actions without touching disk.
- **`--no-color`** / **`NO_COLOR`** - disables ANSI output for CI environments.
- **`STAIRCASE_DEBUG`** - enables `set -x` tracing.
- **`STAIRCASE_DIR`**, **`STAIRCASE_TMP_DIR`**, **`STAIRCASE_HOOKS_DIR`**, **`STAIRCASE_RUNNER`** - override workspace root, tmp directory, hooks directory, and agent runner respectively.

### Technical Notes

- Zero Python dependencies - pure Bash + `jq`.
- Cross-platform: macOS, Linux, WSL, Docker.
- All JSON mutations use atomic `mktemp` + `mv` writes.
- Context JSON built with `jq -n` - special characters in case IDs are always safe.

[1.2.0]: https://github.com/b070nd/staircase/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/b070nd/staircase/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/b070nd/staircase/releases/tag/v1.0.0
