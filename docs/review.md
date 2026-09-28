# Reviewing changes made elsewhere

Cloud agents (GitHub's Copilot coding agent, Codex in the cloud and others) work on
their own machines and hand you a branch or a pull request. stAirCase cannot decide
their steps before they happen, but it can make sure that only the files you approve
reach your branch, and certify that you did.

```bash
git fetch origin pull/42/head:pr-42
staircase review pr-42 --by "Copilot coding agent"
```

## What happens

1. stAirCase shows you what is about to be reviewed and asks you to confirm
   (`--yes` in scripts).
2. It creates a separate worktree of your **current** branch and brings the branch's
   changes into it **one file at a time**. It applies the branch's own diff, so newer
   work on your branch is kept. A file whose change no longer fits your branch is
   left out and reported.
3. Each file comes to you (or your [rules](approvals.md)) to approve or reject.
   Rejected files are left out.
4. Exactly the approved files are committed on a new branch, `staircase/run-N`, with
   `Assisted-by: <who made them>` and a signed
   [change certificate](audit.md#the-change-certificate-on-every-commit). The
   compiled plan, which the certificate's digest covers, names the exact commit you
   reviewed.

## What it guarantees

The files were changed before anyone decided them, so the change reaches
[CAL 2](adr/0001-core-promise-and-assurance-levels.md), not CAL 3: every byte that
reaches your branch was decided, but nobody governed what the agent did on its own
machine. A second person can still sign the result with
[`staircase sign`](audit.md#two-person-review-cal-4); CAL 4 needs CAL 3 first, so a
reviewed change stays at CAL 2.

`--allow` limits where the changes may go: a file elsewhere comes to you as drift.
