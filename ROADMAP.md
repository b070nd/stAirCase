# Roadmap

stAirCase is pre-1.0. This page says where it is going and why. The order changes when
users ask for something else, and no dates are promised. Ideas and objections are
welcome in the [issues](https://github.com/b070nd/stAirCase/issues).

## Where we are going

AI agents will write a large share of the changes to code, infrastructure and
configuration. Every team will have to answer one question: **can we prove what the
agent changed, and who allowed it?**

Our goal: **every change an AI agent makes carries a change certificate**. It is a
small, signed statement of:
- which agent and model proposed the change;
- under which task and rules;
- who or what approved each byte, and with which evidence.

From the certificate and the approved proposals, anyone can rebuild the commit and get
the same bytes.

stAirCase does both halves:
- **Gate** - it puts itself in front of any agent and produces the certificate.
- **Verify** - it lets a repository, a CI pipeline or an auditor require the
  certificate.

The certificate uses open standards ([in-toto](https://in-toto.io/),
[DSSE](https://github.com/secure-systems-lab/dsse), SLSA's
[source track](https://slsa.dev/spec/v1.2/)), so other tools can produce and check it
too. Agents come and go; the evidence format should outlast them.

## Principles

- **The core promise never weakens.** Only bytes that stAirCase derived itself, and
  that a recorded decision covers, reach your branch
  ([ADR 0001](docs/adr/0001-core-promise-and-assurance-levels.md)).
- **Fail closed.** When in doubt, nothing is committed.
- **Local-first.** One program, your machine, your git. Services are optional.
- **Use standards; don't invent them.**
- **No new dependency without a real need.**
- **A model never approves alone.** Every guarantee is written down.
- **Explicit models over prompt rules.** What a run may do lives in code and tables
  that can be read and tested, not in instructions to a model.

## Change assurance levels

Not every way of working allows the same guarantee, so each change states the level it
reached:

| Level | What it means |
|---|---|
| **CAL 1** Attributed | We know which agents and models took part. |
| **CAL 2** Reviewed | Every byte an agent wrote was decided, and the commit can be rebuilt from the approved proposals. |
| **CAL 3** Gated | Every agent action was decided *before* it ran, in an isolated copy; commands ran in a sandbox; stAirCase built the commit. |
| **CAL 4** Two-party | Also signed by an identified person who did not start the run. |

A repository can require a level per branch or path, for example CAL 4 for `infra/`.
Details: [ADR 0001](docs/adr/0001-core-promise-and-assurance-levels.md).

## How it is built

```
 capture (thin, per agent)  →   decide (in a fixed order)   →   prove and verify (standards)
 hooks of Claude Code, Codex,    refuse · scope · guards ·        change certificate ·
 Gemini CLI, OpenCode, Cursor;   verifiers · signals · rules ·    signed approvals ·
 built-in agents; review-after   validator · people               verify command and CI check
                     └──── trusted core: exact bytes, audit chain, commit ────┘
```

Six contracts stay stable and versioned while everything around them may change:
1. the [hook bridge](docs/adr/0003-hook-bridge.md);
2. the proposal and decision records;
3. the [change certificate](docs/adr/0002-change-certificate.md);
4. the interface to fast decision models;
5. verifier results;
6. the policy file.

## Who it is for

| You are | You would use | Level |
|---|---|---|
| a developer | `staircase claude "task"` in any repository | CAL 3 |
| an open-source maintainer | a CI check that requires certificates on AI-assisted pull requests | checks CAL 2+ |
| a team | shared rules and signing keys in a governance repository, plus the CI check | CAL 2–4 |
| a company | the vendors' managed hook settings, so every agent session is governed | CAL 3–4 |
| a user of cloud agents | `staircase review <pull request>` for changes made elsewhere | CAL 2 |
| an operations team | the same gate on infrastructure and deployment repositories | CAL 3–4 |

## The plan

### Phase 0 - Foundations (in progress)

- This roadmap and the first architecture decisions ([docs/adr](docs/adr/)).
- **One hook bridge for every agent:** `staircase hook <agent>`. There is no longer a
  secret on the command line and no need for curl, and one command works for every
  agent ([ADR 0003](docs/adr/0003-hook-bridge.md)).
- Fuzz tests for the trusted core.

### Phase 1 - v0.3: put `staircase` in front of your agent

- **Zero setup:** `staircase claude "add a health endpoint"` in any git repository. No
  vendor, topology or case to create first. You get a branch with exactly the approved
  changes.
- **Claude Code** out of experimental, and **Codex CLI** support.
- **Change certificate v1:**
  - an `Assisted-by:` trailer on the commit;
  - `staircase verify`;
  - a GitHub Action that requires certificates;
  - public anchoring of digests only, never content.
- **Lessons from your rejections:** when you reject a change, your reason is kept for
  the project (in the workspace, not the repository) and the next session's agent
  reads it first, so the same mistake is not proposed twice.

### Phase 2 - v0.4: any agent, anywhere

- **Gemini CLI** and **OpenCode** support. *Gemini CLI has been run for real (0.46.0: a governed run, a held
  approval, a rejection and its retry, a verify and a rebuild in a fresh clone, a resume of its own session, and the control for a change
  written around the hooks); OpenCode has been run for real once (1.18.35: a governed `write`, and a rejection with its retry, verified and
  rebuilt in a fresh clone) and stays experimental (`staircase gemini`, `staircase opencode`; [compatibility](docs/compatibility.md)).*
- **Review-after** capture, used for **Cursor** (CAL 2). *Done: `staircase seal`
  works with any agent that edits your checkout.*
- `staircase review <pull request>` for cloud agents.
- Templates for managed (company-wide) hook settings.
- **Signed approvals** with your SSH key: *who* approved becomes provable. *Done for
  each decision: `--sign-approvals`, with the approval API taking signatures too.*
- **Agree on the task before it starts:** a session shows its scope (the paths it may
  change), the kinds of change and the budget, and you approve them before the agent
  runs. This is the first step of "approve the task, not every step" (phase 3).
- An evaluation of fast decision models, such as Jev or the open-source Laya, on our own
  test cases. The model never decides freely: it only picks among the next steps the
  run allows, as a typed answer, and a wrong pick can only send a change to a person.
  The numbers get published. *Built: `--signal typesafe-ai/jev` (or a local Laya with
  `--signal-url`) and `make eval-jev` with 24 labelled changes. Laya was measured on 2026-10-07 and is
  weak at the fixed threshold; Jev through the gateway is not measured, by the owner's decision
  ([results](docs/evaluation/README.md)).*

### Phase 3 - v0.5: scale human attention

- **The run as an explicit state machine:** every step of a run (checks, worktree,
  deciding, final review, commit) and every allowed move between them in one table,
  instead of spread through the code. Each run's path becomes testable and visible in
  its evidence, and it is the ground for runs that can pause and resume (phase 4).
- **Approve the task, not every step:** building on the up-front agreement from phase
  2, changes inside the approved scope can be approved on evidence; anything outside
  comes to you. Some approvals are sampled for your review. *Done:
  `--approve-in-scope`, with a checkpoint every fifth change and a final review;
  review attention in the certificate makes approvals given without reading
  visible. Evidence-based approval: `--approve-on-evidence` approves in-scope changes
  only when the checks pass on the state each change produces and the reviewer models
  agree, replacing the sampled checkpoint.*
- **Gates as evidence:** each checkpoint must pass its gates - tests run in a sandbox
  on the exact approved code, two independent reviewer models that must agree, and a
  person - and every gate's result goes into the change certificate. *Done:
  `--check` runs tests in the sandbox on the exact commit and records the result in
  the certificate; repeated `--validator` models must agree.*
- **A definition of done:** a session cannot end until its required gates pass. The
  agent's own "stop" hook is refused with the failures, so it keeps working inside
  governance until the result is right. ("An attempt is allowed to be wrong; it is not
  allowed to ship until it isn't.") *Done for `--check` in Claude Code and Codex
  sessions.*
- **Guards:**
  - new or changed dependencies always come to you;
  - hidden Unicode ("Trojan Source") and secrets written into code are flagged.
- **Decision models as signals:** they pick among the transitions the run's state
  machine allows, and may only make decisions stricter until they have proven
  themselves on your own history.
- `staircase policy test`: see what a new rule would have decided on past runs.
- **A deeper sandbox:** approved commands and checks already run without network,
  write only in their worktree, and cannot read the workspace or common credential
  folders, on macOS and on Linux (bubblewrap, or Landlock where bubblewrap is
  missing or not allowed; tested in CI). Next:
  - the agents' own commands sandboxed too: Claude Code's built-in sandbox turned
    on in `staircase claude` sessions, so they can reach CAL 3. *Done.*

### Phase 4 - v0.6 to v0.9: teams without servers

- A **local background service**, so long runs survive restarts (they resume from the
  state machine's recorded state) and you can approve from a browser. Approvals there
  are signed with your SSH key (a passkey was considered and dropped: one identity
  system is enough). *Started: every run with `--approval-port` serves a review page
  (line diffs, notes, approve or reject) and `staircase serve` shows all running
  sessions on one page; decisions are signed with `--sign-approvals`. An interrupted
  run no longer loses what it had approved: `staircase recover` commits exactly that.
  Continuing the agent itself after a restart is still open; the design is written and accepted
  ([ADR 0005](docs/adr/0005-durable-continuation.md)), nothing of it is built.*
- **Attach mode:** govern the agent in your own checkout, sealed when you commit.
  *Done: `staircase attach` and `staircase seal` (CAL 2), which also covers Cursor.*
- **Git as the control plane:**
  - rules, blueprints and trusted keys come from a governance repository; *done:
    `staircase governance`;*
  - evidence is kept under git refs. *Started: certificates and ledgers are git notes
    (`refs/notes/staircase*`), and `verify --rebuild` checks them in CI;*
  - a report spans many repositories. *Done: `staircase report`.*

### Phase 5 - v1.0: a standard others can implement

- A written specification of the certificate and of how a commit is rebuilt, with test
  vectors. *Done: the certificate and the rebuild are specified
  ([spec](docs/spec/certificate-v1.md)), with 13 + 25 vectors and independent
  implementations; `staircase rebuild` reproduces a commit from its ledger.*
- The certificate format proposed to in-toto, and its mapping to SLSA's source track
  published, together with a mapping to the OWASP Top 10 for LLM applications and
  NIST's secure development profile for generative AI (SP 800-218A). *Mappings
  published ([standards](docs/standards.md)); the in-toto proposal is
  [drafted](docs/spec/in-toto-predicate.md); `verify --vsa-out` writes a signed
  [SLSA verification summary](docs/spec/vsa-v1.md) per verified commit.*
- A compatibility promise for the six contracts. *Written
  ([compatibility](docs/compatibility.md)); it becomes semantic versioning at 1.0.*

## Waiting for demand

These are built only when their trigger happens:

| Item | Trigger |
|---|---|
| a hosted service (only as a reader of git evidence) | three or more teams ask for it |
| plugins for custom deciders (WebAssembly) | others want to ship their own deciders |
| a policy expression language | the rules cannot express a real policy |
| connectors for SIEM or compliance tools | the existing OpenTelemetry and JSON output is not enough |
| ACP or MCP integration | an agent makes its permission requests enforceable |
| sharing blueprints | a community forms around them |
| a container sandbox (`--sandbox docker`: own image, memory and process limits) | a user needs another OS image or resource limits for commands |

## Not planned

- our own IDE or agent framework;
- a software-as-a-service product first;
- new protocols where a standard exists.

## How we decide what comes next

After every release we look at:
- issues and pull requests from people other than the maintainer;
- release downloads;
- repositories that use the CI check;
- the first teams that adopt it.

Then we reorder this page. The next review is on **2026-11-15**.
