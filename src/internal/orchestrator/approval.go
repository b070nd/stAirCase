package orchestrator

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/b070nd/staircase-core/src/internal/domain"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// maxApprovedFileBytes caps a created file: the whole proposal must fit the
// IPC line limit (256 KiB) and stay reviewable by a human.
const maxApprovedFileBytes = 200 << 10

// search_block markers for whole-file operations.
const (
	MarkerNewFile    = "(new file)"    // replace_block is the complete new content
	MarkerDeleteFile = "(delete file)" // the file is removed
)

// approvedFile is what the operator approved for one path.
type approvedFile struct {
	content []byte
	mode    os.FileMode // permissions to commit: 0644 or 0755
	deleted bool
}

// approvals is the trusted record of what a run's approvals mean, byte for
// byte. Nothing the runtime claims (content_hash, what it wrote) is trusted:
// every approved state is derived here from the base commit and exactly the
// edits the operator was shown. Finalize checks the worktree against it
// (verify) and then commits the approved state itself (commit).
type approvals struct {
	repo  *GitRepo // the run's worktree
	base  *object.Commit
	files map[string]*approvedFile // cleaned slash path → approved state
}

func newApprovals(repo *GitRepo, baseSHA string) (*approvals, error) {
	c, err := repo.r.CommitObject(plumbing.NewHash(baseSHA))
	if err != nil {
		return nil, fmt.Errorf("load base commit %s: %w", baseSHA, err)
	}
	return &approvals{repo: repo, base: c, files: map[string]*approvedFile{}}, nil
}

// errBadPath marks a refusal caused by the proposed path itself.
var errBadPath = errors.New("invalid path")

// derive computes the state that approving edits would produce, without
// recording it. An error means the proposal cannot be approved as shown.
func (a *approvals) derive(edits []domain.ProposedEdit) (map[string]*approvedFile, error) {
	next := map[string]*approvedFile{}
	current := func(p string) (*approvedFile, error) {
		if f, ok := next[p]; ok {
			return f, nil
		}
		if f, ok := a.files[p]; ok {
			return f, nil
		}
		return a.fromBase(p)
	}
	for _, e := range edits {
		p, err := cleanApprovedPath(a.repo.path, e.File)
		if err != nil {
			return nil, err
		}
		switch e.SearchBlock {
		case MarkerNewFile:
			if len(e.ReplaceBlock) > maxApprovedFileBytes {
				return nil, fmt.Errorf("%s: new file is %d bytes; at most %d can be approved at once", p, len(e.ReplaceBlock), maxApprovedFileBytes)
			}
			mode := os.FileMode(0o644)
			if f, err := current(p); err != nil {
				return nil, err
			} else if f != nil && !f.deleted {
				mode = f.mode // overwriting keeps the file's mode
			}
			next[p] = &approvedFile{content: []byte(e.ReplaceBlock), mode: mode}
		case MarkerDeleteFile:
			f, err := current(p)
			if err != nil {
				return nil, err
			}
			if f == nil || f.deleted {
				return nil, fmt.Errorf("%s: cannot delete a file that does not exist", p)
			}
			next[p] = &approvedFile{deleted: true}
		default:
			f, err := current(p)
			if err != nil {
				return nil, err
			}
			if f == nil || f.deleted {
				return nil, fmt.Errorf("%s: cannot edit a file that does not exist", p)
			}
			// Edits apply to the file's text with universal newlines (\r\n and a
			// lone \r read as \n); only \r\n is normalized in the blocks.
			src := universalNewlines(f.content)
			search := []byte(strings.ReplaceAll(e.SearchBlock, "\r\n", "\n"))
			if !bytes.Contains(src, search) {
				return nil, fmt.Errorf("%s: search_block not found in the approved content", p)
			}
			replace := []byte(strings.ReplaceAll(e.ReplaceBlock, "\r\n", "\n"))
			next[p] = &approvedFile{content: bytes.Replace(src, search, replace, 1), mode: f.mode}
		}
	}
	return next, nil
}

