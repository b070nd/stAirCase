# Getting started

In about 15 minutes you will:

1. watch the whole flow once, offline, with no API key;
2. let AI agents make a real change in one of your repositories — with you
   approving every step;
3. review the result, accept it, and keep signed evidence of what happened.

New to the words used here (case, story, topology, proposal)? Read
[Concepts](docs/concepts.md) first — it takes five minutes.

## 1. Install

```bash
brew install b070nd/staircase/staircase
staircase init       # creates the workspace in ~/.staircase-workspace
staircase doctor     # checks that everything is ready
```

Other ways to install, and how to verify a download: [Install](docs/install.md).

## 2. Watch it work (offline, no key)

The demo runs the real product with a stand-in model, so it needs no API key and no
network. It needs Go, git and curl.

```bash
git clone https://github.com/b070nd/stAirCase.git
cd stAirCase
./demo/run-demo.sh
```

You will see an agent propose three changes (create, edit and delete a file). Each
one stops and waits for you: press Enter to approve. At the end the demo checks that
the new branch holds exactly the approved changes, that your checkout did not change,
and that the signed audit chain verifies. Two more modes show the safety checks
failing closed: `--tamper` and `--drift`. More in [the demo guide](demo/README.md).

## 3. Your first real run

You need a git repository with at least one commit, and an API key for a model
provider. The steps below use Anthropic; any supported provider works (see
[Models and API keys](docs/models.md)).

### Add your project

```bash
staircase vendor add acme
staircase project add acme shop --source ~/code/shop
```

Each command prints a number (`#1` for the first project). The examples below use
`1`; use the numbers your commands print.

### Store the model key

The key is read from standard input, so it never appears in your shell history:

```bash
printf %s "$ANTHROPIC_API_KEY" | staircase secret set ANTHROPIC_API_KEY
```

It is stored encrypted in the workspace and only ever sent to the model provider.

### Describe the team of agents

A *topology* is the team: a supervisor that starts and routes the work, and the
agents it can hand work to.

```bash
staircase topology register 1 supervisor            # 1 = the project
staircase topology agent add 1 supervisor \
  "You plan the work and hand it to the coder. End the run when the story is done." \
  --model claude-sonnet-4-6                          # 1 = the topology
staircase topology agent add 1 coder \
  "You write code. Make small, focused changes and explain each one." \
  --model claude-sonnet-4-6
staircase topology edge add 1 supervisor coder
staircase topology edge add 1 coder supervisor
```

You can also keep the team, the work and the rules as files in their own repository
and import them: see [Blueprints](docs/blueprints.md).

### Describe the work

A *case* is the work: a PRD (what to build) and *stories* (what "done" means).

```bash
staircase case new 1                                 # 1 = the project; prints the case number
printf 'Add a /health endpoint that returns HTTP 200 with the body "ok".\n' > prd.md
staircase case set-prd 1 prd.md
staircase story add 1 "GET /health returns 200 and the body ok"
staircase story scope 1 --allow 'src/**' --allow README.md   # 1 = the story
```

The scope lists the paths this story may change. A change anywhere else is shown to
you as *drift* — see [Drift supervision](docs/drift.md).

### Compile and check

```bash
staircase compile 1     # writes the plan the run will execute
staircase gate 1        # checks keys, plan, git; BLOCK failures stop the run
```

### Run it

```bash
staircase run 1
```

The agents start in a separate worktree of your repository. Your checkout is not
touched; agents see your **last commit**, so commit what they should see first.

Each time an agent wants to change a file, a screen shows you **exactly** what would
change:

- press **`y`** to approve — only these bytes will be committed;
- press **`n`**, type a reason and press **Enter** to reject — the agent gets your
  reason and can try again (**Esc** goes back).

To approve from another terminal, a script or a service instead, see
[Approvals](docs/approvals.md).

When the agents finish, stAirCase checks that the worktree holds exactly what you
approved and commits it on the branch `staircase/run-1`.

## 4. Review and accept

```bash
git -C ~/code/shop log --stat -1 staircase/run-1     # what was committed
git -C ~/code/shop diff HEAD...staircase/run-1       # the full change
staircase inspect runs                               # all runs and their status
staircase inspect log 1                              # every event of run 1
```

A commit does not prove that a story is done — you decide that. Check the work,
then accept each story:

```bash
staircase story accept 1
staircase case status 1      # COMPLETED once every story is accepted
```

Merge the branch the way you normally do. `staircase push 1` can push it and open a
GitHub pull request for you; it needs a GitHub token (see the
[CLI reference](docs/cli.md#staircase-push)).

Not happy with the result? `staircase case rollback 1` removes the run's branch and
worktree, keeps its audit record, and lets you run the case again.

## 5. Keep the evidence

Every decision of the run is on a tamper-evident audit chain. Export a signed copy:

```bash
staircase audit export 1
staircase audit verify ~/.staircase-workspace/audit/run-1.checkpoint.json
```

More, including anchoring the evidence in a public transparency log:
[Audit evidence](docs/audit.md).

## Next steps

- [Approvals](docs/approvals.md) — approve from a script or service, auto-approve
  with rules, or let a model review changes.
- [Blueprints](docs/blueprints.md) — keep your agent setup in its own repository.
- [Drift supervision](docs/drift.md) — keep runs on their stories.
- [Safety boundary](docs/safety.md) — what stAirCase does and does not protect.
- [Troubleshooting](docs/troubleshooting.md) — when something does not work.
