package orchestrator

import (
	"fmt"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// GitRepo wraps a go-git repository and worktree, providing the small set of
// git operations the orchestrator needs without shelling out to the system git
// binary (CHECK 5.3.1).
//
// Exception: git stash push / stash pop have no go-git v5 equivalent (the stash
// API is read-only). Those two operations are kept as exec.Command calls in
// handleDirtyTree and the deferred stash-pop in Run().
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
	r, err := gogit.PlainOpen(path)
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
	// Detached HEAD — return the full commit SHA so checkout can restore it.
	return head.Hash().String(), nil
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
		// Looks like a commit SHA — restore detached HEAD.
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
	// error if no config entry exists — safe to ignore).
	_ = g.r.DeleteBranch(name)
	// RemoveReference removes the actual ref pointer (this is what makes the
	// branch disappear from `git branch -a`).
	return g.r.Storer.RemoveReference(plumbing.NewBranchReferenceName(name))
}

// AddAll stages all changes (tracked modifications, deletions, and new files)
// in the worktree. Equivalent to `git add .` / `git add -A`.
func (g *GitRepo) AddAll() error {
	return g.w.AddWithOptions(&gogit.AddOptions{All: true})
}

// AddFiles stages only the named paths. Paths should be relative to the
// repository root. An empty slice is a no-op. Returns the first error
// encountered; remaining paths are not staged when an error occurs.
func (g *GitRepo) AddFiles(paths []string) error {
	for _, p := range paths {
		if _, err := g.w.Add(p); err != nil {
			return fmt.Errorf("git add %q: %w", p, err)
		}
	}
	return nil
}

// Commit creates a new commit with the given message. The author name and email
// are read from the global git config; if absent "staircase" / "staircase@local"
// are used as fallbacks. Returns the new commit's full SHA.
func (g *GitRepo) Commit(msg string) (string, error) {
	cfg, _ := g.r.ConfigScoped(config.GlobalScope)
	name, email := cfg.User.Name, cfg.User.Email
	if name == "" {
		name = "staircase"
	}
	if email == "" {
		email = "staircase@local"
	}
	hash, err := g.w.Commit(msg, &gogit.CommitOptions{
		Author: &object.Signature{Name: name, Email: email, When: time.Now()},
	})
	if err != nil {
		return "", err
	}
	return hash.String(), nil
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
