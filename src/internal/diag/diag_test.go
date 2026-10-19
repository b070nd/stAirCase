package diag_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/diag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func secrets(v ...string) func() []string { return func() []string { return v } }

func read(t *testing.T, path string) string {
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

// A log stops growing at its bound, whatever is written, and says it did: a marker where the output stopped and, at the end, how much.
func TestLog_stops_at_its_bound_and_says_so(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.log")
	l, err := diag.Open(path, 1024, nil)
	require.NoError(t, err)
	for range 200 {
		_, err := l.Write([]byte("0123456789 a line of ordinary diagnostic output\n"))
		require.NoError(t, err, "a full log never fails the writer")
	}
	require.NoError(t, l.Close())

	got := read(t, path)
	assert.LessOrEqual(t, len(got), 1024, "the file, markers included, is within the bound")
	assert.Contains(t, got, "bound of 1024 bytes", "the marker names the bound")
	assert.Contains(t, got, "dropped", "and says output was dropped")
	assert.Regexp(t, `dropped \d+ bytes in all`, got, "the end says how much")
}

// A log that is never filled carries no marker.
func TestLog_under_its_bound_is_left_as_written(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.log")
	l, err := diag.Open(path, 1024, nil)
	require.NoError(t, err)
	_, _ = l.Write([]byte("one line\n"))
	require.NoError(t, l.Close())
	assert.Equal(t, "one line\n", read(t, path))
}

// A log opened again (a resumed session appends to its log) counts what is already in it.
func TestLog_counts_what_an_earlier_open_wrote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.log")
	for range 3 {
		l, err := diag.Open(path, 1024, nil)
		require.NoError(t, err)
		_, _ = l.Write([]byte(strings.Repeat("x", 500) + "\n"))
		require.NoError(t, l.Close())
	}
	assert.LessOrEqual(t, len(read(t, path)), 1024)
}

// Delivered secrets never reach the file, even when the write that carries one is cut in two.
func TestLog_scrubs_delivered_secrets_across_writes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.log")
	l, err := diag.Open(path, 1<<20, secrets("hunter2-token"))
	require.NoError(t, err)
	_, _ = l.Write([]byte("token=hunter2"))
	_, _ = l.Write([]byte("-token and more\nsecond hunter2-token line\n"))
	_, _ = l.Write([]byte("an unfinished hunter2-token"))
	require.NoError(t, l.Close())

	got := read(t, path)
	assert.NotContains(t, got, "hunter2")
	assert.Contains(t, got, "token=<REDACTED> and more\n")
	assert.Contains(t, got, "an unfinished <REDACTED>", "what is left unfinished at the end is written, scrubbed")
}

// A very long line with no newline is not held without end, and a secret in it is still scrubbed.
func TestLog_scrubs_a_secret_in_an_endless_line(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.log")
	l, err := diag.Open(path, 1<<20, secrets("hunter2-token"))
	require.NoError(t, err)
	chunk := strings.Repeat("a", 1000)
	for i := range 200 {
		w := chunk
		if i == 100 {
			w = chunk[:995] + "hunter2-token"[:6]
		}
		if i == 101 {
			w = "hunter2-token"[6:] + chunk
		}
		_, _ = l.Write([]byte(w))
	}
	require.NoError(t, l.Close())
	assert.NotContains(t, read(t, path), "hunter2")
}

// Redirect sends what is printed to os.Stdout into the log, scrubbed and bounded, and gives os.Stdout back.
func TestLog_redirect_takes_os_stdout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.log")
	l, err := diag.Open(path, 1<<20, secrets("s3cret"))
	require.NoError(t, err)
	before := os.Stdout
	restore := l.Redirect()
	_, _ = os.Stdout.WriteString("progress s3cret here\n")
	restore()
	require.NoError(t, l.Close())
	assert.Same(t, before, os.Stdout)
	assert.Equal(t, "progress <REDACTED> here\n", read(t, path))
}
