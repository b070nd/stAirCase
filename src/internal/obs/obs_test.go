package obs_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/b070nd/staircase-core/src/internal/obs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRedaction verifies that sensitive field values are replaced with
// "<REDACTED>" in the structured log output (CHECK 10.1.3).
func TestRedaction(t *testing.T) {
	var buf bytes.Buffer
	obs.InitJSON(&buf, slog.LevelDebug)

	obs.Log.Warn("ipc: secret delivered",
		slog.String("token", "super-secret-bootstrap-token"),
		slog.String("plaintext_value", "my-api-key-plaintext"),
		slog.String("key_name", "OPENAI_KEY"),
		slog.String("safe_field", "visible-value"),
	)

	output := buf.String()
	assert.NotContains(t, output, "super-secret-bootstrap-token", "token value must be redacted")
	assert.NotContains(t, output, "my-api-key-plaintext", "plaintext_value must be redacted")
	assert.Contains(t, output, "<REDACTED>", "redaction marker must appear at least once")
	assert.Contains(t, output, "visible-value", "non-sensitive fields must be preserved")
	assert.Contains(t, output, "ipc: secret delivered", "log message must be preserved")
}

// TestRedaction_safe_fields_pass_through verifies that ordinary fields are not
// affected by the redacting handler.
func TestRedaction_safe_fields_pass_through(t *testing.T) {
	var buf bytes.Buffer
	obs.InitJSON(&buf, slog.LevelDebug)

	obs.Log.Info("reconcile done",
		slog.Int64("run_id", 42),
		slog.String("status", "SUCCESS"),
	)

	output := buf.String()
	assert.Contains(t, output, "42")
	assert.Contains(t, output, "SUCCESS")
	assert.NotContains(t, output, "<REDACTED>")
}

// TestRedaction_case_insensitive verifies that "Token", "TOKEN" etc. are also redacted.
func TestRedaction_case_insensitive(t *testing.T) {
	var buf bytes.Buffer
	obs.InitJSON(&buf, slog.LevelDebug)

	obs.Log.Warn("test",
		slog.String("Token", "should-be-gone"),
		slog.String("PASSWORD", "also-gone"),
	)

	output := buf.String()
	assert.NotContains(t, output, "should-be-gone")
	assert.NotContains(t, output, "also-gone")
	// Check that the keys are still present but values are redacted.
	assert.True(t, strings.Count(output, "<REDACTED>") >= 2)
}

// TestWithAttrs_redacts_sensitive_pre_attached_attrs verifies that WithAttrs
// redacts sensitive fields attached via slog.With() (CHECK 10.1.2).
func TestWithAttrs_redacts_sensitive_pre_attached_attrs(t *testing.T) {
	var buf bytes.Buffer
	obs.InitJSON(&buf, slog.LevelDebug)

	// slog.With calls WithAttrs on the underlying handler.
	logger := obs.Log.With(slog.String("token", "secret-value"), slog.String("msg_id", "42"))
	logger.Info("delivery")

	output := buf.String()
	assert.NotContains(t, output, "secret-value", "token value must be redacted via WithAttrs")
	assert.Contains(t, output, "42", "non-sensitive pre-attached field must pass through")
	assert.Contains(t, output, "<REDACTED>")
}

// TestWithGroup_delegates verifies that WithGroup is forwarded to the inner
// handler (slog uses it for namespace prefixing).
func TestWithGroup_delegates(t *testing.T) {
	var buf bytes.Buffer
	obs.InitJSON(&buf, slog.LevelDebug)

	// slog.WithGroup calls WithGroup on the underlying handler.
	logger := obs.Log.WithGroup("grp")
	logger.Info("scoped", slog.String("key", "value"))

	output := buf.String()
	// The group prefix should appear in the JSON output.
	assert.Contains(t, output, "grp", "group name must appear in output after WithGroup")
}

// TestInitOTel_noop_when_no_endpoint verifies that InitOTel("") returns a
// no-op shutdown func and sets OTelEndpoint to "" (CHECK 10.3.1).
func TestInitOTel_noop_when_no_endpoint(t *testing.T) {
	shutdown := obs.InitOTel("")
	require.NotNil(t, shutdown, "shutdown func must not be nil")
	assert.Equal(t, "", obs.OTelEndpoint, "endpoint must remain empty")
	shutdown() // must not panic
}

// TestInitOTel_with_endpoint_sets_global verifies that InitOTel records the
// endpoint and returns a non-nil shutdown func (CHECK 10.3.1).
func TestInitOTel_with_endpoint_sets_global(t *testing.T) {
	t.Cleanup(func() { obs.InitOTel("") }) // reset after test
	shutdown := obs.InitOTel("localhost:4317")
	require.NotNil(t, shutdown)
	assert.Equal(t, "localhost:4317", obs.OTelEndpoint)
	shutdown()
}
