# A project's profile

Some things stay true of a client's repository for every run: which files define the tests, which files always need a person to
approve a change (a contract file, a schema), where a change may land, which checks to run. Typing them on every run is tedious, and
keeping them in the repository would put stAirCase into your client's code.

A **profile** keeps them in your workspace instead, one per project.

```bash
staircase project profile set 3 --protect "phpunit.xml" --protect "tests/**" \
  --sensitive "contracts/**" --scope "app/code/**" --visible vendor --check "vendor/bin/phpunit"
staircase project profile show 3
staircase project profile set 3 --clear checks          # remove one field
```

Find project ids with `staircase project list <vendor>`. A session such as `staircase claude "..."` uses the project whose source is the
repository you are in.

## What a profile holds

| Field | Meaning |
|---|---|
| `protect` | Paths or patterns, from the repository root, where a check is defined. A change to one always needs a person, and is parked while you are away. |
| `sensitive` | More paths that always need a person, on top of the built-in list (CI, build, dependency and secret files). Put contract files here. A profile can add to the list but never remove from it. |
| `scope` | Where a change may land. A file outside it comes to a person as drift. For `claude`, `codex` and `gemini` it is the default for `--allow`; for `run` it narrows the stories' scope. |
| `visible` | Ignored directories or files that the checks need, such as `vendor/`. Literal paths from the repository root. Checks see them read-only ([details](safety.md)). |
| `unreadable` | Paths or patterns the agent must not read, such as a tracked Composer `auth.json`. `--unreadable-defaults` adds the usual list: `auth.json`, `.env` files, private keys ([per agent](safety.md)). |
| `instructions` | Text every agent on the project is told before its task, up to 32 KiB: house rules such as "patches only, never edit `vendor/`". Set it with `--instructions "..."` or `--instructions-file <file>`. The profile keeps a copy, so the file can go. |
| `checks` | Commands, such as the tests, run on the commit a run makes. |
| `sandbox` | Where approved commands and checks run: `auto`, `required`, `off` or `docker:image=<ref>`. |

## How a run uses it

A run reads the profile when it starts and fills in what the command line left out. A flag wins, then the profile, then the default.
The project's `sensitive` paths are always added, and no flag removes them.

The values the run ends up with are its terms, saved with it, and they include the profile's **digest**. A reviewer can see which profile
a run started under, and a continuation keeps that profile even if you change it in between. A project without a profile behaves as before.

The profile is a file in the workspace (`profiles/project-<id>.json`), never in the repository. If it cannot be read, the run does not
start, because starting with less than the project asks for would be wrong. The profile is not signed.

## The environment

A check runs in a throwaway checkout of the commit, and a checkout holds tracked files only. A directory your repository ignores is not
in it, so a check that needs composer's `vendor/` could not pass. `visible` (or `--visible` for one run) lists such directories.

- It applies to all checks: after the commit, when the agent says it is done, and as evidence for an approval.
- The directories are read-only. They are not in the agent's worktree and not part of what is approved.
- The check's record and the change certificate say that the check saw them.
- A path that does not exist in your checkout, or that git tracks, stops the run before anything is made.
- Bubblewrap and Landlock do not support this yet, so a run on either is refused. Use the macOS sandbox, `--sandbox docker:image=<image>`
  (the image is never pulled for you) or `--sandbox off`.

## Instructions

The project's instructions reach every agent (Claude Code, Codex, Gemini, OpenCode or the built-in runtime) in the same place: after an
away run's own brief and before the task. Nothing is written into your repository, so you do not need a `CLAUDE.md` there.

`staircase case briefing <case-id>` prints exactly what an agent is told. The instructions are saved with the run's terms, their digest is
on `run_bound` (`instructions_sha256`), and a continuation tells its agent the same words.

They are instructions to a model, not enforcement. An agent can ignore them. Anything that must hold belongs in `protect`, `sensitive` or
`scope`, where a person decides.

## What the agent must not read

A file your repository tracks is in the agent's worktree, and what an agent reads can go to your model provider (see the
[safety page](safety.md)). `unreadable` names such files.

It only works where an agent can be held to it. The built-in agent and Claude Code are. Codex, Gemini CLI and OpenCode have sandboxes that
limit what they write, not what they read, so a run with them on a project with unreadable paths is refused, and the message names the
agent. `--unreadable` for one run adds to the profile and never replaces it.
