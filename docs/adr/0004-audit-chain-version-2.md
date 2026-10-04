# ADR 0004: The audit chain covers the event type (version 2)

- Status: accepted
- Date: 2026-10-05

## Context

Every run event is hash-chained: its `event_hash` is a SHA-256 over what came before
it. The first version hashed `payload + previous hash + commit hash`, joined without
separators. A review found two weaknesses:

- **The event type was not covered.** Anyone who can write the workspace database, or
  the entries of an exported checkpoint, can relabel an event without breaking the chain:
  hide a `yield_decided` by renaming it, or give an event the name another part of the
  product reads for its meaning (recovery trusts `yield_decided` events).
- **The parts were not separated**, so a byte could move from one field to the next with
  the same hash.

Changing the algorithm for everything would make every existing chain, and every
exported and anchored checkpoint, fail.

## Decision

- New entries carry `hash_version` 2: the SHA-256 of `staircase-chain-v2\n`, then the
  event type, payload, previous hash and commit hash, each as an 8-byte big-endian length
  and its bytes. Existing entries are version 1 (a database column with default 1; an
  exported entry without the field is version 1) and verify exactly as before.
- One function, `persistence.VerifyEntries`, decides for the store, `audit verify` and
  `inspect log`. A chain may mix versions. An unknown version is refused, and an entry
  cannot be downgraded: the stored hash is only valid under the version it was computed
  under.
- The version is added to the exported entry (`hash_version`, omitted for version 1), so
  signed checkpoints written before stay byte-identical when verified.
- An older stAirCase refuses a workspace that has the migration (it records every
  migration and stops on one it does not know).

## Consequences

- Version 1 runs keep the weakness: they cannot be strengthened afterwards without
  rewriting history that may already be anchored. The docs say so.
- Someone who holds the workspace signing key can still rewrite a whole chain and sign it
  again; signed checkpoints and the Rekor anchor are what stop that, as before.
- Conformance vectors (`docs/spec/audit-chain-vectors.json`) and an independent checker
  (`docs/spec/audit_chain_vectors.py`) pin the format.
