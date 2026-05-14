# stAirCase

**The control layer between AI plans and enterprise execution.**

stAirCase is not an agent framework. It is the architectural layer around agents:
the place where plans become governed, auditable, cross-project execution without
polluting the repositories being changed.

AI coding agents are useful, but unmanaged agent workflows leave a trail of
temporary prompts, generated plans, copied context, local configuration, loose
secrets, and half-auditable decisions inside product repos. stAirCase keeps that
agent exhaust out of the target projects. It centralizes orchestration state,
policy checks, approvals, secrets, dependency context, and run evidence in a
separate workspace.

The goal is simple:

> A plan enters stAirCase. stAirCase decides whether, how, and under what
> controls it may execute.

## What It Is For

stAirCase is designed for teams that want AI-assisted development without giving
agents unbounded access to source code, secrets, or cross-repository change
coordination.

It is useful when you need to answer questions like:

- Which plan was executed?
- Which projects did it affect?
- Which upstream projects were relevant?
- Which agent/tool requested a secret or edit?
- Which quality gates passed before execution?
- Who approved a risky action?
- What branch isolated the changes?
- What evidence can be exported after the run?

## Core Thesis

stAirCase exists to make agentic software work **addressable, governable, and
clean**.

- **Addressable**: vendors, projects, dependencies, cases, topologies, runs, and
  event logs live in a queryable workspace instead of scattered repo files.
- **Governable**: quality gates, encrypted secrets, human approval, branch
  isolation, and audit checkpoints wrap execution before an agent can affect a
  project.
- **Clean**: target repos do not get spammed with agent metadata, prompt files,
  runtime state, temporary orchestration scripts, or copied context.

## What It Is Not

stAirCase is intentionally not trying to replace every agent runtime.

It is not:

- a general chat UI
- a prompt notebook
- a repo-local `.agent/` metadata system
- a replacement for CI
- a replacement for code review
- a model provider abstraction as the primary product

It is the enterprise control plane around those pieces.

## High-Level Workflow

```text
Plan / PRD
   |
   v
Case in stAirCase
   |
   v
Project DAG + topology + repo context
   |
   v
Quality gates + policy checks + secret boundaries
   |
   v
Isolated execution branch + authenticated IPC runtime
   |
   v
Human approval for risky actions
   |
   v
Audit log, run record, checkpoint, branch result
```

The important boundary is that the orchestration state stays in `$STAIRCASE_DIR`.
The target repository only receives approved changes on an isolated
`staircase/run-*` branch.

## Key Capabilities

### Zero-Trace Project Attachment

Project source repositories are linked into stAirCase, but stAirCase does not
write its runtime state into them. Workspace state, generated execution scripts,
SQLite records, virtualenv files, IPC sockets, event logs, keys, and audit
material stay outside the target repo.

The target repo remains a product repo, not an agent workspace.

### Cross-Project Orchestration

Enterprise changes often span multiple services. stAirCase models vendors,
projects, dependencies, components, cases, and topologies so a plan can be
compiled with awareness of upstream and downstream context.

That makes changes like API migrations, dependency updates, platform refactors,
and security remediation addressable across more than one repository.

### Governed Execution

Before a run executes, stAirCase can check structure, security, runtime
readiness, dependency state, source paths, topology freshness, and secret
availability. These gates turn "agent, go do this" into a controlled transition
from plan to execution.

### Secret Boundaries

Secrets are encrypted at rest in the stAirCase workspace. The Python execution
runtime requests secrets over an authenticated IPC channel. The AES key stays in
Go; the Python process receives only the plaintext value it needs at use time.

This avoids passing API keys through broad environment variables or sprinkling
secret material across project directories.

### Human-in-the-Loop Approval

Agent edits and other sensitive actions can pause for operator approval through
the TUI or an approval HTTP endpoint. Approval, rejection, feedback, and
execution state are part of the controlled run, not side-channel chat history.

### Audit and Evidence

Runs produce event logs and checkpoints suitable for later inspection. The
system is designed around the idea that enterprise users need evidence after the
fact, not just a successful local command.

## Architecture

```text
+-----------------------------+
| stAirCase CLI               |
| commands, gates, inspection |
+--------------+--------------+
               |
               v
+-----------------------------+
| Workspace control state     |
| SQLite, encrypted secrets,  |
| cases, topology, runs, logs |
+--------------+--------------+
               |
               v
+-----------------------------+
| Execution boundary          |
| generated Python harness,   |
| authenticated IPC, HITL     |
+--------------+--------------+
               |
               v
+-----------------------------+
| Linked project repositories |
| clean repos, isolated       |
| staircase/run-* branches    |
+-----------------------------+
```

The Go binary owns the control plane: persistence, gates, secrets, IPC, audit,
branch safety, and operator interfaces. The generated Python harness owns the
agent runtime path and talks back to Go through IPC.

## Example Command Path

```bash
# Build from source
CGO_ENABLED=0 go build -o staircase ./src/cmd/staircase/

# Initialize the external control workspace
export STAIRCASE_DIR="$HOME/.staircase"
staircase init

# Register a project without adding agent files to that repo
staircase project add mycompany payments-api --source /home/you/code/payments-api

# Store provider credentials in the workspace vault
staircase secret set ANTHROPIC_API_KEY sk-ant-...

# Define an execution topology for the project
staircase topology register 1 supervisor --runtime langgraph
staircase topology agent add 1 supervisor "Route work to the right specialist" --model claude-opus-4-6
staircase topology agent add 1 coder "Implement approved code changes" --model claude-sonnet-4-6
staircase topology edge add 1 supervisor coder

# Create, compile, gate, and run a case
staircase case new 1
staircase compile 1
staircase gate 1
staircase run 1
```

See [QUICKSTART.md](QUICKSTART.md) for a fuller walkthrough.

## Enterprise Use Cases

- Cross-service API changes where one project depends on another.
- Security remediation that needs controlled edits, approval, and evidence.
- Platform migrations across many repositories without repo-local agent clutter.
- Regulated development workflows where every agent action needs an audit trail.
- Internal developer platforms that want to offer AI execution behind policy.

## Design Principles

### Keep Repositories Clean

Agent orchestration belongs outside product repos. A source repository should not
need to carry prompt caches, run metadata, generated plans, or tool state just
because an AI system helped modify it.

### Put Controls Before Execution

The valuable enterprise boundary is between "we have a plan" and "something is
allowed to execute." stAirCase treats that boundary as a first-class system.

### Make Cross-Project Work Explicit

Project dependencies, topology versions, run history, and upstream readiness
should be visible and queryable. Cross-project execution should not depend on a
prompt remembering which services matter.

### Preserve Human Authority

The agent can propose. stAirCase controls when proposals become file changes,
branch commits, or auditable run events.

### Prefer Boring Evidence

Enterprises need durable records more than clever demos. stAirCase favors
SQLite state, explicit gates, encrypted storage, branch isolation, and exportable
logs over hidden session memory.

## Current Status

stAirCase is an early-stage implementation of this control-layer architecture.
The current codebase includes:

- Go CLI and SQLite persistence
- project and dependency registry
- topology and case management
- Python/LangGraph execution generation
- encrypted secret storage
- authenticated Go/Python IPC
- human approval surfaces
- quality gates
- run/event inspection
- audit checkpoint primitives

The next useful milestone is not more abstraction. It is a tight enterprise demo:
take one plan, execute it against one or more linked repos, require approval for
risky actions, keep target repos clean, and export convincing evidence afterward.

## License

[MIT](LICENSE) (c) Botond Biro
