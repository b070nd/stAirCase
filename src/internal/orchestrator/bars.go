package orchestrator

import (
	"strings"

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

// PersonBar says why a file edit touching files would go to a person under these terms whatever the policy says, or "" when no bar
// reaches it: a path that holds a check's definition (protected), or a sensitive path. It is the one wording of that reason, used by
// the deciders (before limits and policy), by `staircase policy test` on a past proposal's paths, and by delivery by policy (condition 4).
func (t Terms) PersonBar(files []string) string {
	if why := protectedBar(t.ProtectedPaths, files); why != "" {
		return why
	}
	return sensitiveBar(t.SensitivePaths, files)
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

// sensitiveBar is why files reach a sensitive path, or "": one of the built-in list, or one the project added (extra).
func sensitiveBar(extra, files []string) string {
	if f := sensitive(files); f != "" {
		return "it changes " + f + ", a sensitive path"
	}
	for _, f := range files {
		if matchesFold(extra, f) {
			return "it changes " + f + ", a sensitive path of this project"
		}
	}
	return ""
}

// outsideScope is why files reach outside a run's scope (the paths its --allow or its project's profile name), or "" when scope is
// empty or every file is inside it. It comes to a person like drift from the stories' scope, and counts no violation.
func outsideScope(scope, files []string) string {
	if len(scope) == 0 {
		return ""
	}
	for _, f := range files {
		if !matchesFold(scope, f) {
			return "it changes " + f + ", outside this run's scope (" + strings.Join(scope, ", ") + ")"
		}
	}
	return ""
}
