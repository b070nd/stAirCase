// Package obs provides structured logging with automatic redaction of sensitive
// fields (CHECK 10.1.2).
//
// Usage:
//
//	obs.Log.Warn("stash pop failed", "err", err)
//	obs.InitJSON(os.Stderr, slog.LevelInfo) // replace default discard handler
package obs

import (
	"context"
	"io"
	"log/slog"
	"strings"
)

// sensitiveKeys is the set of lowercase attribute key names that are redacted.
var sensitiveKeys = map[string]bool{
	"token":           true,
	"plaintext":       true,
	"plaintext_value": true,
	"password":        true,
	"secret":          true,
	"authorization":   true,
	"key":             true,
}

// Log is the package-level structured logger.  It wraps a [RedactingHandler]
// that discards output by default.  Call [InitJSON] to write JSON to a writer.
var Log = slog.New(NewRedactingHandler(slog.NewTextHandler(io.Discard, nil)))

// InitJSON reconfigures [Log] to write JSON lines to w at the given minimum level.
func InitJSON(w io.Writer, level slog.Level) {
	Log = slog.New(NewRedactingHandler(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})))
}

// RedactingHandler wraps a [slog.Handler] and replaces the values of any
// [slog.Attr] whose key matches a sensitive field name with the literal string
// "<REDACTED>" (CHECK 10.1.2).
type RedactingHandler struct {
	inner slog.Handler
}

// NewRedactingHandler returns a [RedactingHandler] that delegates to inner
// after redacting sensitive attributes.
func NewRedactingHandler(inner slog.Handler) *RedactingHandler {
	return &RedactingHandler{inner: inner}
}

// Enabled delegates to the inner handler.
func (h *RedactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle clones the record, redacts sensitive attrs, then delegates to inner.
func (h *RedactingHandler) Handle(ctx context.Context, r slog.Record) error {
	clean := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		clean.AddAttrs(redactAttr(a))
		return true
	})
	return h.inner.Handle(ctx, clean)
}

// WithAttrs returns a new handler with the given attrs pre-attached (redacted).
func (h *RedactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cleaned := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		cleaned[i] = redactAttr(a)
	}
	return &RedactingHandler{inner: h.inner.WithAttrs(cleaned)}
}

// WithGroup delegates to the inner handler unchanged.
func (h *RedactingHandler) WithGroup(name string) slog.Handler {
	return &RedactingHandler{inner: h.inner.WithGroup(name)}
}

// redactAttr replaces the value of a with "<REDACTED>" if its key is sensitive.
func redactAttr(a slog.Attr) slog.Attr {
	if sensitiveKeys[strings.ToLower(a.Key)] {
		return slog.String(a.Key, "<REDACTED>")
	}
	return a
}
