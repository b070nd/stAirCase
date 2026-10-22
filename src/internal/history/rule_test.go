package history_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEvents_every_event_type_is_in_the_golden_test (ADR 0014): a type added to history without a line in the golden test's list has no
// sample in the fixture that it reproduces; a name in the list that no type carries is stale.
func TestEvents_every_event_type_is_in_the_golden_test(t *testing.T) {
	src, err := os.ReadFile("events.go")
	require.NoError(t, err)
	declared := map[string]bool{}
	for _, m := range regexp.MustCompile(`\) eventName\(\) string\s*\{\s*return "([a-z_]+)"`).FindAllStringSubmatch(string(src), -1) {
		declared[m[1]] = true
	}
	require.Greater(t, len(declared), 30)
	for name := range declared {
		_, listed := events[name]
		assert.True(t, listed, "the event %s has a type and no line in the golden test", name)
	}
	for name := range events {
		assert.True(t, declared[name], "the golden test lists %s and no type carries that name", name)
	}
}

// TestChain_is_written_through_the_writer (ADR 0014): no code outside persistence and history appends an entry itself. The writer is
// where the ceiling, the scrubbing and the encoding are decided.
func TestChain_is_written_through_the_writer(t *testing.T) {
	root := filepath.Join("..", "..")
	var offenders []string
	require.NoError(t, filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		slash := filepath.ToSlash(p)
		if strings.Contains(slash, "/internal/persistence/") || strings.Contains(slash, "/internal/history/") {
			return nil
		}
		b, _ := os.ReadFile(p)
		if regexp.MustCompile(`\.AppendEventLog(Chained(If)?)?\(`).Match(b) {
			offenders = append(offenders, slash)
		}
		return nil
	}))
	assert.Empty(t, offenders, "these append to the chain themselves")
}
