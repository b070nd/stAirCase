package orchestrator

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// The read boundary (a spec of the first real project): paths the project keeps out of the agent's reach, such as a tracked Composer
// auth.json. An agent that can be controlled enforces them where it reads; an agent that cannot (Codex, Gemini CLI, OpenCode, whose
// sandboxes restrict writes, not reads) is refused at the start of the run rather than pretend (NewTerms).

// reach is what AgentEnv remembers of the files below the worktree that are out of reach: found once, at the first question.
type reach struct {
	once  sync.Once
	files []string
}

// OutOfReach says whether the project keeps rel, a path relative to the worktree with slashes, out of the agent's reach.
func (e *AgentEnv) OutOfReach(rel string) bool {
	return len(e.Unreadable) > 0 && matchesFold(e.Unreadable, strings.TrimPrefix(rel, "./"))
}

// HoldsOutOfReach says whether a file below dir (relative to the worktree; "" or "." is the whole worktree) is out of reach: what a
// search from dir would read. Files the agent makes later are not counted: it cannot read or change a path that is out of reach.
func (e *AgentEnv) HoldsOutOfReach(dir string) bool {
	if len(e.Unreadable) == 0 {
		return false
	}
	e.reach.once.Do(func() {
		_ = filepath.WalkDir(e.Worktree, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, rerr := filepath.Rel(e.Worktree, p)
			switch {
			case rerr != nil || rel == ".":
			case d.IsDir() && rel == ".git":
				return fs.SkipDir
			case !d.IsDir() && e.OutOfReach(filepath.ToSlash(rel)):
				e.reach.files = append(e.reach.files, filepath.ToSlash(rel))
			}
			return nil
		})
		slices.Sort(e.reach.files)
	})
	dir = strings.Trim(filepath.ToSlash(dir), "/")
	if dir == "" || dir == "." {
		return len(e.reach.files) > 0
	}
	return slices.ContainsFunc(e.reach.files, func(f string) bool { return strings.HasPrefix(f, dir+"/") })
}

// HoldsOutOfReachDir is HoldsOutOfReach for a directory that may be in the agent's tool arguments as an absolute path under the worktree.
func (e *AgentEnv) HoldsOutOfReachDir(abs string) bool {
	rel, err := filepath.Rel(e.Worktree, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return false
	}
	return e.HoldsOutOfReach(rel)
}

// unreadableRefusal says why a harness cannot be started on a project that keeps paths out of the agent's reach, or "" when it can.
func (t Terms) unreadableRefusal(harness string) string {
	if len(t.Unreadable) == 0 {
		return ""
	}
	name := map[string]string{"codex": "Codex", "gemini": "Gemini CLI", "opencode": "OpenCode"}[harness]
	if name == "" {
		return ""
	}
	return name + " cannot be stopped from reading what this project keeps out of the agent's reach (" + strings.Join(t.Unreadable, ", ") +
		"): its own sandbox restricts what it writes, not what it reads, so the files would reach the model provider. Use Claude Code or the built-in " +
		"runtime for this project, or take the paths out of reach of the run first (staircase project profile set <id> --clear unreadable)"
}
