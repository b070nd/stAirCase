package history

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/b070nd/stAirCase/src/internal/chain"
	"io"
	"time"

	"github.com/b070nd/stAirCase/src/internal/obs"
	"github.com/b070nd/stAirCase/src/internal/persistence"
)

// Event is a typed audit event: a value that knows its own name and whose JSON keys are its payload. Only this package declares
// events (the method is unexported), so a new event needs a type here first and no entry can be written under a name that does
// not fit its payload (ADR 0014).
type Event interface{ eventName() string }

// Settings are what one run's Writer is configured with. The zero value is the orchestrator's: payloads are written as Encode
// shapes them, and an entry over the chain's ceiling is refused.
type Settings struct {
	// Plain writes the value as it marshals, in the order of its fields and with no type key added. The agent host has always written
	// its entries so, and a payload's bytes do not change (the fixture of internal/chain pins them).
	Plain bool
	// Scrub, when set, is applied to every payload before it is kept: the values delivered to the agent never reach the chain.
	Scrub func(payload []byte) []byte
	// Cap, when positive, is the most an agent-supplied payload may hold; over it the entry is written as a short note of what was
	// left out (its kind, size and digest) and is not an error.
	Cap int
	// Debug, when set, gets one line for each entry written, as written.
	Debug io.Writer
	// OnFail is called when the store could not write an entry.
	OnFail func(event string, err error)
}

// Writer appends a run's events to its audit chain: the one way an entry gets there (ADR 0014). The acceptance of a story is the one
// entry that is inserted by persistence, inside the transaction that accepts the story; its payload is still an event's (Prepare).
type Writer struct {
	store *persistence.Store
	runID int64
	set   Settings
}

// NewWriter is the writer of one run's chain.
func NewWriter(store *persistence.Store, runID int64, s Settings) *Writer {
	return &Writer{store: store, runID: runID, set: s}
}

// Append writes e as the run's next entry.
func (w *Writer) Append(e Event) error {
	_, err := w.append(e, nil)
	return err
}

// AppendHash is Append, and returns the new entry's hash: the entry a later one names.
func (w *Writer) AppendHash(e Event) (string, error) { return w.append(e, nil) }

// AppendIf is Append, but proceed runs under the chain's append lock, right before the insert; an error from it writes nothing and
// is returned unchanged.
func (w *Writer) AppendIf(e Event, proceed func() error) error {
	_, err := w.append(e, proceed)
	return err
}

func (w *Writer) append(e Event, proceed func() error) (string, error) {
	name := e.eventName()
	var payload string
	var raw []byte
	if w.set.Plain {
		b, err := json.Marshal(e)
		if err != nil {
			obs.Log.Warn("audit marshal", "event", name, "err", err)
			return "", fmt.Errorf("the payload of %s: %w", name, err)
		}
		raw, payload = b, string(b)
	} else {
		p, err := Encode(name, e)
		if err != nil {
			return "", fmt.Errorf("the payload of %s: %w", name, err)
		}
		raw, payload = []byte(p), p
	}
	if w.set.Scrub != nil {
		payload = string(w.set.Scrub(raw))
	}
	if w.set.Cap > 0 && len(payload) > w.set.Cap { // a cut payload would be neither JSON nor, perhaps, UTF-8: say what was dropped
		sum := sha256.Sum256([]byte(payload))
		var kind struct {
			ActionType string `json:"action_type"`
		}
		_ = json.Unmarshal(raw, &kind) // the kind of request is kept: the decision that follows is matched to its request by it
		payload = fmt.Sprintf(`{"truncated":true,"action_type":%q,"bytes":%d,"sha256":%q}`, kind.ActionType, len(payload), hex.EncodeToString(sum[:]))
	}
	if w.set.Debug != nil {
		_, _ = fmt.Fprintf(w.set.Debug, "%s %s %s\n", time.Now().UTC().Format(time.RFC3339Nano), name, payload)
	}
	return w.insert(name, payload, proceed)
}

func (w *Writer) insert(name, payload string, proceed func() error) (string, error) {
	var refused error
	log, err := w.store.AppendEventLogChainedIf(w.runID, name, payload, "", func() error {
		if proceed != nil {
			refused = proceed()
		}
		return refused
	})
	if err != nil {
		if refused != nil { // the caller said not to write: not a failure of the store
			return "", err
		}
		obs.Log.Warn("append audit event", "event", name, "err", err)
		if w.set.OnFail != nil {
			w.set.OnFail(name, err)
		}
		return "", err
	}
	obs.AuditChainLength.Inc()
	return log.EventHash, nil
}

// Prepare is the entry for e, for the one write that cannot go through a Writer: the entry that has to be inserted in the same
// transaction as the change it records (the acceptance of a story). It is shaped and sized as the Writer would, and the caller
// inserts it with the chain's own hash.
func Prepare(e Event) (persistence.AuditEntry, error) {
	name := e.eventName()
	payload, err := Encode(name, e)
	if err != nil {
		return persistence.AuditEntry{}, fmt.Errorf("the payload of %s: %w", name, err)
	}
	if err := chain.CheckSize(name, payload); err != nil {
		return persistence.AuditEntry{}, err
	}
	return persistence.AuditEntry{Type: name, Payload: payload}, nil
}
