package changeset

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/persistence"
)

// Report is what verifying a set found. The set holds only when no member has a problem and the set itself has none.
type Report struct {
	Set      string
	Problems []string // of the set as a whole
	Members  []MemberReport
}

// MemberReport is what verifying one member run found.
type MemberReport struct {
	Member
	Name     string   // the project's name
	Outcome  string   // what the run's own verified history says
	CAL      int      // the change assurance level on its certificate, when it has one
	Changed  []string // the contracts of the project this run changed
	Problems []string
}

// OK says whether the whole set holds.
func (r Report) OK() bool {
	if len(r.Problems) > 0 {
		return false
	}
	for _, m := range r.Members {
		if len(m.Problems) > 0 {
			return false
		}
	}
	return true
}

// Verify checks every member against what the workspace knows and the repositories hold: the run was delivered (by its own verified
// history), its certificate is signed by the workspace key, is about the run's commit, reaches minCAL, records no failed check and names
// this set, and every contract the run changed was acknowledged, for exactly that content, by every other member. It changes nothing. A
// problem is a finding in the report; an error is a failure to look.
func Verify(store *persistence.Store, wsDir string, s Set, minCAL int) (Report, error) {
	r := Report{Set: s.ID}
	if len(s.Members) == 0 {
		r.Problems = append(r.Problems, "the set has no member run: add the runs with staircase changeset add")
		return r, nil
	}
	pub, err := crypto.LoadSigningPublicKey(wsDir)
	if err != nil {
		return r, fmt.Errorf("the workspace's signing key: %w", err)
	}
	names := map[int64]string{}
	for _, m := range s.Members {
		p, err := store.GetProject(m.Project)
		if err != nil || p == nil {
			return r, fmt.Errorf("project %d of the set does not exist", m.Project)
		}
		names[m.Project] = fmt.Sprintf("%s (#%d)", p.Name, p.ID)
	}
	for _, m := range s.Members {
		mr := MemberReport{Member: m, Name: names[m.Project]}
		mr.verify(store, wsDir, s, pub, minCAL, names)
		r.Members = append(r.Members, mr)
	}
	return r, nil
}

func (mr *MemberReport) fail(format string, args ...any) {
	mr.Problems = append(mr.Problems, fmt.Sprintf(format, args...))
}

func (mr *MemberReport) verify(store *persistence.Store, wsDir string, s Set, pub ed25519.PublicKey, minCAL int, names map[int64]string) {
	run, err := store.GetRun(mr.Run)
	if err != nil || run == nil {
		mr.fail("run #%d does not exist", mr.Run)
		return
	}
	if c, err := store.GetCase(run.CaseID); err != nil || c == nil || c.ProjectID != mr.Project {
		mr.fail("run #%d is not a run of project %s", mr.Run, mr.Name)
		return
	}
	h, err := history.Read(store, run.ID)
	if err != nil {
		mr.fail("run #%d: its history cannot be read: %v", run.ID, err) // unknown, never satisfied
		return
	}
	outcome, why := h.Outcome()
	mr.Outcome = string(outcome)
	if outcome != history.OutcomeDelivered {
		mr.fail("run #%d is not delivered: %s (%s)", run.ID, outcome, why)
		return
	}
	commit := run.GitCommitHash
	raw, err := os.ReadFile(filepath.Join(wsDir, "audit", fmt.Sprintf("run-%d.certificate.json", run.ID)))
	if err != nil {
		mr.fail("run #%d has no change certificate: %v", run.ID, err)
		return
	}
	var env certificate.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		mr.fail("run #%d: the change certificate is not valid JSON", run.ID)
		return
	}
	st, err := certificate.Open(env, pub)
	if err != nil {
		mr.fail("run #%d: the change certificate does not verify: %v", run.ID, err)
		return
	}
	p := st.Predicate
	mr.CAL = p.CAL
	if err := st.Accept(commit, p.CAL, minCAL); err != nil {
		mr.fail("run #%d: %v", run.ID, err)
	}
	switch p.ChangeSet {
	case s.ID:
	case "":
		mr.fail("run #%d: its certificate names no change set (start the run with --change-set %s)", run.ID, s.ID)
	default:
		mr.fail("run #%d: its certificate names change set %q, not %q", run.ID, p.ChangeSet, s.ID)
	}
	mr.contracts(store, s, p.BaseCommit, commit, names)
}

// contracts finds the contracts of the project that the run changed, and the acknowledgements that are missing.
func (mr *MemberReport) contracts(store *persistence.Store, s Set, base, commit string, names map[int64]string) {
	proj, err := store.GetProject(mr.Project)
	if err != nil || proj == nil {
		return
	}
	for _, c := range s.Contracts {
		if c.Project != mr.Project {
			continue
		}
		before, err := BlobAt(proj.SourcePath, base, c.Path)
		if err != nil {
			mr.fail("contract %s: %v", c.Path, err)
			continue
		}
		after, err := BlobAt(proj.SourcePath, commit, c.Path)
		if err != nil {
			mr.fail("contract %s: %v", c.Path, err)
			continue
		}
		if before == after {
			continue
		}
		mr.Changed = append(mr.Changed, c.Path)
		for _, other := range s.Members {
			if other.Project != mr.Project && !s.Acknowledged(c, other.Project, after) {
				mr.fail("%s changed the contract %s (run #%d) and %s has not acknowledged it: staircase changeset ack %s %d:%s --by %d",
					mr.Name, c.Path, mr.Run, names[other.Project], s.ID, c.Project, c.Path, other.Project)
			}
		}
	}
}

// BlobAt is the git blob id of path in commit of the repository at repo, "" when the commit has no such file.
func BlobAt(repo, commit, path string) (string, error) {
	if commit == "" {
		return "", fmt.Errorf("no commit to read it at")
	}
	out, err := exec.Command("git", "-C", repo, "ls-tree", commit, "--", path).Output()
	if err != nil {
		return "", fmt.Errorf("cannot read it at %.12s in %s: %w", commit, repo, err)
	}
	fields := strings.Fields(strings.SplitN(string(out), "\t", 2)[0])
	if len(fields) < 3 || fields[1] != "blob" {
		return "", nil
	}
	return fields[2], nil
}
