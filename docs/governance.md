# Working as a team: the governance repository

A team keeps its rules and trusted keys in one git repository, reviewed like any
other code. Each developer's workspace takes them from there, pinned to an exact
commit.

## The repository

```
team-governance/
  policy.json       the rules and limits every run uses (required)
  allowed_signers   the people trusted for two-person review (optional)
  keys/
    alice.pub       each member's workspace signing key (optional)
    bob.pub
  blueprints/       blueprints the team shares (optional)
    hello/
      blueprint.yaml
      prompts/coder.md
```

- `policy.json` is the same file as a workspace's own ([approvals](approvals.md)).
  Test a change with `staircase policy test` before you merge it.
- `allowed_signers` is git's format: `<email> <ssh public key>` per line.
- Each `blueprints/<name>/` is a [blueprint](blueprints.md) folder, exactly as
  `staircase blueprint import` reads one. They are checked the same way (unknown fields,
  files outside the folder and the like are refused), and a symlink or submodule inside
  one is refused too, since it would not be the bytes the commit names.
- Each `keys/*.pub` is a member's `~/.staircase-workspace/.signing.pub`, the raw
  32-byte key. Add a member by adding theirs.

## Using it

```bash
staircase governance use git@github.com:acme/team-governance.git
```

This fetches the repository, checks the policy with the same strict rules a run
uses, and installs the files in your workspace at the branch's current commit (use
`--ref` for another branch, a tag or a commit). The policy is signed with your
workspace key, so runs detect a later edit. If anything is invalid, nothing
changes.

Every blueprint in `blueprints/` is imported as a snapshot, with the pinned commit as
its source, so everyone who uses the same commit has the same blueprint under the same
hash (`staircase blueprint list`). `governance use` prints how to bind one:
`staircase project bind <project-id> <hash>`. A blueprint that is invalid stops the whole
install, policy and keys included. A blueprint the team removes later stays in your
workspace: runs bound to it need its snapshot.

From then on:

- runs use the team's rules and limits;
- `staircase verify` and `staircase report` accept certificates signed by any
  member's key, so a teammate's commits pass your CI check too;
- `staircase verify` counts the team's reviewers for [CAL 4](audit.md#two-person-review-cal-4)
  without `--allowed-signers`.

## Staying in step

```bash
staircase governance status
```

shows the pinned commit, warns when the repository has moved on, and when a file in
your workspace was changed since it was installed. Changes never arrive on their
own: review what changed in the repository, then run `governance use` again.

## What to protect

Whoever can change the governance repository decides the team's rules and whom its
members trust. Protect its main branch like production code: required reviews, and
signed commits if you use them.

## What this is not

This is a way to share rules and keys through git. It is not an organization's
identity system, and it does not claim to be:

- **No single sign-on, roles or authenticated initiators.** Who is trusted is whoever
  holds a listed key. A run records the git email of the checkout it ran in, which
  anyone can set; nothing ties a run to a signed-in person.
- **Rollout and revocation are not atomic.** Each workspace takes a change when its
  owner runs `governance use`; until then it keeps the old rules and keys. To revoke
  a key, remove it from `keys/` and `allowed_signers`, merge, and have every member
  run `governance use`; certificates are checked against a member's *current* keys, so
  a revoked key stops verifying only where the new bundle has been installed. A CI
  check reads the bundle from its base branch, so it changes the moment the merge
  lands.
- **Durability.** Decisions are committed to the workspace database with
  `synchronous=FULL` before the agent is told the answer, so an acknowledged
  decision survives a process crash and, on a filesystem that honours `fsync`, a power
  cut. macOS acknowledges `fsync` before the data reaches the disk unless the
  application asks for `F_FULLFSYNC`, which stAirCase does not (it would triple the
  time of the test suite there); on a laptop that loses power at the wrong moment the
  last decision may be lost. Keep the audit exports and the certificates in git for
  anything that matters.
- **Mock agents are not compatibility evidence.** The adapters for Gemini CLI and
  OpenCode are tested against stand-ins until their real behaviour is demonstrated;
  [compatibility](compatibility.md) says which agent versions have actually been run.

Fuller answers (authenticated actors, project roles, key custody in a KMS, retention)
are planned only where a pilot needs them: see the [roadmap](../ROADMAP.md).
