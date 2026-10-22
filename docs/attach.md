# Agents in your own checkout: attach and seal

Some agents edit your checkout directly: Cursor, IDE assistants, or any tool
stAirCase has no hooks for. stAirCase cannot decide their steps before they
happen, but it can make sure only the changes you approve are committed, and
certify that you did.

```bash
staircase attach                      # while the agent works: plain git commit is refused
# … the agent edits your files …
git add -p                            # stage what you want to commit
staircase seal -m "Add a health check" --by Cursor
staircase attach --off                # back to normal
```

## seal

`staircase seal` is used instead of `git commit`:

1. It takes exactly what is staged, as it is now.
2. Each staged file comes to you (or your [rules](approvals.md), guards, validators
   and signals) to approve or reject, as in [`staircase review`](review.md).
3. Your current branch moves to **one new commit** with the approved files, your
   message, an `Assisted-by: <agent>` trailer and a
   [change certificate](audit.md#the-change-certificate-on-every-commit).

Rejected changes are not committed and not thrown away: they stay in your working
files, uncommitted. Edits you did not stage are left alone. If your branch moved
while you were deciding, it is not touched, and the certified commit stays on its
`staircase/run-N` branch.

`seal` takes `--allow <glob>` (repeatable) like `review` and the other commands: a staged file outside that scope comes to you as
drift instead of as a routine approval.

`--check` commands run on the sealed commit in a throwaway checkout of it, not in your working directory, and `--sandbox off` only
removes the sandbox: the checkout still holds tracked files only, so a directory your repository ignores (`vendor/`, `node_modules/`,
`generated/`) is not there. A check that needs one cannot pass until stAirCase can show it to a check (a project's environment).

The files were changed before anyone decided them, so a sealed commit reaches
[CAL 2](adr/0001-core-promise-and-assurance-levels.md). For CAL 3, run the agent
through stAirCase (`staircase claude`, `staircase codex`).

## attach

`staircase attach` installs a pre-commit hook in the repository that refuses
`git commit` and shows how to seal. It guards against committing an agent's work by
accident. It is not a wall: `git commit --no-verify` gets through, which is why the
[CI check](audit.md#require-certificates-on-pull-requests) is the second line.
An existing pre-commit hook of your own is never replaced; `attach --off` removes
only stAirCase's.

Nothing of stAirCase is written into your repository's files: the hook goes under the git directory (`.git/hooks`), and stAirCase's
other traces are refs and worktree metadata under `.git`. If `core.hooksPath` points to a directory inside your working tree (a
`.husky` folder, say), `attach` refuses, naming the setting, because the hook would be an untracked file in your code; point
`core.hooksPath` outside the repository, or unset it, and attach again. `attach --off` works whatever the setting is.
