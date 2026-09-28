# Blueprints

A **blueprint** keeps your whole agent setup as files: the agents and their prompts,
how they hand work to each other, the cases, the stories and the paths each story
may change. The files live in **their own repository**, not in the product
repository the agents work on.

With the CLI you build the same setup command by command (see
[Getting started](../QUICKSTART.md)). A blueprint is better when you want to review
changes to prompts like code, reuse a setup, or prove later exactly which setup a
run used.

## What a blueprint looks like

```
my-blueprints/hello/
├── blueprint.yaml
├── prd.md
└── prompts/coder.md
```

```yaml
name: hello
supervisor: supervisor
agents:
  - name: supervisor
    model: claude-sonnet-4-6
    prompt: You coordinate the work.
  - name: coder
    model: claude-sonnet-4-6
    prompt_file: prompts/coder.md       # or the text inline with prompt:
edges:
  - {from: supervisor, to: coder}
  - {from: coder, to: supervisor}
cases:
  - slug: greet
    prd_file: prd.md                    # or the text inline with prd:
    stories:
      - text: Create a greeting file
        scope: {allow: [GREETING.md], max_files: 1}
limits:
  checkpoint_every: 5
  max_files_changed: 10
  max_scope_violations: 2
  max_run_secs: 600
```

The complete example is [`examples/blueprints/hello`](../examples/blueprints/hello/blueprint.yaml).
`scope` and `limits` are explained in [Drift supervision](drift.md).

stAirCase refuses a blueprint that has:

- a field it does not know (so a typo never goes unnoticed);
- a file outside the blueprint's folder (`..`, an absolute path, or a symlink that
  leads out);
- an edge to an agent that does not exist;
- a model no supported provider serves.

## 1. Import it

```bash
staircase blueprint import ./my-blueprints/hello
staircase blueprint list
```

Import reads all the files and stores the result as one snapshot, named by its
SHA-256 hash. The same content always gets the same hash, so importing it again
changes nothing. Any change, even one word of a prompt, makes a new blueprint with
a new hash. Old snapshots are never changed.

If the folder is a git checkout with no uncommitted changes, the commit is recorded
too. Otherwise import warns that the snapshot cannot be traced back to a commit.

## 2. Bind it to a project

```bash
staircase project bind 1 7f8d56acc5df    # 1 = the project; the hash, or its start
```

It prints the cases it created, for example `case #1  greet  (2 stories)`.

Binding adds the blueprint's team as a **new** topology version of the project, and
its cases and stories (with their scope) as new cases. Nothing that already exists
is changed.

After you change the blueprint, import it again and bind the new hash.

## 3. Compile and run as usual

```bash
staircase compile 1     # 1 = the case
staircase gate 1
staircase run 1
```

## Runs are pinned to the blueprint

The `runtime.plan_pinned` [gate](gates.md) stops a run when the compiled plan no
longer matches the blueprint — for example after someone added an agent to the
bound topology with `topology agent add` and recompiled. To change a bound case,
change the blueprint, import it and bind it again.

Every run records what it started from in its first audit event, `run_bound`: the
commit, the topology version, the hash of the plan and the hash of the blueprint.
So you can always tell exactly which prompts and rules a run used. See
[Audit evidence](audit.md).
