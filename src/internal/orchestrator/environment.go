package orchestrator

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/sandbox"
)

// visibleOverlays is a run's environment as the sandbox shows it to a check: for each declared path, where it is in the user's checkout
// and where it appears. It judges the declaration when the run starts, so a run is refused before it makes anything: a path that does
// not exist, one the repository tracks (it is in every checkout already; an environment is for what is ignored), and an engine that
// cannot keep it read-only.
func visibleOverlays(sourceRoot string, t Terms) ([]sandbox.Overlay, error) {
	if len(t.Visible) == 0 {
		return nil, nil
	}
	if sourceRoot == "" {
		return nil, errors.New("--visible needs the project's checkout: this project has no source path")
	}
	if why := sandbox.OverlayRefusal(orAuto(t.Sandbox)); why != "" {
		return nil, errors.New(why)
	}
	root, err := filepath.EvalSymlinks(sourceRoot)
	if err != nil {
		return nil, err
	}
	var out []sandbox.Overlay
	for _, p := range t.Visible {
		src := filepath.Join(root, filepath.FromSlash(p))
		resolved, err := filepath.EvalSymlinks(src)
		if err != nil {
			return nil, fmt.Errorf("the environment path %s does not exist in %s: declare what is there (ignored directories such as vendor/)", p, sourceRoot)
		}
		if tracked, _ := exec.Command("git", "-C", root, "ls-files", "--", p).Output(); strings.TrimSpace(string(tracked)) != "" {
			return nil, fmt.Errorf("the environment path %s is tracked by git, so it is in every checkout of a commit already: an environment is for what the repository ignores", p)
		}
		if _, err := os.Stat(resolved); err != nil {
			return nil, err
		}
		out = append(out, sandbox.Overlay{Source: resolved, Target: p})
	}
	return out, nil
}

// environmentNames are the declared paths an overlay set shows, for the record of a check.
func environmentNames(overlays []sandbox.Overlay) []string {
	var out []string
	for _, o := range overlays {
		out = append(out, o.Target)
	}
	return out
}
