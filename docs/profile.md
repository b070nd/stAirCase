# A project's profile

Some things are true of a client's repository for every run: which files define the tests, which files must never be approved without a
person (a contract file, a schema), where a change may land, which checks to run. Typing that on every run is tedious, and keeping it in
the repository would put stAirCase into your client's code. A **profile** keeps it in your workspace, per project.

```bash
staircase project profile set 3 --protect "phpunit.xml" --protect "tests/**" \
  --sensitive "contracts/**" --scope "app/code/**" --visible vendor --check "vendor/bin/phpunit"
staircase project profile show 3
staircase project profile set 3 --clear checks          # take a field away
```

Project ids are in `staircase project list <vendor>`. A session (`staircase claude "..."`) uses the project whose source is the repository
you are in.

## What a profile holds

| Field | Meaning |
|---|---|
| `protect` | paths or patterns, from the repository's root, where a check is defined: a change to one always needs a person, and is parked while you are away |
| `sensitive` | more paths a change to which always needs a person, on top of the built-in list (CI, build, dependency and secret files). A contract file belongs here. A profile can add to the list and never take from it |
| `scope` | where a change may land: a file elsewhere comes to a person as drift. For `claude`, `codex` and `gemini` it is the default for `--allow`; for `run` it narrows the stories' scope |
| `visible` | ignored directories or files of your checkout that the checks need, such as `vendor/` or `generated/`: literal paths from the repository's root. Checks see them, read-only, in the checkout of the commit they run in ([what that is and is not](safety.md)) |
| `unreadable` | paths or patterns the agent must not read, such as a tracked Composer `auth.json`. `--unreadable-defaults` writes the usual list into the profile (`auth.json`, `.env` files, private keys). An agent that can be held to it refuses to read them; one that cannot is not started on the project ([per agent](safety.md)) |
| `instructions` | what every agent on the project is told before its task, as text of at most 32 KiB: the house's conventions ("patches only, never edit `vendor/`", the copyright header). Set with `--instructions "..."` or `--instructions-file <file>` (the profile keeps a copy; the file may go) |
| `checks` | commands, such as the tests, run on the commit a run makes |
| `sandbox` | where approved commands and checks run: `auto`, `required`, `off` or `docker:image=<ref>` |

## How a run uses it

A run reads the profile when it starts and fills what the command line left out: a flag always wins (`--check`, `--protect`, `--sandbox`,
`--allow`), then the profile, then the default. A project's `sensitive` paths are always added: no flag takes them away. `staircase run`
takes `--allow` too, so the same scope can be given to every command.

The resolved values are the run's terms, saved with it, and the profile's **digest** is in them: a reviewer can see which profile a run
started under, and a continuation keeps the one it started with even if you change the profile in between. A project without a profile
behaves as before.

The profile is a file in the workspace (`profiles/project-<id>.json`, next to the policy), never in the repository. A profile that cannot
be read stops the run: starting under less than the project asks is the one wrong answer. It is not signed (the policy file can be; a
signed profile is not built yet).

**The environment.** A check runs in a throwaway checkout of the commit, and a checkout holds tracked files only: a directory your
repository ignores is not in it, so a check that needs composer's `vendor/` could not pass. `visible` (or `--visible` for one run)
declares such directories. The environment applies to the checks, wherever they run: after the commit, when the agent says it is done,
and as evidence for an approval. It is shown read-only, it is not in the agent's worktree and not part of what is approved, and the
record of a check and the change certificate say that the check saw it. A declared path that does not exist in your checkout, or that
git tracks, refuses the run before anything is made. Bubblewrap and Landlock are not supported for it yet, and a run on either refuses;
use the macOS sandbox, `--sandbox docker:image=<image>` (the image is never pulled for you) or `--sandbox off`.

**Instructions.** The project's instructions reach every agent, Claude Code, Codex, Gemini, OpenCode or the built-in runtime, in the same
place: after an away run's own brief and before the task. Nothing about them is in your repository, so no `CLAUDE.md` is needed there.
`staircase case briefing <case-id>` prints the exact text an agent of a compiled case is told, composed by the function the agents
use. The instructions are saved with the run's terms, their digest is bound on `run_bound` (`instructions_sha256`), and a continuation
tells its agent the same words. They are **instructions to a model, not enforcement**: an agent can ignore them. What must hold belongs
in `protect`, `sensitive` and `scope`, which a person decides.

**What the agent must not read.** A file your repository tracks is in the agent's worktree, and what an agent reads can go to your model
provider (the [safety page](safety.md) says so). `unreadable` names such files. It is a boundary only where an agent can be held to it, so
the profile decides what each agent may do on the project, and no agent is started under a promise it cannot keep: Codex, Gemini CLI and
OpenCode have sandboxes that restrict what they write, not what they read, so a run of them on a project with unreadable paths is
refused, naming the agent. The same applies to `--unreadable` for one run, which adds to the profile and never replaces it.
