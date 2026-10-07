# OpenCode 1.18.35: one real governed run

Run on 2026-10-07 on macOS/arm64 with the owner's consent and a $0.05 cap, with `./demo/smoke.sh opencode`
(`SMOKE_OPENCODE_MODEL=openai/gpt-4.1-mini`, `SMOKE_MAX_SECS=240`). OpenCode 1.18.35 was installed with
`npm install -g opencode-ai@1.18.35` (the Homebrew formula was refused: it needs an untrusted tap). Its login is the owner's
OpenAI API key in OpenCode's own data folder; stAirCase did not pass it.

| Run | What happened | Cost (OpenCode's own record) | Result |
|---|---|---|---|
| free check, model `openai/nonexistent-model` | stopped at the model call; the run failed, nothing was committed | 0 | as expected |
| 1 | one proposal (`write` of `GREETING.md`), approved through the approval API | 0.0056444 USD (10959 input, 124 output tokens) | commit certified CAL 3; `verify --rebuild` in a fresh clone with only the public key: tree identical |
| 2 (`REJECT_FIRST=1`) | first proposal rejected, the retry approved; the committed bytes are the retry's, not the rejected text | 0.006898 USD (11145 input, 189 output tokens) | commit certified CAL 3; fresh-clone verify and rebuild: tree identical |

Total 0.0125 USD. The run's session cost is read from OpenCode's own database
(`opencode db "select title, cost, tokens_input, tokens_output from session ..."`).

Checked beforehand, with no model call, on 1.18.35: `run <message> --model provider/model`; the environment variables
the adapter sets (`OPENCODE_CONFIG_DIR`, `OPENCODE_CONFIG_CONTENT`, `OPENCODE_DISABLE_AUTOUPDATE`); that `opencode debug config`
lists the adapter's plugin from `OPENCODE_CONFIG_DIR/plugins/staircase.js`; and the hook names `tool.execute.before`
and `tool.execute.after` in the binary.

What it does **not** show: any tool but `write`; a command (`--allow-shell-exec`); a second OpenCode version or model; the
control for a change nobody governed; session resume (OpenCode has none: a resume needs `--fresh-context`).
