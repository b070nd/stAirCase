package orchestrator

import (
	"github.com/b070nd/stAirCase/src/internal/history"
)

// SavedTerms reads the terms run runID saved when it started, checked against the digest its verified chain h names. No saved
// terms (a run of an older version) is os.ErrNotExist. `staircase policy test` uses it to apply a run's protected paths to its
// past decisions.
func SavedTerms(wsDir string, runID int64, h history.History) (Terms, error) {
	return loadTerms(wsDir, runID, h, h.Bound.Harness)
}

// PersonBar says why a file edit touching files would go to a person under these terms whatever the policy says, or "" when no
// bar reaches it: a path that holds a check's definition (protected), or a sensitive path. It is the rule the deciders apply
// before limits and the policy (decide.go), asked of a past proposal's recorded paths.
func (t Terms) PersonBar(files []string) string {
	for _, f := range files {
		if matchesFold(t.ProtectedPaths, f) {
			return "it changes " + f + ", which holds the definition of a check"
		}
	}
	if f := sensitive(files); f != "" {
		return "it changes " + f + ", a sensitive path"
	}
	return ""
}
