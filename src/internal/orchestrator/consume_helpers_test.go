package orchestrator_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/stretchr/testify/require"
)

func journalCount(t *testing.T, r runtest.Result) int {
	b, err := os.ReadFile(filepath.Join(r.WsDir, "journal", "run-1.approved.jsonl"))
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	return strings.Count(string(b), "\n")
}
