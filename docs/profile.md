# A project's profile

Some things are true of a client's repository for every run: which files define the tests, which files must never be approved without a
person (a contract file, a schema), where a change may land, which checks to run. Typing that on every run is tedious, and keeping it in
the repository would put stAirCase into your client's code. A **profile** keeps it in your workspace, per project.

```bash
staircase project profile set 3 --protect "phpunit.xml" --protect "tests/**" \
  --sensitive "contracts/**" --scope "app/code/**" --check "vendor/bin/phpunit"
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

Instructions for the agent, the directories checks may see (`vendor/`) and files the agent must not read are further fields to come; this
page lists a field when it exists.
