# ADR 0001: The core promise and change assurance levels

- Status: accepted
- Date: 2026-09-28

## Context

stAirCase exists so that a team can accept changes written by AI agents and prove
afterwards what happened. Agents, models and review habits change every few months;
the promise must not.

v0.2.0 gives one strong guarantee: agents work in an isolated worktree, every change is
decided before it happens, and only approved bytes are committed. The ways in that are
planned next give weaker guarantees. Examples: reviewing an edit after Cursor has made
it, or checking a pull request that a cloud agent opened. Without names for these
guarantees, users cannot tell what they got, and a repository cannot require a minimum.

## Decision

**1. The core promise.** A change reaches the target branch only as bytes that stAirCase
derived itself and that a recorded decision covers. stAirCase builds the commit from
those bytes, and anyone can check this afterwards. No feature may weaken this promise
without saying so.

**2. Four change assurance levels (CAL).** Each level includes the ones below it.

| Level | What it guarantees |
|---|---|
| CAL 1 Attributed | The change says which agents and models took part (a certificate and an `Assisted-by:` trailer). Nothing is enforced. |
| CAL 2 Reviewed | Every byte an agent wrote is covered by a recorded decision, and the commit can be rebuilt from the approved proposals. Proposals may have been captured after the agent made them. |
| CAL 3 Gated | Every agent action was decided before it ran, in an isolated worktree. No approved command ran outside a sandbox. stAirCase built the commit. |
| CAL 4 Two-party | CAL 3, and the decisions, or the final review of the whole change, are signed by an identified person who did not start the run. |

**3. Who may decide.**
- A person can always decide.
- A policy rule, a verifier's result or a validator model can also decide, within the
  limits of the level. A model's approvals count only when a person reviews the whole
  change at the end, as the validator works today.
- Fast decision models (signals) may only make a decision stricter until they have been
  measured on local cases.

**4. Honest reporting.** A run records the level it actually reached. A lower level is
never shown as a higher one.

**5. Requirements.** A repository can require a minimum level per branch or path. The
verifier enforces it.

## Update, 2026-09-28

CAL 4 is reachable: `staircase sign` adds a reviewer's SSH signature to the
certificate, and `staircase verify --allowed-signers` counts it when the reviewer is
trusted and is not the git identity the run was made under (`requestedBy` in the
certificate). Signing each decision, not only the whole change, comes later.

## Consequences

- Every way in states the highest level it can reach, both in its documentation and in
  the certificate ([ADR 0002](0002-change-certificate.md)).
- A feature that cannot keep the promise at a level does not ship at that level.
- **Today:** v0.2.0 already enforces what CAL 3 needs for file changes. A run with
  shell commands enabled reaches CAL 3 only once commands run in a sandbox. Claude
  Code runs reach CAL 3 only once its own commands are sandboxed.
- **Update (sandbox):** approved commands of built-in agents now run in an OS
  sandbox (macOS `sandbox-exec`, Linux `bwrap`) and are recorded as `shell_ran`.
  A run stays at CAL 3 when every approved command ran sandboxed; the files such a
  command wrote are decided before they reach the commit.
- CAL 4 resembles two-party review in SLSA's source track (Level 4). The
  documentation may describe the mapping, but it never claims compliance on anyone's
  behalf.
