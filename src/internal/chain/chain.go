// Package chain is the audit chain's rule: how an entry is hashed, how a run's entries are verified, and how big one entry may
// be. It holds no database and no event: the store keeps the rows and calls it when it writes one, and history gives the entries
// their meaning. Anything that must agree on the algorithm (the store, `audit verify`, `inspect log`) calls it here.
package chain

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/b070nd/stAirCase/src/internal/domain"
)

// Version is the chain format new entries are written under.
//
// Version 1 (SHA-256(payload ‖ prev ‖ commit)) did not cover the event type, and joined its parts without separators. Version 2
// covers the event type and length-prefixes every part (docs/audit.md, "The hash chain"). Entries written before it stay
// version 1 and verify as they always did.
const Version = 2

// MaxPayload is the most one entry holds (ADR 0014). What an agent supplies is capped lower, at 64 KiB, by the agent host before it
// is written; this is the ceiling for what the orchestrator writes itself, such as the review an away run offers for a large change.
const MaxPayload = 1 << 20

// ErrPayloadTooLarge is returned, with the event and its size, for a payload over MaxPayload. The entry is not written: a payload
// cut short would still hash and verify, and so stand as the record of something the run never wrote.
var ErrPayloadTooLarge = errors.New("the audit entry is too large")

// CheckSize refuses a payload no entry can hold.
func CheckSize(eventType, payload string) error {
	if len(payload) > MaxPayload {
		return fmt.Errorf("%w: %s is %d bytes and an entry holds at most %d", ErrPayloadTooLarge, eventType, len(payload), MaxPayload)
	}
	return nil
}

// HashV1 is the version 1 chain hash: SHA-256(payload ‖ prevHash ‖ gitCommitHash).
//
// The payload comes first and the commit is a third input, where the original checklist specified SHA-256(prev_hash ‖ event_body).
// That is intentional: including the commit ties each entry to the revision that wrote it. Changing the order would invalidate
// every audit chain written so far.
func HashV1(payload, prevHash, gitCommitHash string) string {
	h := sha256.Sum256([]byte(payload + prevHash + gitCommitHash))
	return fmt.Sprintf("%x", h)
}

// HashV2 is the version 2 chain hash: SHA-256 of the label "staircase-chain-v2\n" followed by event type, payload, previous hash
// and commit, each as an 8-byte big-endian length and its bytes.
func HashV2(eventType, payload, prevHash, gitCommitHash string) string {
	h := sha256.New()
	h.Write([]byte("staircase-chain-v2\n"))
	for _, field := range []string{eventType, payload, prevHash, gitCommitHash} {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(field)))
		h.Write(n[:])
		h.Write([]byte(field))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Expected is the hash entry e must carry, given the hash of the entry before it, under the chain version e names.
func Expected(e domain.RunEventLog, prevHash string) (string, error) {
	switch e.HashVersion {
	case 0, 1:
		return HashV1(e.Payload, prevHash, e.GitCommitHash), nil
	case 2:
		return HashV2(e.EventType, e.Payload, prevHash, e.GitCommitHash), nil
	}
	return "", fmt.Errorf("entry id=%d uses chain version %d, which this version of stAirCase does not know", e.ID, e.HashVersion)
}

// Check judges every entry on its own: its hash must be the one it should carry given the hash stored on the entry before it. The
// result has one element for each entry, nil where the entry is intact. A broken entry does not hide the ones after it, which is
// what the commands that show a chain want; Verify is the single answer the commands that rely on one want.
func Check(entries []domain.RunEventLog) []error {
	out := make([]error, len(entries))
	prevHash := ""
	for i, e := range entries {
		want, err := Expected(e, prevHash)
		switch {
		case err != nil:
			out[i] = err
		case e.EventHash != want:
			out[i] = fmt.Errorf("hash mismatch at entry %d (id=%d, %s): stored=%s computed=%s", i+1, e.ID, e.EventType, e.EventHash, want)
		}
		prevHash = e.EventHash
	}
	return out
}

// Verify recomputes every hash of a run's entries, oldest first, and names the first one that does not match: the single check
// that the store, `staircase audit verify` and `staircase inspect log` share.
func Verify(entries []domain.RunEventLog) error {
	for _, err := range Check(entries) {
		if err != nil {
			return err
		}
	}
	return nil
}
