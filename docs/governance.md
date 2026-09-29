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
```

- `policy.json` is the same file as a workspace's own ([approvals](approvals.md)).
  Test a change with `staircase policy test` before you merge it.
- `allowed_signers` is git's format: `<email> <ssh public key>` per line.
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
