# ADR 0010: One replay of the audit chain

- Status: accepted (2026-10-17). The owner settled every question of the design session (Q1 to Q23) as proposed. It is built in
  steps on one branch, each step with `make check` green.
- Related: ADR 0004 (audit chain version 2), ADR 0005 (durable continuation, section 1, which this ADR carries out), ADR 0006 (Away
  mode), ADR 0007 (prototype bridging), ADR 0009 (declared commands).

## Context

ADR 0005 asks for "one pure function" that replays a run's chain, shared by recovery and resume, so the two "cannot disagree about
what a run approved". What was built instead is about twenty hand-written `json.Unmarshal` calls on event payloads in eight files.
`yield_decided` alone is parsed in eleven places, each with its own anonymous struct, and three writers build payloads as
`map[string]any`.

A comparison of the readers found that they do disagree:

- Recovery carries undecided requests across a resume. Resume resets them.
- Neither treats `review_delivered` as the end of a chain.
- Recovery refuses a run whose earlier final review no longer matches, even when a later review does.
- Prototype and Adopt pair a decision with the most recent request. With interleaved requests that is the wrong request.
- The drift supervisor's replay differs from the live run in three ways.
- The saved terms (`options.json`) are trusted without a check against the chain.

Two readers also read rows that were never verified: `audit export` and `lastDriftHalt`. Both were fixed first, on the branch before
this one.

## Decision

### 1. One module: `internal/history`

The module holds the typed payload of every event a reader uses, and one replay of a verified chain into a `History` value.

- **Only verified chains (Q2).** There is a single entry point, `history.Read(store, runID)`. It reads the chain through
  `ListVerifiedEventLogs`, and nothing builds a `History` from rows that were not verified. The display commands (`inspect`, `replay`,
  the `clean` archive) keep showing raw rows, because showing them is what they are for.
- **Typed payloads with today's keys (Q3, Q13).** Writers fill typed structs whose JSON keys and omit-when-empty behaviour match
  what is written today. A golden test per event type pins this. Old and new chains read the same, and ADR 0004 is untouched: the
  chain hashes whatever bytes are written.
- **One fold (Q4, Q15).** One walk over the events gives the binding, every decision with its request, the parked and adopted
  proposals, the offered review, the continuation's counters, the drift halt, the usage and what sealed the chain. Each caller replays
  afresh. There is no cache to go stale.
- **Each decision carries its request (Q7).** Prototype, Adopt and recovery never go back to the raw events.
- **Open parts stay raw (Q8).** `evidence` and `validator_state` vary by source, so they are kept as raw JSON. A piece of them is
  typed only when a reader needs it.
- **Unknown events and fields are ignored (Q9).** The workspace already refuses a database written by a newer stAirCase.
- **Pure (Q14).** Writers compute digests, evidence and signatures. The module only shapes and reads, with no git and no files. It
  depends on `domain` and `persistence`, never on `orchestrator`.

### 2. One grammar for every reader (Q5, Q10)

A history that no run can have written is refused with one typed error, `GrammarError`. It names the entry's position, its event
type and the reason, and every caller words it the same way. Analytics readers (`lessons`, `policy test`, `policysim`) report such a
run and skip it rather than miscount it. The rules:

1. `run_bound` comes first, and only once.
2. Every decision except a final review uses up one request audited before it, in the same segment and of the same action type.
   Requests that a segment left undecided do not carry over a `run_resumed` (Q16).
3. A decision that names its request's chain entry (`request_entry`) uses up exactly that request (Q19). Chains written before the
   field existed fall back to rule 2.
4. Proposal numbers increase.
5. `commit_prepared` ends the agent's part of the run. No request, no proposal decision and no resume may follow it. Only a final
   review may, which is what recovery asks of a run that died between naming its commit and moving its branch. `certificate_issued`,
   `run_recovered` and `review_delivered` end the chain: no decision and no resume after them (Q17).
6. A known event whose payload cannot be read is refused.

### 3. The readers' answers

- **Which final review counts (Q18).** Only a final review made after the last approval counts. A run continued after an approved
  final review can be recovered again: the earlier review is history, not a claim about the current approvals.
- **Adoption (Q19).** `adopts` is read from the decision, which must be approved, rather than from the request before it.
- **The drift supervisor's replay matches the live run (Q20).** `declared` decisions skip the drift check, as they do live.
  Approvals that expired, and so carry paths, are replayed rather than skipped. A resume that acknowledged drift (`ack_drift`) clears
  the halt, so the next continuation does not need `--ack-drift` again.
- **Recovery's final review (Q21).** It is written as a `yield_decided` final review, in the live run's shape, instead of
  `recovery_final_review`. The certificate's `decisions` counts the same things as before: every decision, the final review
  included. A recovered run now counts its review as a finished run counts its own. Chains that hold the old event read as before,
  and nothing reads that event.
- **The saved terms are checked (Q22).** `run_bound` records `terms_sha256`, the digest of the `options.json` the run saved. Resume
  and Prototype refuse terms that do not match. Runs bound before the field existed are read as before. This is the first piece of
  the architecture review's candidate #2.
- **Recovery asks for a final review more often than a live run (Q23).** A live run asks after `task` or `evidence` approvals, or
  when the reviewer model left something unreviewed. Recovery asks after any approval not made by a person or a policy rule,
  reviewer-model approvals included. This is stricter and safe, and it is kept and documented (`docs/audit.md`).

### 4. Delivery (Q6, Q11, Q12)

1. `GLOSSARY.md`, and the package with table tests for every rule and every field.
2. `audit export` and `lastDriftHalt` read through the package.
3. Recovery and resume, with the rule changes above.
4. Review, decide, prototype and adopt, with the pairing fix.
5. Certify.
6. The CLI readers (`lessons`, `policy test`) and `policysim`.
7. The writers, with `request_entry`, `terms_sha256` and recovery's final review in the live shape.

Test hooks that exist only to reach past the interface (`ExportedAuditedHistory`, `ExportedRunContext`) are deleted once nothing
uses them. The integration tests stay as they are and keep proving the callers.

## Consequences

- The rules about what a run can have written live in one place, with one test table.
- A reader cannot read unverified rows by accident.
- A payload key cannot be renamed by accident: its golden test fails.
- Old chains replay as before, except where a rule above was a bug fix. That is the case for recovery after a continued final
  review, adoption with interleaved requests, and the acknowledged drift halt.
