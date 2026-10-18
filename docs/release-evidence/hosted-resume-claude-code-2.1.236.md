# A resumed hosted session, with Claude Code 2.1.236 (2026-10-18)

What was asked ([ADR 0008](../adr/0008-governing-another-runner.md), section 2.1): when a runner resumes a session it started through
`staircase host claude`, does the session go on with the run it was governed as, does Claude Code remember its conversation, and is the
result still delivered only as stAirCase derives it?

**Setup.** stAirCase built from `feature/SC-123-hosted-resume` (`d176a64`); Claude Code 2.1.236, model haiku, its credential passed by
name (`--pass-env CLAUDE_CODE_OAUTH_TOKEN`); a new repository and workspace; `staircase host setup --allow "NOTES.md" --check "test -f
NOTES.md"`. Both sessions were started with **Cloudroom 0.1.57's own argument list** (as in the [first hosted
run](host-cloudroom-args-claude-code-2.1.236.md)), the first with `--session-id 7a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d`, the second with
`--resume` and the same id in its place. One stream-json user message on stdin each. Cloudroom itself was not run.

**Session 1** asked the agent to invent a code word, write it only in its reply, and create `NOTES.md` with the line `first`.
- Its reply said `CODEWORD: elephant`. `NOTES.md` was approved on evidence; the run ended `AWAITING_REVIEW` (review entry
  `de544e4045c5…`).
- Before the resume, the word was in no file of the workspace or the repository (apart from the runner's own log of its stream) and in no
  entry of the audit chain.

**Session 2** (`--resume`) asked: "What was the code word you told me before? Add it as a second line to NOTES.md, nothing else."
- Claude Code was started with `--resume <id>`; the chain's `agent_session` says `resumed`. Its reply: the code word was "elephant".
- The same run was continued, not a new one: `run_resumed` with `segment` 2, `context` `native_resume`, `next_seq` 2 and
  `withdrew_review` naming `de544e4045c5…`. The edit was approved on evidence, the checks ran again on the exact tree, and a new
  `awaiting_review` (`a23e77114d61…`) was offered.
- `staircase decide 1 --accept` committed `10c803cf6a86` with `NOTES.md` = `first`, `elephant`. A fresh clone with only the public key:
  `valid change certificate, CAL 3`, `rebuilt from 2 approved proposal(s): tree 501d8da688af is identical`.

The runner's stream held only JSON (207 and 166 lines), ending in Claude Code's `result`; none of stAirCase's own output. Claude Code also
sent `control_request` messages to initialize the runner's SDK MCP server (`cloudroom`), which only a runner answers. With no runner there,
nothing answered, and the sessions went on.

**Cost:** $0.079 and $0.047 as Claude Code reported them, $0.126 in all. That is more than the $0.05 estimated beforehand.

**What it does not show:** Cloudroom itself starting and resuming the stand-in; the other two cases of a resume (a run whose process was
killed, and a run already decided that a new run follows), which are tested with a stand-in Claude Code
(`TestHost_resume_continues_an_interrupted_run`, `TestHost_resume_after_a_decision_starts_a_new_run`); a long multi-turn session; and
whether "elephant" could have been guessed. It is a common word, so this record shows that a resumed session answered correctly, not that
guessing was impossible.
