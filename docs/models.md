# Models and API keys

Each agent uses one model. Different agents in the same team can use different
providers. stAirCase calls the providers directly over HTTPS; there is nothing else
to install.

## Supported providers

The model name decides the provider and which secret holds its key:

| Model name | Provider | Secret name |
|---|---|---|
| `claude-…` | Anthropic | `ANTHROPIC_API_KEY` |
| `gpt-…`, `o1-…`, `o3-…`, `o4-…` | OpenAI | `OPENAI_API_KEY` |
| `gemini-…` | Google | `GOOGLE_API_KEY` |
| `grok-…` | xAI | `XAI_API_KEY` |
| `provider/model`, e.g. `openai/gpt-4o` | an OpenAI-compatible gateway ([Vercel AI Gateway](https://vercel.com/ai-gateway) by default) | `LLM_GATEWAY_API_KEY`, optional `LLM_GATEWAY_URL` |

The `secret.provider_keys` gate stops a run until every model in the team has its key.

## Store a key

Keys are read from standard input, so they never appear in your shell history or in
the list of running processes:

```bash
printf %s "$ANTHROPIC_API_KEY" | staircase secret set ANTHROPIC_API_KEY
printf %s "$OPENAI_API_KEY"    | staircase secret set OPENAI_API_KEY
printf %s "$AI_GATEWAY_API_KEY" | staircase secret set LLM_GATEWAY_API_KEY
printf %s "https://gateway.example.com/v1" | staircase secret set LLM_GATEWAY_URL   # optional
```

Run `staircase secret set NAME` without input to type the value; it is not shown.
Setting a name again **replaces** the value (for example after you rotate a key).

- Keys are stored encrypted (AES-256-GCM) in the workspace database.
- A key is decrypted only to call its provider. It is never shown to the model,
  never written to a log, and removed from anything the audit chain records.
- `staircase secret list` shows names only, never values.
- `staircase secret rotate` re-encrypts every secret under a new workspace key.

**One key per project.** Add `--project <id>` to store a key for one project only
(for example a separate billing account). A project key wins over the global one.

## Choose the model of each agent

Set it when you add the agent:

```bash
staircase topology agent add 1 supervisor "You plan and route the work." --model claude-opus-4-6
staircase topology agent add 1 coder "You write code." --model gpt-4o
```

An agent added without `--model` gets the project's default model, set with
`staircase project config set <project-id> --default-model <model>`. With no default
either, it uses `claude-sonnet-4-6`.

## Limit what a run may spend

```bash
staircase project config set 1 --budget-cap 5     # US dollars per run; 0 = no limit
staircase project config show 1
```

The run shows tokens and estimated cost per agent as it goes, and stops (`KILLED`)
when the estimate reaches the cap. The prices are the providers' published standard
rates, read from their price pages on 2026-10-05 (Anthropic, OpenAI and Google; where a
price depends on the prompt's size the dearer tier is used), so the estimate is a ceiling,
not an invoice: it ignores batch and cache discounts. A model without a known price (a
newer one, or Claude Code) counts at $15 per million tokens in and $75 out, so an unknown
model can never slip past the cap. A dated name (`claude-haiku-4-5-20251001`) or a gateway
name (`provider/model`) is priced by its model. The validator's calls count too.

## Record once, replay offline

```bash
staircase run 1 --record-llm run1.jsonl    # save every model exchange
staircase run 1 --replay-llm run1.jsonl    # answer from the file: offline, free, repeatable
```

A replay stops with an error on any request the recording does not contain (a changed
prompt, plan or file) instead of guessing. Recordings contain your prompts and
repository content; they never contain keys. Treat them like source code.

## What leaves your machine

A real run sends the PRD, the stories, a map of the repository and the files the
agents read to the model provider you chose. Nothing is sent anywhere else. The
offline demo and replays send nothing.
