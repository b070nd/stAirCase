package orchestrator

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/b070nd/staircase-core/src/internal/ipc"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// maxApprovedFileBytes caps a created file: the whole proposal must fit the
// IPC line limit (256 KiB) and stay reviewable by a human.
const maxApprovedFileBytes = 200 << 10

// search_block markers the runtime uses for whole-file operations.
const (
	markerNewFile    = "(new file)"
	markerDeleteFile = "(delete file)"
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
// edits the operator was shown, and finalize commits only if the worktree
// matches it.
type approvals struct {
	root  string
	base  *object.Commit
	files map[string]*approvedFile // cleaned slash path → approved state
}

func newApprovals(root string, gr *GitRepo, baseSHA string) (*approvals, error) {
	c, err := gr.r.CommitObject(plumbing.NewHash(baseSHA))
	if err != nil {
		return nil, fmt.Errorf("load base commit %s: %w", baseSHA, err)
	}
	return &approvals{root: root, base: c, files: map[string]*approvedFile{}}, nil
}

// errBadPath marks a refusal caused by the proposed path itself.
var errBadPath = errors.New("invalid path")

// derive computes the state that approving edits would produce, without
// recording it. An error means the proposal cannot be approved as shown.
func (a *approvals) derive(edits []ipc.ProposedEdit) (map[string]*approvedFile, error) {
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
		p, err := cleanApprovedPath(a.root, e.File)
		if err != nil {
			return nil, err
		}
		switch e.SearchBlock {
		case markerNewFile:
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
		case markerDeleteFile:
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
			// The runtime reads files in Python text mode (universal newlines)
			// and normalizes only \r\n in the blocks; derive it the same way.
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

// change is a reason finalize refuses to commit, recorded as an audit event.
type change struct{ event, file, detail string }

// verify compares the worktree with the approved state: every approved path
// must hold exactly its approved bytes (or be gone), and nothing else may have
// changed — in the working tree or the index.
func (a *approvals) verify(w *GitRepo) ([]change, error) {
	var out []change
	for p, f := range a.files {
		if _, err := cleanApprovedPath(a.root, p); err != nil { // swapped for a dir or symlink
			out = append(out, change{"approval_path_escape", p, err.Error()})
			continue
		}
		full := filepath.Join(a.root, filepath.FromSlash(p))
		info, err := os.Lstat(full)
		switch {
		case f.deleted:
			if err == nil {
				out = append(out, change{"approval_content_mismatch", p, "deletion was approved but the file exists"})
			}
		case err != nil:
			out = append(out, change{"approval_content_mismatch", p, "approved file is missing"})
		case !info.Mode().IsRegular():
			out = append(out, change{"approval_content_mismatch", p, "approved path is not a regular file"})
		default:
			got, err := os.ReadFile(full)
			if err != nil || !bytes.Equal(got, f.content) {
				out = append(out, change{"approval_content_mismatch", p,
					fmt.Sprintf("approved sha256 %s, found %s", sha256Hex(f.content), sha256Hex(got))})
			}
		}
	}
	st, err := w.w.Status()
	if err != nil {
		return nil, fmt.Errorf("stage approved files: read worktree status: %w", err)
	}
	for p, s := range st {
		if s.Staging == gogit.Unmodified && s.Worktree == gogit.Unmodified {
			continue
		}
		if _, ok := a.files[p]; !ok {
			out = append(out, change{"unapproved_worktree_change", p, fmt.Sprintf("index %q, worktree %q", s.Staging, s.Worktree)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].file < out[j].file })
	return out, nil
}

// stage puts exactly the approved state in the index, with approved modes
// (the runtime's temp-file writes leave 0600, dropping an exec bit).
func (a *approvals) stage(w *GitRepo) error {
	for p, f := range a.files {
		if !f.deleted {
			if err := os.Chmod(filepath.Join(a.root, filepath.FromSlash(p)), f.mode); err != nil {
				return fmt.Errorf("stage approved files: %w", err)
			}
		}
		if _, err := w.w.Add(p); err != nil { // a missing path stages its deletion
			return fmt.Errorf("stage approved files: git add %q: %w", p, err)
		}
	}
	return nil
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
