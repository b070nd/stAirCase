package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/b070nd/stAirCase/src/internal/wslock"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// GitRepo wraps a go-git repository and worktree, providing the small set of
// git operations the orchestrator needs without shelling out to the system git
// binary (CHECK 5.3.1).
//
// Exception: go-git v5 has no worktree API, so addWorktree/removeWorktree
// shell out to git.
type GitRepo struct {
	r    *gogit.Repository
	w    *gogit.Worktree
	path string
}

// OpenGitRepo opens the git repository rooted at path. Returns an error if
// path is empty, is not a git repository, or the worktree cannot be obtained
// (e.g. bare repo).
func OpenGitRepo(path string) (*GitRepo, error) {
	if path == "" {
		return nil, fmt.Errorf("no source path configured")
	}
	// EnableDotGitCommonDir makes go-git follow a linked worktree's commondir to
	// the shared objects and refs; without it, commits made in a run's worktree
	// never reach the run branch. It is a no-op for ordinary repositories.
	r, err := gogit.PlainOpenWithOptions(path, &gogit.PlainOpenOptions{EnableDotGitCommonDir: true})
	if err != nil {
		return nil, fmt.Errorf("open git repo %s: %w", path, err)
	}
	w, err := r.Worktree()
	if err != nil {
		return nil, fmt.Errorf("git worktree %s: %w", path, err)
	}
	return &GitRepo{r: r, w: w, path: path}, nil
}

// CurrentBranch returns the short name of the currently checked-out branch.
// If HEAD is detached it returns the full commit SHA instead, matching the
// behaviour of `git rev-parse --abbrev-ref HEAD` + fallback to `git rev-parse HEAD`.
func (g *GitRepo) CurrentBranch() (string, error) {
	head, err := g.r.Head()
	if err != nil {
		return "", fmt.Errorf("git head: %w", err)
	}
	if head.Name().IsBranch() {
		return head.Name().Short(), nil
	}
	// Detached HEAD - return the full commit SHA so checkout can restore it.
	return head.Hash().String(), nil
}

// HeadSHA returns the commit HEAD points at.
func (g *GitRepo) HeadSHA() (string, error) {
	head, err := g.r.Head()
	if err != nil {
		return "", fmt.Errorf("git head: %w", err)
	}
	return head.Hash().String(), nil
}

