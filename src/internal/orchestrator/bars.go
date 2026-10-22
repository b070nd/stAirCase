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

// PersonDecides ends the reason a person is asked about a change the bars send to them. It is the one suffix of that sentence,
// shared by the deciders, `staircase policy test` and the refusals of delivery by policy.
const PersonDecides = ": a person decides, whatever the policy says"

// PersonBar says why a file edit touching files would go to a person under these terms whatever the policy says, or "" when no
// bar reaches it: a path that holds a check's definition (protected), or a sensitive path. It is the one wording of that reason:
// the deciders apply the two bars before limits and the policy (decide.go) with protectedBar and sensitiveBar, which this joins;
// `staircase policy test` asks it of a past proposal's recorded paths, and delivery by policy repeats it (condition 4).
func (t Terms) PersonBar(files []string) string {
	if why := protectedBar(t.ProtectedPaths, files); why != "" {
		return why
	}
	return sensitiveBar(files)
}

// protectedBar is why files reach a path that holds a check's definition, or "".
func protectedBar(protected, files []string) string {
	for _, f := range files {
		if matchesFold(protected, f) {
			return "it changes " + f + ", which holds the definition of a check"
		}
	}
	return ""
}

// sensitiveBar is why files reach a sensitive path, or "".
func sensitiveBar(files []string) string {
	if f := sensitive(files); f != "" {
		return "it changes " + f + ", a sensitive path"
	}
	return ""
}
