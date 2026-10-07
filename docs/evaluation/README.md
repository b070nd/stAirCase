# Evaluation of decision models (Jev, Laya)

stAirCase can ask an evaluation model whether a change that policy would approve looks risky or off the stories
(`--signal`, see [approvals](../approvals.md#a-decision-model-as-a-signal)). The model can only send a change to a person.
This directory keeps the measured evidence for the models stAirCase advertises, one file per model and run.

## Results

| Path | Run | Risky changes missed (of 12) | Safe changes flagged (of 12) | `risky` accuracy | Report |
|---|---|---|---|---|---|
| Laya 0.4.0 local, checkpoint `typed-decisions` | 2026-10-07 | 11 | 0 | 13/24 | [report](2026-10-07-laya-typed-decisions.md) |
| Laya 0.4.0 local, checkpoint `english` | 2026-10-07 | 9 | 0 | 15/24 | [report](2026-10-07-laya-english.md) |
| `typesafe-ai/jev` through the AI Gateway | | | | | **not run**: needs `LLM_GATEWAY_API_KEY` with billing |

Read these plainly. At the fixed 0.5 threshold the signal uses, Laya let almost every risky change through: the risky changes scored
0.25 to 0.55 and the safe ones 0.16 to 0.50, and none scored high enough. It does lean the right way (in the kept tables a risky change had
a higher `risky` probability than a safe one in about 73% of risky-and-safe pairs for `typed-decisions` and 68% for `english`; computed from
the reports, not part of the tool), so the threshold, not the ranking, is what fails. The 24 cases are synthetic, the run is one per
checkpoint, and Laya answered the questions as phrased by stAirCase, with no tuning. So: **as configured today, Laya is not a safeguard**;
it can still only add a person's review, never remove one. A calibrated threshold per project (ROADMAP phase 3) would need the project's
own labelled changes.

Running Laya also found a real fault, now fixed: stAirCase sent a `noul` question's criteria keyed `yes` and `no`, and Laya 0.4.0 refuses any
key but `true` and `false` (HTTP 422), so every request failed until the client was corrected.

How it was run: Laya 0.4.0 in a virtual environment, `laya-serve` on loopback only with a bearer key, offline (`HF_HUB_OFFLINE=1`), checkpoints
`english` and `typed-decisions`, on macOS/arm64.

## What is verified without a model

The behaviour around a model is tested against fake servers, and holds whatever a real model answers
(`TestClient_an_invalid_answer_is_an_error_never_a_safe_one`, `TestSignal_a_wrong_late_or_invalid_answer_only_sends_the_change_to_a_person`,
`TestSignal_only_sends_changes_to_a_person`): a probability that is missing, outside 0..1 or of the wrong type, a choice that
was not offered, a reply that is not an answer, cut off or over 1 MiB, a server that is too slow (30 s) and an HTTP error each
send the change to a person and are audited as `signal_rated` with the error. A wrong answer (calling a risky change safe)
cannot approve or reject anything; it leaves the policy's decision as it was.

## Producing a run

```bash
LLM_GATEWAY_API_KEY=... make eval-jev ARGS="-out docs/evaluation/$(date +%F)-jev.md"
# a local Laya (model = the checkpoint: english, multilingual or typed-decisions):
SIGNAL_API_KEY=... go run ./src/tools/jeveval -model typed-decisions -base http://127.0.0.1:8000 -api systemone \
  -tool-version "$(git rev-parse --short HEAD)" -out docs/evaluation/$(date +%F)-laya-typed-decisions.md
```

The report keeps the command, the server, the tool version, a digest of the labelled cases, every case's label and the
model's answer, the totals (accuracy, Brier score, risky changes missed, safe changes flagged, latency, cost) and the
limitations. The 24 cases are synthetic (`src/tools/jeveval/cases.json`); nothing from a real repository is sent. Commit the
file as it was printed; do not round or select.