// addWorktree creates branch at base and checks it out in a new linked
// worktree at path, leaving repo's own checkout untouched. go-git has no
// worktree API, so this shells out like the other worktree commands.
func addWorktree(repo, path, branch, base string) error {
	return withWorktreeLock(repo, func() error {
		out, err := exec.Command("git", "-C", repo, "worktree", "add", "-b", branch, path, base).CombinedOutput()
		if err != nil {
			return fmt.Errorf("git worktree add: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	})
}

var worktreeMu sync.Mutex

// withWorktreeLock runs f with no other staircase creating or removing a
// worktree of repo. Git is not safe against two `worktree add` at once (one can
// fail reading another's half-written .git/worktrees/<name>/commondir), and
// sessions in one repository do start together. The lock is a mutex for this
// process and a flock on a file in the repository's git directory for others.
func withWorktreeLock(repo string, f func() error) error {
	worktreeMu.Lock()
	defer worktreeMu.Unlock()
	if out, err := exec.Command("git", "-C", repo, "rev-parse", "--path-format=absolute", "--git-common-dir").Output(); err == nil {
		if lf, err := os.OpenFile(filepath.Join(strings.TrimSpace(string(out)), "staircase-worktree.lock"), os.O_CREATE|os.O_RDWR, 0o600); err == nil {
			defer func() { _ = lf.Close() }()
			for deadline := time.Now().Add(30 * time.Second); wslock.LockExclusive(lf.Fd()) != nil && time.Now().Before(deadline); {
				time.Sleep(20 * time.Millisecond)
			}
			defer func() { _ = wslock.Unlock(lf.Fd()) }()
		}
	}
	return f()
}

// removeWorktree deletes a run's worktree (its branch is kept).
func removeWorktree(repo, path string) error {
	return withWorktreeLock(repo, func() error {
		out, err := exec.Command("git", "-C", repo, "worktree", "remove", "--force", path).CombinedOutput()
		if err != nil {
			return fmt.Errorf("git worktree remove: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	})
}

// BranchExists reports whether a local branch with the given name exists.
// Returns false on any error (missing branch, repo error, etc.).
func (g *GitRepo) BranchExists(name string) bool {
	_, err := g.r.Reference(plumbing.NewBranchReferenceName(name), false)
	return err == nil
}

// CreateBranch creates and checks out a new local branch from the current HEAD.
// Equivalent to `git checkout -b <name>`.
func (g *GitRepo) CreateBranch(name string) error {
	return g.w.Checkout(&gogit.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName(name),
		Create: true,
	})
}

// CheckoutBranch checks out an existing branch by name, or restores a detached
// HEAD by SHA (40-char hex string). Equivalent to `git checkout <name>`.
func (g *GitRepo) CheckoutBranch(nameOrSHA string) error {
	if len(nameOrSHA) == 40 {
		// Looks like a commit SHA - restore detached HEAD.
		return g.w.Checkout(&gogit.CheckoutOptions{
			Hash: plumbing.NewHash(nameOrSHA),
		})
	}
	return g.w.Checkout(&gogit.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName(nameOrSHA),
	})
}

// DeleteBranch deletes the local branch by removing both its branch config
// entry and its ref. Equivalent to `git branch -D <name>`.
func (g *GitRepo) DeleteBranch(name string) error {
	// DeleteBranch removes the [branch "<name>"] config section (may return an
	// error if no config entry exists - safe to ignore).
	_ = g.r.DeleteBranch(name)
	// RemoveReference removes the actual ref pointer (this is what makes the
	// branch disappear from `git branch -a`).
	return g.r.Storer.RemoveReference(plumbing.NewBranchReferenceName(name))
}

// identity is the author of staircase's commits: the global git user, else
// "staircase" <staircase@local>.
func (g *GitRepo) identity() (name, email string) {
	if cfg, err := g.r.ConfigScoped(config.GlobalScope); err == nil {
		name, email = cfg.User.Name, cfg.User.Email
	}
	if name == "" {
		name = "staircase"
	}
	if email == "" {
		email = "staircase@local"
	}
	return name, email
}

// IsClean reports whether the worktree has no uncommitted changes.
// Equivalent to checking whether `git status --porcelain` produces output.
func (g *GitRepo) IsClean() (bool, error) {
	status, err := g.w.Status()
	if err != nil {
		return false, err
	}
	return status.IsClean(), nil
}

// ListBranches returns all local branch names that start with prefix.
// Equivalent to `git for-each-ref --format=%(refname:short) refs/heads/<prefix>*`.
func (g *GitRepo) ListBranches(prefix string) ([]string, error) {
	refs, err := g.r.References()
	if err != nil {
		return nil, err
	}
	var branches []string
	_ = refs.ForEach(func(ref *plumbing.Reference) error {
		if ref.Name().IsBranch() && strings.HasPrefix(ref.Name().Short(), prefix) {
			branches = append(branches, ref.Name().Short())
		}
		return nil
	})

	return branches, nil
}

// RemoteURL returns the fetch URL of the named remote (typically "origin").
// Returns an error when the remote does not exist or has no URLs configured.
func (g *GitRepo) RemoteURL(name string) (string, error) {
	remote, err := g.r.Remote(name)
	if err != nil {
		return "", fmt.Errorf("git remote %q: %w", name, err)
	}
	urls := remote.Config().URLs
	if len(urls) == 0 {
		return "", fmt.Errorf("git remote %q has no URLs", name)
	}
	return urls[0], nil
}

// Push pushes branch to the named remote. When token is non-empty it is used
// as a GitHub-compatible Personal Access Token (BasicAuth with "x-token-auth"
// username). When token is empty the push uses unauthenticated transport
// (suitable for local remotes or repos with SSH keys configured in the OS).
func (g *GitRepo) Push(remoteName, branch, token string) error {
	refSpec := config.RefSpec(
		"refs/heads/" + branch + ":refs/heads/" + branch,
	)
	opts := &gogit.PushOptions{
		RemoteName: remoteName,
		RefSpecs:   []config.RefSpec{refSpec},
	}
	if token != "" {
		opts.Auth = &githttp.BasicAuth{Username: "x-token-auth", Password: token}
	}
	if err := g.r.Push(opts); err != nil && err != gogit.NoErrAlreadyUpToDate {
		return fmt.Errorf("git push %s %s: %w", remoteName, branch, err)
	}
	return nil
}
