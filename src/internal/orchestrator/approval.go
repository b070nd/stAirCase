package orchestrator

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/b070nd/stAirCase/src/internal/domain"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// maxApprovedFileBytes caps a created file: the whole proposal must fit the
// IPC line limit (256 KiB) and stay reviewable by a human.
const maxApprovedFileBytes = 200 << 10

// maxApprovedBinaryBytes bounds a file that is not text, which is decided by its
// size and digest and travels in the ledger as base64.
const maxApprovedBinaryBytes = 2 << 20

// search_block markers for whole-file operations.
const (
	MarkerNewFile    = "(new file)"    // replace_block is the complete new content
	MarkerDeleteFile = "(delete file)" // the file is removed
	MarkerShell      = "(shell)"       // a shell_exec proposal: file is the working directory, replace_block the command
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
		// The ledger is JSON, which cannot carry bytes that are not UTF-8: such a
		// change could be approved but never reproduced, and a person cannot read
		// it. Refuse it here, where a run and a rebuild derive (F94).
		if !utf8.ValidString(e.File) || !utf8.ValidString(e.SearchBlock) || !utf8.ValidString(e.ReplaceBlock) {
			return nil, fmt.Errorf("%q: the text is not valid UTF-8: a whole new file that is not text goes in content_b64", strings.ToValidUTF8(e.File, "?"))
		}
		p, err := cleanApprovedPath(a.repo.path, e.File)
		if err != nil {
			return nil, err
		}
		switch e.SearchBlock {
		case MarkerNewFile:
			content := []byte(e.ReplaceBlock)
			if e.ContentB64 != "" {
				var err error
				if content, err = binaryContent(p, e); err != nil {
					return nil, err
				}
			} else if len(e.ReplaceBlock) > maxApprovedFileBytes {
				return nil, fmt.Errorf("%s: new file is %d bytes; at most %d can be approved at once", p, len(e.ReplaceBlock), maxApprovedFileBytes)
			}
			mode := os.FileMode(0o644)
			if f, err := current(p); err != nil {
				return nil, err
			} else if f != nil && !f.deleted {
				mode = f.mode // overwriting keeps the file's mode
			}
			next[p] = &approvedFile{content: content, mode: mode}
		case MarkerDeleteFile:
			if e.ContentB64 != "" {
				return nil, fmt.Errorf("%s: content_b64 belongs to a whole new file", p)
			}
			f, err := current(p)
			if err != nil {
				return nil, err
			}
			if f == nil || f.deleted {
				return nil, fmt.Errorf("%s: cannot delete a file that does not exist", p)
			}
			next[p] = &approvedFile{deleted: true}
		default:
			if e.ContentB64 != "" {
				return nil, fmt.Errorf("%s: content_b64 belongs to a whole new file", p)
			}
			f, err := current(p)
			if err != nil {
				return nil, err
			}
			if f == nil || f.deleted {
				return nil, fmt.Errorf("%s: cannot edit a file that does not exist", p)
			}
			// The blocks are read with \n line breaks and the file keeps its own:
			// an LF file is edited as is, the blocks' lines end in \r\n in a CRLF
			// file, so an edit changes only its own lines (F89). Only a file
			// with mixed line breaks is read with universal newlines (\r\n and a
			// lone \r as \n) and saved with \n throughout.
			src := f.content
			search := []byte(strings.ReplaceAll(e.SearchBlock, "\r\n", "\n"))
			replace := []byte(strings.ReplaceAll(e.ReplaceBlock, "\r\n", "\n"))
			switch {
			case bytes.IndexByte(src, '\r') < 0:
			case allCRLF(src):
				search = bytes.ReplaceAll(search, []byte("\n"), []byte("\r\n"))
				replace = bytes.ReplaceAll(replace, []byte("\n"), []byte("\r\n"))
			default:
				src = universalNewlines(src)
			}
			if !bytes.Contains(src, search) {
				return nil, fmt.Errorf("%s: search_block not found in the approved content", p)
			}
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

// worktreeChanges are the worktree's changes that were not approved, as
// whole-file edits: a changed or new file with its current content, a
// missing file as a deletion. They are what a review-after proposal decides.
func (a *approvals) worktreeChanges() ([]domain.ProposedEdit, error) {
	paths := map[string]bool{}
	st, err := a.repo.w.Status()
	if err != nil {
		return nil, fmt.Errorf("read worktree status: %w", err)
	}
	for p, s := range st {
		if s.Staging != gogit.Unmodified || s.Worktree != gogit.Unmodified {
			paths[p] = true
		}
	}
	for p := range a.files { // an approved file changed back to its base content
		paths[p] = true
	}
	var edits []domain.ProposedEdit
	for _, p := range slices.Sorted(maps.Keys(paths)) {
		want, err := a.current(p)
		if err != nil {
			return nil, err
		}
		got, err := os.ReadFile(filepath.Join(a.repo.path, filepath.FromSlash(p)))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if want != nil && !want.deleted {
				edits = append(edits, domain.ProposedEdit{File: p, SearchBlock: MarkerDeleteFile})
			}
		case err != nil:
			return nil, err
		case want == nil || want.deleted || !bytes.Equal(got, want.content):
			e := domain.ProposedEdit{File: p, SearchBlock: MarkerNewFile, ReplaceBlock: string(got)}
			if !utf8.Valid(got) { // not text: a whole-file change shown by its size and digest
				e.ReplaceBlock, e.ContentB64 = "", base64.StdEncoding.EncodeToString(got)
			}
			edits = append(edits, e)
		}
	}
	return edits, nil
}

// restore puts paths back to their approved state: approved content, or the
// base commit's, or no file at all.
func (a *approvals) restore(paths []string) error {
	for _, p := range paths {
		full := filepath.Join(a.repo.path, filepath.FromSlash(p))
		f, err := a.current(p)
		if err != nil {
			return err
		}
		if f == nil || f.deleted {
			if err := os.Remove(full); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			continue
		}
		if err := writeAtomic(full, f.content, f.mode); err != nil {
			return err
		}
	}
	return nil
}

// violation is a reason finalize refuses to commit, recorded as an audit event.
type violation struct{ event, file, detail string }

// verify compares the worktree with the approved state: HEAD is still the
// base commit (the agent made no commits of its own), every approved path
// holds exactly its approved bytes (or is gone), and nothing else changed -
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
	git, cleanup, err := a.privateGit()
	if err != nil {
		return "", err
	}
	defer cleanup()
	base := a.base.Hash.String()
	tree, err := applyFiles(git, base, a.files)
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

// tree is the git tree the approved state makes on the base commit: what commit
// would commit, without committing it.
func (a *approvals) tree() (string, error) {
	git, cleanup, err := a.privateGit()
	if err != nil {
		return "", err
	}
	defer cleanup()
	return applyFiles(git, a.base.Hash.String(), a.files)
}

// privateGit runs git in the run's repository with a private index, the
// orchestrator's identity and no hooks (a hook written by the agent must not run
// as the orchestrator).
func (a *approvals) privateGit() (git func(stdin []byte, args ...string) (string, error), cleanup func(), err error) {
	tmp, err := os.MkdirTemp("", "staircase-commit-")
	if err != nil {
		return nil, nil, fmt.Errorf("commit approvals: %w", err)
	}
	name, email := a.repo.identity()
	env := append(os.Environ(), "GIT_INDEX_FILE="+filepath.Join(tmp, "index"), // private index
		"GIT_AUTHOR_NAME="+name, "GIT_AUTHOR_EMAIL="+email,
		"GIT_COMMITTER_NAME="+name, "GIT_COMMITTER_EMAIL="+email)
	git = func(stdin []byte, args ...string) (string, error) {
		cmd := exec.Command("git", append([]string{"-C", a.repo.path, "-c", "core.hooksPath=/dev/null"}, args...)...)
		var stderr bytes.Buffer
		cmd.Env, cmd.Stdin, cmd.Stderr = env, bytes.NewReader(stdin), &stderr
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("commit approvals: git %s: %w: %s", args[0], err, bytes.TrimSpace(stderr.Bytes()))
		}
		return strings.TrimSpace(string(out)), nil
	}
	return git, func() { _ = os.RemoveAll(tmp) }, nil
}

// binaryContent decodes a new file's content_b64 and checks it is what that
// field is for: bytes that are not valid UTF-8, in a proposal that has no
// replace_block, within the size limit. (Text has one representation only.)
func binaryContent(p string, e domain.ProposedEdit) ([]byte, error) {
	if e.ReplaceBlock != "" {
		return nil, fmt.Errorf("%s: a change has either replace_block or content_b64, not both", p)
	}
	b, err := base64.StdEncoding.DecodeString(e.ContentB64)
	if err != nil {
		return nil, fmt.Errorf("%s: content_b64 is not base64: %w", p, err)
	}
	if utf8.Valid(b) {
		return nil, fmt.Errorf("%s: the content is valid UTF-8 text: send it as replace_block", p)
	}
	if len(b) > maxApprovedBinaryBytes {
		return nil, fmt.Errorf("%s: file is %d bytes; at most %d can be approved at once", p, len(b), maxApprovedBinaryBytes)
	}
	return b, nil
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

// allCRLF reports whether every line break in b is \r\n.
func allCRLF(b []byte) bool {
	n := bytes.Count(b, []byte("\r\n"))
	return n > 0 && bytes.Count(b, []byte("\r")) == n && bytes.Count(b, []byte("\n")) == n
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
