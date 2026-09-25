# Blueprints

A blueprint is the automation for a project — agents and their prompts,
routing, cases, stories and the paths each story may change — kept as files in
**its own repository**, not in the product repository it works on.

```
my-blueprints/hello/
├── blueprint.yaml
├── prd.md
└── prompts/coder.md
```

See [`examples/blueprints/hello`](../examples/blueprints/hello/blueprint.yaml)
for every field. Prompts and PRDs are inline (`prompt:`, `prd:`) or in files of
the blueprint (`prompt_file:`, `prd_file:`); files outside the blueprint's
directory (`..`, absolute paths, symlinks leading out) are refused, as are
unknown fields, unknown agents in edges and models no provider serves.

## Import: an immutable snapshot

```bash
staircase blueprint import ./my-blueprints/hello
staircase blueprint list
```

Import resolves the files, writes the blueprint as canonical JSON and names it
by the sha256 of that content. The same content always gets the same hash and
importing it again changes nothing; any edit — a single prompt word — is a new
blueprint. When the directory is a clean git checkout, its commit is recorded
too; otherwise import warns that the snapshot is not reproducible from source.

## Bind: apply it to a project

```bash
staircase project bind <project-id> <hash-or-prefix>
```

Binding creates a **new** topology version and the blueprint's cases and
stories (with their scope) in the project, and records the blueprint on the
project and on each case. Existing topologies, cases and stories are not
changed. Bind again after importing a new version of the blueprint.

## Runs of bound cases are pinned

`staircase compile <case>` writes the plan with the case's stories and its
blueprint. The `runtime.plan_pinned` gate then blocks a run when the plan is
not exactly the blueprint's case — for example after `topology agent add` on
the bound topology and a recompile. To change a bound case, change the
blueprint, import it, and bind again.

Every run records what it executed as its first audit event, `run_bound`:
the base commit, the topology version of the plan, the plan's sha256
(`plan_digest`) and the blueprint hash.

The blueprint's `limits` and the stories' `scope` are carried along for drift
supervision (not enforced yet).
