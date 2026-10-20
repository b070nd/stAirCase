package diag_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

// size is the length of the file at path, or 0 when there is none.
func size(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	return len(b)
}

// A log written to ten times its bound keeps the newest lines, and the file and its numbered previous file together never exceed the
// bound, markers included. The end says how often it rotated and how much it dropped.
func TestLog_written_to_ten_times_its_bound_keeps_the_newest_lines_within_the_bound(t *testing.T) {
	const bound = 4096
	path := filepath.Join(t.TempDir(), "x.log")
	l, err := diag.Open(path, bound, nil)
	require.NoError(t, err)
	const lines = 800 // about 50 bytes each: ten times the bound
	for i := range lines {
		_, err := l.Write([]byte(fmt.Sprintf("line %04d of ordinary diagnostic output\n", i)))
		require.NoError(t, err, "a full log never fails the writer")
		assert.LessOrEqual(t, size(t, path)+size(t, path+".1"), bound, "the pair is within the bound after every write")
	}
	require.NoError(t, l.Close())

	got := read(t, path)
	assert.LessOrEqual(t, size(t, path)+size(t, path+".1"), bound, "the pair is within the bound, markers included")
	assert.Contains(t, got, fmt.Sprintf("line %04d of", lines-1), "the newest line survives")
	prev, _ := os.ReadFile(path + ".1")
	assert.NotContains(t, got+string(prev), "line 0000 of", "the oldest output is what is dropped")
	assert.Regexp(t, `rotated \d+ times, dropped \d+ bytes`, got, "the end says how often and how much")
	assert.NotRegexp(t, `rotated 0 times`, got)
}

// Output is lost only as the whole of a previous file: what the markers count as dropped is what the replaced previous files held.
func TestLog_dropped_bytes_are_the_replaced_previous_files(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.log")
	l, err := diag.Open(path, 2048, nil)
	require.NoError(t, err)
	var written int
	for i := range 400 {
		line := fmt.Sprintf("line %04d of ordinary diagnostic output\n", i)
		written += len(line)
		_, _ = l.Write([]byte(line))
	}
	require.NoError(t, l.Close())

	m := regexp.MustCompile(`dropped (\d+) bytes in all`).FindStringSubmatch(read(t, path))
	require.NotNil(t, m, "the end says how much")
	dropped, err := strconv.Atoi(m[1])
	require.NoError(t, err)
	assert.Greater(t, dropped, 0)
	assert.GreaterOrEqual(t, dropped+size(t, path)+size(t, path+".1"), written, "every byte written is kept or counted as dropped")
}

// A secret is scrubbed wherever the rotation falls, even when the write that carries it is cut in two exactly where the file
// rotates: neither file holds the secret or either half of it.
func TestLog_scrubs_a_secret_split_across_a_rotation(t *testing.T) {
	const secret = "hunter2-token"
	for shift := range 160 { // every position of the secret against the rotation point
		path := filepath.Join(t.TempDir(), "x.log")
		l, err := diag.Open(path, 1024, secrets(secret))
		require.NoError(t, err)
		for i := range 4 {
			_, _ = l.Write([]byte(fmt.Sprintf("line %02d of ordinary output\n", i)))
		}
		// One line longer than a file holds, so that the rotation falls inside it, with the secret arriving in two writes.
		_, _ = l.Write([]byte(strings.Repeat("p", 150+shift) + secret[:6]))
		_, _ = l.Write([]byte(secret[6:] + strings.Repeat("q", 400) + "\n"))
		for i := range 12 {
			_, _ = l.Write([]byte(fmt.Sprintf("later %02d with %s inside\n", i, secret)))
		}
		require.NoError(t, l.Close())

		prev, _ := os.ReadFile(path + ".1")
		all := read(t, path) + string(prev)
		require.Contains(t, all, "<REDACTED>", "shift %d", shift)
		require.NotContains(t, all, "hunter2", "shift %d", shift)
		require.NotContains(t, all, "-token", "shift %d", shift)
	}
}

// Opening a log that is already at its bound rotates it, so a continuation's output is kept and the earlier output is still read
// from the previous file.
func TestLog_reopened_at_its_bound_is_rotated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.log")
	l, err := diag.Open(path, 2048, nil)
	require.NoError(t, err)
	for i := range 40 { // fills the first file of 1024 bytes and goes into a second
		_, _ = l.Write([]byte(fmt.Sprintf("first session line %02d xxxxxxxxxxxxxx\n", i)))
	}
	require.NoError(t, l.Close())

	// Make the file full the way a long session leaves it, then open it again.
	full := strings.Repeat("old output of the session that died\n", 40)
	require.NoError(t, os.WriteFile(path, []byte(full), 0o600))

	l, err = diag.Open(path, 2048, nil)
	require.NoError(t, err)
	assert.Contains(t, read(t, path+".1"), "old output of the session that died", "it is rotated on opening, before anything is written")
	assert.Less(t, size(t, path), 200, "and the log it continues in is fresh")
	_, _ = l.Write([]byte("the continuation's first line\n"))
	require.NoError(t, l.Close())

	assert.Contains(t, read(t, path), "the continuation's first line\n", "the continuation is not blind")
	assert.Contains(t, read(t, path+".1"), "old output of the session that died", "what was there is the previous file")
	assert.LessOrEqual(t, size(t, path)+size(t, path+".1"), 2048, "and the pair is within the bound")
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
