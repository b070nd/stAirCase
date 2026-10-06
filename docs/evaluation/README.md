# Evaluation of decision models (Jev, Laya)

stAirCase can ask an evaluation model whether a change that policy would approve looks risky or off the stories
(`--signal`, see [approvals](../approvals.md#a-decision-model-as-a-signal)). The model can only send a change to a person.
This directory keeps the measured evidence for the models stAirCase advertises, one file per model and run.

## Results

**None yet.** No number is published because none has been measured: the gateway path needs an AI Gateway key
(`LLM_GATEWAY_API_KEY`) for `typesafe-ai/jev`, and the local path needs `laya-serve` running (`pip install "laya[serve]"`,
model weights from Hugging Face). Neither was available when this was written. Until a run is kept here, the models'
accuracy is unknown, and stAirCase makes no claim about it.

| Path | Needed | State |
|---|---|---|
| `typesafe-ai/jev` through the AI Gateway | `LLM_GATEWAY_API_KEY` with the gateway's billing set up | not run |
| Laya, local (`laya-serve`) | the package and weights installed, `SIGNAL_API_KEY` if `LAYA_API_KEY` is set | not run |

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
# a local Laya:
SIGNAL_API_KEY=... go run ./src/tools/jeveval -model laya -base http://127.0.0.1:8000 -api systemone \
  -tool-version "$(git rev-parse --short HEAD)" -out docs/evaluation/$(date +%F)-laya.md
```

The report keeps the command, the server, the tool version, a digest of the labelled cases, every case's label and the
model's answer, the totals (accuracy, Brier score, risky changes missed, safe changes flagged, latency, cost) and the
limitations. The 24 cases are synthetic (`src/tools/jeveval/cases.json`); nothing from a real repository is sent. Commit the
file as it was printed; do not round or select.