// current is a path's approved state so far: approved in this run, or as in
// the base commit (nil when absent).
func (a *approvals) current(p string) (*approvedFile, error) {
	if f, ok := a.files[p]; ok {
		return f, nil
	}
	return a.fromBase(p)
}

// record adopts a derived state once the decision is on the audit chain.
func (a *approvals) record(next map[string]*approvedFile) {
	for p, f := range next {
		a.files[p] = f
	}
}

// fromBase returns a path's state at the base commit, or nil if absent.
func (a *approvals) fromBase(p string) (*approvedFile, error) {
	f, err := a.base.File(p)
	if errors.Is(err, object.ErrFileNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: read base: %w", p, err)
	}
	if f.Mode != filemode.Regular && f.Mode != filemode.Executable {
		return nil, fmt.Errorf("%s: %w: not a regular file in the base commit", p, errBadPath)
	}
	content, err := f.Contents()
	if err != nil {
		return nil, fmt.Errorf("%s: read base: %w", p, err)
	}
	mode := os.FileMode(0o644)
	if f.Mode == filemode.Executable {
		mode = 0o755
	}
	return &approvedFile{content: []byte(content), mode: mode}, nil
}

// violation is a reason finalize refuses to commit, recorded as an audit event.
type violation struct{ event, file, detail string }

