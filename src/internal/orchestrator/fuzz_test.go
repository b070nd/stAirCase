package orchestrator

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/domain"
)

// The trusted core decides what bytes a run may commit, so its rules are
// checked on arbitrary input. `go test` runs the seeds; `make fuzz` explores.

// FuzzCleanApprovedPath: whatever the agent sends, an accepted path is a
// canonical, relative path to a non-directory inside the root, with no
// repository internals and no symlink on the way.
func FuzzCleanApprovedPath(f *testing.F) {
	for _, s := range []string{
		"file.txt", "sub/f.txt", "./a.txt", "sub/../ok.txt", "sub/new/deeper/n.txt", "sub/.gitignore",
		"", ".", "..", "../escape.txt", "sub/../../escape.txt", "/etc/passwd", `..\escape.txt`,
		".git", ".git/config", "sub/.git/x", ".GIT/hooks/pre-commit", "sub", "sub/dir", "link/x.txt",
		"alias.txt", "a/./b//c.txt", "sub/\x00.txt", " .git", "sub/.git ", "…/x",
	} {
		f.Add(s)
	}
	root := pathTree(f)
	f.Fuzz(func(t *testing.T, rel string) {
		got, err := cleanApprovedPath(root, rel)
		if err != nil {
			return
		}
		if got == "" || got == "." || filepath.IsAbs(got) || strings.HasPrefix(got, "/") {
			t.Fatalf("%q accepted as %q: not a relative file path", rel, got)
		}
		if got != filepath.ToSlash(filepath.Clean(filepath.FromSlash(got))) {
			t.Fatalf("%q accepted as %q: not canonical", rel, got)
		}
		p := root
		for _, part := range strings.Split(got, "/") {
			if part == ".." || strings.EqualFold(part, ".git") {
				t.Fatalf("%q accepted as %q: component %q", rel, got, part)
			}
			p = filepath.Join(p, part)
			if info, err := os.Lstat(p); err == nil && info.Mode()&os.ModeSymlink != 0 {
				t.Fatalf("%q accepted as %q: goes through symlink %s", rel, got, p)
			}
		}
		if r, err := filepath.Rel(root, p); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			t.Fatalf("%q accepted as %q: outside the root", rel, got)
		}
		if info, err := os.Lstat(p); err == nil && info.IsDir() {
			t.Fatalf("%q accepted as %q: a directory", rel, got)
		}
	})
}

// pathTree builds the tree the path rules are checked against: a file, a
// directory, a symlink out of the root and a symlink to a file.
func pathTree(f *testing.F) string {
	root := f.TempDir()
	for _, err := range []error{
		os.MkdirAll(filepath.Join(root, "sub", "dir"), 0o755),
		os.WriteFile(filepath.Join(root, "sub", "f.txt"), []byte("x"), 0o644),
		os.Symlink(f.TempDir(), filepath.Join(root, "link")),
		os.Symlink(filepath.Join(root, "sub", "f.txt"), filepath.Join(root, "alias.txt")),
	} {
		if err != nil {
			f.Fatal(err)
		}
	}
	return root
}

// FuzzDerive: an approved edit means exactly one thing. A search/replace
// changes the first occurrence of the search text in the file (read with
// universal newlines) and nothing else, or is refused when the text is
// absent; a new file is its content byte for byte; a delete removes the file.
// Deriving twice gives the same bytes.
func FuzzDerive(f *testing.F) {
	for _, s := range [][3]string{
		{"a\nb\na2\n", "b\n", "B\n"},
		{"a\r\nb\r\n", "b\r\n", "B\n"},      // CRLF file, CRLF block
		{"a\rb\r", "b", "B"},                // lone CR read as newline
		{"x", "missing", "y"},               // refused
		{"x", "", "prefix"},                 // empty search: inserts at the start
		{"x", MarkerNewFile, "new\r\nfile"}, // whole-file content, kept as is
		{"x", MarkerDeleteFile, ""},
		{"a\nb\n", "a\nb\n", ""}, // edit to empty
		{"abcabc", "abc", "Z"},   // only the first occurrence
	} {
		f.Add(s[0], s[1], s[2])
	}
	root := f.TempDir() // derive only reads it: one empty worktree serves every input
	f.Fuzz(func(t *testing.T, content, search, replace string) {
		a := &approvals{repo: &GitRepo{path: root},
			files: map[string]*approvedFile{"f.txt": {content: []byte(content), mode: 0o644}}}
		edit := []domain.ProposedEdit{{File: "f.txt", SearchBlock: search, ReplaceBlock: replace}}
		next, err := a.derive(edit)
		again, err2 := a.derive(edit)
		if (err == nil) != (err2 == nil) || (err == nil && !bytes.Equal(next["f.txt"].content, again["f.txt"].content)) {
			t.Fatalf("deriving twice differs")
		}

		switch search {
		case MarkerNewFile:
			if len(replace) > maxApprovedFileBytes {
				if err == nil {
					t.Fatalf("a %d-byte new file was accepted", len(replace))
				}
				return
			}
			if err != nil || string(next["f.txt"].content) != replace || next["f.txt"].deleted {
				t.Fatalf("new file: got %+v, %v; want the content as is", next["f.txt"], err)
			}
		case MarkerDeleteFile:
			if err != nil || !next["f.txt"].deleted {
				t.Fatalf("delete: got %v, %v", next["f.txt"], err)
			}
		default:
			src := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(content)
			s := strings.ReplaceAll(search, "\r\n", "\n")
			i := strings.Index(src, s)
			if i < 0 {
				if err == nil {
					t.Fatalf("search text %q is not in %q but the edit was accepted", s, src)
				}
				return
			}
			want := src[:i] + strings.ReplaceAll(replace, "\r\n", "\n") + src[i+len(s):]
			if err != nil || string(next["f.txt"].content) != want || next["f.txt"].mode != 0o644 {
				t.Fatalf("edit: got %q (%v); want %q", next["f.txt"].content, err, want)
			}
		}
	})
}

// FuzzUniversalNewlines: the text an edit applies to has no carriage returns
// left, and reading it again changes nothing.
func FuzzUniversalNewlines(f *testing.F) {
	for _, s := range []string{"", "a\r\nb", "a\rb", "\r\r\n\n", "\n\r"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		once := universalNewlines(b)
		if bytes.IndexByte(once, '\r') >= 0 {
			t.Fatalf("%q still holds a carriage return: %q", b, once)
		}
		if !bytes.Equal(universalNewlines(once), once) {
			t.Fatalf("%q: not idempotent", b)
		}
	})
}
