// Package changeset links runs of several repositories under one id. Each run stays one repository with its own worktree, chain and
// certificate; the set holds the member runs, the declared contract files and the acknowledgements a person recorded. It is a workspace
// file, like a profile, never in a client's repository. It holds no database and no git: Verify brings the facts of the runs.
package changeset

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stAirCase/src/internal/engine"
)

// Set is a change set. Its id is chosen by the person, so it can be written on a run before the run exists.
type Set struct {
	ID      string    `json:"id"`
	Created time.Time `json:"created"`
	// Members are the runs of the set, one per project, in the order the projects joined.
	Members []Member `json:"members,omitempty"`
	// Contracts are the files that more than one repository depends on (an API description, a schema): a change to one needs every
	// other member's acknowledgement.
	Contracts []Contract `json:"contracts,omitempty"`
	Acks      []Ack      `json:"acks,omitempty"`
}

// Member is a run of a project that belongs to the set.
type Member struct {
	Project int64 `json:"project"`
	Run     int64 `json:"run"`
}

// Contract is a file of one project, from the root of its repository.
type Contract struct {
	Project int64  `json:"project"`
	Path    string `json:"path"`
}

// Ack records that a person looked at a change to a contract and the project By accepts it. It is bound to the content (the git blob
// the change produced), so a later change to the file needs a new acknowledgement. Run is the run that made the change.
type Ack struct {
	Contract Contract  `json:"contract"`
	Blob     string    `json:"blob"`
	By       int64     `json:"by"`
	Run      int64     `json:"run"`
	Who      string    `json:"who,omitempty"`
	At       time.Time `json:"at"`
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ValidID says whether id can name a set: lower case letters, digits, dot, dash and underscore, at most 64 characters. It becomes a file
// name and is written in certificates, so it is plain.
func ValidID(id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("a change set's id is lower case letters, digits, . - _ (at most 64), starting with a letter or digit; %q is not", id)
	}
	return nil
}

// Path is where the set is kept in the workspace.
func Path(wsDir, id string) string { return filepath.Join(wsDir, "changesets", id+".json") }

// Create makes an empty set. A set that exists is refused: its id names the work and is on the certificates of the runs.
func Create(wsDir, id string, now time.Time) (Set, error) {
	if err := ValidID(id); err != nil {
		return Set{}, err
	}
	if _, err := os.Stat(Path(wsDir, id)); err == nil {
		return Set{}, fmt.Errorf("change set %q already exists: staircase changeset show %s", id, id)
	}
	s := Set{ID: id, Created: now.UTC().Truncate(time.Second)}
	return s, s.Save(wsDir)
}

// Load reads a set. One that is not there says how to make it; one that cannot be read is an error, never an empty set: an
// acknowledgement must not vanish.
func Load(wsDir, id string) (Set, error) {
	if err := ValidID(id); err != nil {
		return Set{}, err
	}
	b, err := os.ReadFile(Path(wsDir, id))
	if errors.Is(err, os.ErrNotExist) {
		return Set{}, fmt.Errorf("no change set %q: make it with staircase changeset create %s", id, id)
	}
	if err != nil {
		return Set{}, fmt.Errorf("change set %q: %w", id, err)
	}
	var s Set
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return Set{}, fmt.Errorf("change set %q is not readable: %w", id, err)
	}
	if s.ID != id {
		return Set{}, fmt.Errorf("the file of change set %q names %q", id, s.ID)
	}
	return s, nil
}

// List is the ids of the workspace's sets, sorted.
func List(wsDir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(wsDir, "changesets"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if id, ok := strings.CutSuffix(e.Name(), ".json"); ok && ValidID(id) == nil {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids, nil
}

// Save writes the set whole, through a temporary file, so a crash leaves the old one.
func (s Set) Save(wsDir string) error {
	file := Path(wsDir, s.ID)
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// AddRun makes run the project's run in the set. A project has one: a run added for a project that has another replaces it, and the
// replaced run is returned (0 when there was none, or the same run was there). Acknowledgements stay: they are of content, not of runs.
func (s *Set) AddRun(project, run int64) (replaced int64, err error) {
	if project <= 0 || run <= 0 {
		return 0, errors.New("a member is a run of a project: both ids are positive")
	}
	for i, m := range s.Members {
		if m.Project != project {
			continue
		}
		if m.Run != run {
			replaced = m.Run
		}
		s.Members[i].Run = run
		return replaced, nil
	}
	s.Members = append(s.Members, Member{Project: project, Run: run})
	return 0, nil
}

// Member is the project's member, when it has one.
func (s Set) Member(project int64) (Member, bool) {
	for _, m := range s.Members {
		if m.Project == project {
			return m, true
		}
	}
	return Member{}, false
}

// Declare makes a file a contract of the set. The path is literal and inside the repository.
func (s *Set) Declare(c Contract) error {
	clean, ok := engine.CleanRepoPath(strings.TrimSpace(c.Path))
	if !ok {
		return fmt.Errorf("contract %q: name a file inside the repository, from its root", c.Path)
	}
	if strings.ContainsAny(clean, "*?[") {
		return fmt.Errorf("contract %q names one file, not a pattern", c.Path)
	}
	if clean == ".git" || strings.HasPrefix(clean, ".git/") {
		return fmt.Errorf("contract %q: the git directory is not a contract", c.Path)
	}
	c.Path = clean
	if !slices.Contains(s.Contracts, c) {
		s.Contracts = append(s.Contracts, c)
	}
	return nil
}

// Declared says whether c is a contract of the set.
func (s Set) Declared(c Contract) bool { return slices.Contains(s.Contracts, c) }

// Acknowledge records that project by accepts the content blob of contract c, which run made. Only another member can: a project does not
// acknowledge its own change.
func (s *Set) Acknowledge(c Contract, by, run int64, blob, who string, now time.Time) error {
	if !s.Declared(c) {
		return fmt.Errorf("project %d's %s is not a contract of this set: declare it with staircase changeset contract", c.Project, c.Path)
	}
	if by == c.Project {
		return fmt.Errorf("project %d cannot acknowledge its own change to %s", by, c.Path)
	}
	if _, ok := s.Member(by); !ok {
		return fmt.Errorf("project %d has no run in this set: add one first", by)
	}
	if s.Acknowledged(c, by, blob) {
		return nil
	}
	s.Acks = append(s.Acks, Ack{Contract: c, Blob: blob, By: by, Run: run, Who: who, At: now.UTC().Truncate(time.Second)})
	return nil
}

// Acknowledged says whether project by accepted exactly this content of contract c.
func (s Set) Acknowledged(c Contract, by int64, blob string) bool {
	return slices.ContainsFunc(s.Acks, func(a Ack) bool { return a.Contract == c && a.By == by && a.Blob == blob })
}