// verify compares the worktree with the approved state: HEAD is still the
// base commit (the agent made no commits of its own), every approved path
// holds exactly its approved bytes (or is gone), and nothing else changed —
// in the working tree or the index.
func (a *approvals) verify() ([]violation, error) {
	var out []violation
	head, err := a.repo.HeadSHA()
	if err != nil {
		head = "unreadable (" + err.Error() + ")"
	}
	if head != a.base.Hash.String() {
		out = append(out, violation{"run_branch_moved", "", fmt.Sprintf("HEAD is %s, not the base commit %s", head, a.base.Hash)})
	}
	for p, f := range a.files {
		if _, err := cleanApprovedPath(a.repo.path, p); err != nil { // swapped for a dir or symlink
			out = append(out, violation{"approval_path_escape", p, err.Error()})
			continue
		}
		full := filepath.Join(a.repo.path, filepath.FromSlash(p))
		info, err := os.Lstat(full)
		switch {
		case f.deleted:
			if err == nil {
				out = append(out, violation{"approval_content_mismatch", p, "deletion was approved but the file exists"})
			}
		case err != nil:
			out = append(out, violation{"approval_content_mismatch", p, "approved file is missing"})
		case !info.Mode().IsRegular():
			out = append(out, violation{"approval_content_mismatch", p, "approved path is not a regular file"})
		default:
			got, err := os.ReadFile(full)
			if err != nil || !bytes.Equal(got, f.content) {
				out = append(out, violation{"approval_content_mismatch", p,
					fmt.Sprintf("approved sha256 %s, found %s", sha256Hex(f.content), sha256Hex(got))})
			}
		}
	}
	st, err := a.repo.w.Status()
	if err != nil {
		return nil, fmt.Errorf("verify approvals: read worktree status: %w", err)
	}
	for p, s := range st {
		if s.Staging == gogit.Unmodified && s.Worktree == gogit.Unmodified {
			continue
		}
		if _, ok := a.files[p]; !ok {
			out = append(out, violation{"unapproved_worktree_change", p, fmt.Sprintf("index %q, worktree %q", s.Staging, s.Worktree)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].file < out[j].file })
	return out, nil
}

// commit records the approved state as one commit on branch, built from the
// base tree and the approved bytes held in memory. Nothing is read back from
// the worktree or its index, so a change made after verify cannot reach the
// commit, and the branch moves only if it still points at the base commit.
// Returns "" when the approved state equals the base.
func (a *approvals) commit(branch, msg string) (string, error) {
	tmp, err := os.MkdirTemp("", "staircase-commit-")
	if err != nil {
		return "", fmt.Errorf("commit approvals: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	name, email := a.repo.identity()
	env := append(os.Environ(), "GIT_INDEX_FILE="+filepath.Join(tmp, "index"), // private index
		"GIT_AUTHOR_NAME="+name, "GIT_AUTHOR_EMAIL="+email,
		"GIT_COMMITTER_NAME="+name, "GIT_COMMITTER_EMAIL="+email)
	git := func(stdin []byte, args ...string) (string, error) {
		// No hooks: a hook written by the agent must not run as the orchestrator.
		cmd := exec.Command("git", append([]string{"-C", a.repo.path, "-c", "core.hooksPath=/dev/null"}, args...)...)
		var stderr bytes.Buffer
		cmd.Env, cmd.Stdin, cmd.Stderr = env, bytes.NewReader(stdin), &stderr
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("commit approvals: git %s: %w: %s", args[0], err, bytes.TrimSpace(stderr.Bytes()))
		}
		return strings.TrimSpace(string(out)), nil
	}
	base := a.base.Hash.String()
	if _, err := git(nil, "read-tree", base); err != nil {
		return "", err
	}
	paths := make([]string, 0, len(a.files))
	for p := range a.files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		f := a.files[p]
		if f.deleted {
			if _, err := git(nil, "update-index", "--force-remove", "--", p); err != nil {
				return "", err
			}
			continue
		}
		blob, err := git(f.content, "hash-object", "-w", "--stdin")
		if err != nil {
			return "", err
		}
		mode := "100644"
		if f.mode&0o111 != 0 {
			mode = "100755"
		}
		if _, err := git(nil, "update-index", "--add", "--cacheinfo", mode, blob, p); err != nil {
			return "", err
		}
	}
	tree, err := git(nil, "write-tree")
	if err != nil || tree == a.base.TreeHash.String() {
		return "", err
	}
	c, err := git([]byte(msg), "commit-tree", "--no-gpg-sign", tree, "-p", base)
	if err != nil {
		return "", err
	}
	if _, err := git(nil, "update-ref", "-m", "staircase: approved changes", "refs/heads/"+branch, c, base); err != nil {
		return "", err
	}
	return c, nil
}

// digest summarizes approved states for the audit record.
func digest(files map[string]*approvedFile) map[string]string {
	d := make(map[string]string, len(files))
	for p, f := range files {
		if f.deleted {
			d[p] = "deleted"
		} else {
			d[p] = sha256Hex(f.content)
		}
	}
	return d
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func universalNewlines(b []byte) []byte {
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	return bytes.ReplaceAll(b, []byte("\r"), []byte("\n"))
}

// cleanApprovedPath validates a runtime-supplied path at the trust boundary
// and returns it cleaned (relative, slash-separated). It refuses anything but
// a plain file path inside root: empty, absolute, escaping "..", root itself,
// any .git component (repository internals, case-insensitively for macOS),
// and paths that pass through a symlink or name a directory.
func cleanApprovedPath(root, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("%w %q: must be relative to the project root", errBadPath, rel)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w %q: outside the project root", errBadPath, rel)
	}
	parts := strings.Split(clean, "/")
	for _, part := range parts {
		if strings.EqualFold(part, ".git") {
			return "", fmt.Errorf("%w %q: repository internals", errBadPath, rel)
		}
	}
	p := root
	for i, part := range parts {
		p = filepath.Join(p, part)
		info, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			break // the rest is new
		}
		if err != nil {
			return "", fmt.Errorf("%w %q: %v", errBadPath, rel, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%w %q: goes through a symlink", errBadPath, rel)
		}
		if i == len(parts)-1 && info.IsDir() {
			return "", fmt.Errorf("%w %q: is a directory", errBadPath, rel)
		}
	}
	return clean, nil
}
