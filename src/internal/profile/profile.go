// Package profile is what is true of a project for every run of it (a spec of the first real project): the paths a person must always
// decide, the paths a change may touch, the checks, the sandbox. It is plain data, kept in the workspace and never in the client's
// repository, normalized once, and resolved into the terms of a run, where flags override it. It holds no database and no git.
package profile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/engine"
	"github.com/b070nd/stAirCase/src/internal/sandbox"
)

// Profile is a project's standing settings. A field that is empty says nothing: the run's own flags, or the default, apply.
type Profile struct {
	// Protect are paths or patterns, from the repository's root, where a check is defined: a change to one always needs a person.
	Protect []string `json:"protect,omitempty"`
	// Sensitive are paths or patterns a change to which always needs a person, in addition to the built-in list; a profile can add
	// to that list and never take from it.
	Sensitive []string `json:"sensitive,omitempty"`
	// Scope are the paths a run's changes may touch; a change elsewhere comes to a person as drift.
	Scope []string `json:"scope,omitempty"`
	// Visible are directories or files of the user's checkout, relative to its root, that the repository ignores and the checks need
	// (composer's vendor/): checks see them read-only (the project's environment). Literal paths, never patterns.
	Visible []string `json:"visible,omitempty"`
	// Checks are commands, such as the tests, run on the commit a run makes.
	Checks []string `json:"checks,omitempty"`
	// Sandbox is where approved commands and checks run: auto, required, off or docker:...
	Sandbox string `json:"sandbox,omitempty"`
}

// Fields are the names of a profile's fields, as the commands say them.
var Fields = []string{"protect", "sensitive", "scope", "visible", "checks", "sandbox"}

// Empty reports whether the profile says nothing.
func (p Profile) Empty() bool { return p.Digest() == Profile{}.Digest() }

// Normalize is p with every list trimmed, cleaned, sorted and without duplicates (the order of the checks is kept: it is the order
// they run in), refusing a path outside the repository, a pattern that is not well formed and a sandbox mode that does not exist.
func (p Profile) Normalize() (Profile, error) {
	var err error
	if p.Protect, err = paths("protect", p.Protect); err != nil {
		return p, err
	}
	if p.Sensitive, err = paths("sensitive", p.Sensitive); err != nil {
		return p, err
	}
	if p.Scope, err = paths("scope", p.Scope); err != nil {
		return p, err
	}
	if p.Visible, err = literalPaths("visible", p.Visible); err != nil {
		return p, err
	}
	var checks []string
	for _, c := range p.Checks {
		if c = strings.TrimSpace(c); c != "" && !slices.Contains(checks, c) {
			checks = append(checks, c)
		}
	}
	p.Checks = checks
	if p.Sandbox = strings.TrimSpace(p.Sandbox); p.Sandbox != "" {
		if err := sandbox.ParseMode(p.Sandbox); err != nil {
			return p, fmt.Errorf("sandbox: %w", err)
		}
	}
	return p, nil
}

func paths(field string, in []string) ([]string, error) {
	var out []string
	for _, raw := range in {
		clean, ok := engine.CleanRepoPath(strings.TrimSpace(raw))
		if !ok {
			return nil, fmt.Errorf("%s %q: name a path or pattern inside the repository, from its root", field, raw)
		}
		if !engine.ValidGlob(clean) {
			return nil, fmt.Errorf("%s %q is not a well formed pattern", field, raw)
		}
		out = append(out, clean)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// literalPaths is paths that name one directory or file each: cleaned, inside the repository, no pattern, not the git directory.
func literalPaths(field string, in []string) ([]string, error) {
	var out []string
	for _, raw := range in {
		clean, ok := engine.CleanRepoPath(strings.TrimSpace(raw))
		if !ok {
			return nil, fmt.Errorf("%s %q: name a path inside the repository, from its root", field, raw)
		}
		if strings.ContainsAny(clean, "*?[") {
			return nil, fmt.Errorf("%s %q names a directory or a file, not a pattern", field, raw)
		}
		if clean == ".git" || strings.HasPrefix(clean, ".git/") {
			return nil, fmt.Errorf("%s %q: the git directory is not an environment", field, raw)
		}
		out = append(out, clean)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// Digest is the profile's digest: of its normalized JSON, which is what a run binds when it starts under it.
func (p Profile) Digest() string {
	b, _ := json.Marshal(p)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Path is where a project's profile is kept in the workspace.
func Path(wsDir string, projectID int64) string {
	return filepath.Join(wsDir, "profiles", fmt.Sprintf("project-%d.json", projectID))
}

// Load reads a project's profile. A project with none has the empty profile and found is false; one that cannot be read is an error,
// never the empty profile: a run must not start under less than its project asks.
func Load(wsDir string, projectID int64) (p Profile, found bool, err error) {
	b, err := os.ReadFile(Path(wsDir, projectID))
	if errors.Is(err, os.ErrNotExist) {
		return Profile{}, false, nil
	}
	if err != nil {
		return Profile{}, false, fmt.Errorf("the profile of project %d: %w", projectID, err)
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields() // a misspelt field would silently drop a protection
	if err := dec.Decode(&p); err != nil {
		return Profile{}, false, fmt.Errorf("the profile of project %d is not readable: %w", projectID, err)
	}
	if p, err = p.Normalize(); err != nil {
		return Profile{}, false, fmt.Errorf("the profile of project %d: %w", projectID, err)
	}
	return p, true, nil
}

// Save writes a project's normalized profile; an empty one removes the file, so a project with none has none.
func Save(wsDir string, projectID int64, p Profile) error {
	p, err := p.Normalize()
	if err != nil {
		return err
	}
	file := Path(wsDir, projectID)
	if p.Empty() {
		if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}
