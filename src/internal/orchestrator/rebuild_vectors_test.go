package orchestrator_test

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rebuildVector is one conformance case of docs/spec/rebuild-vectors: a base
// tree, approved proposals in order, and the git tree they produce, or the
// phrase a refusal contains.
type rebuildVector struct {
	Description string                  `json:"description"`
	Base        []vectorFile            `json:"base"`
	Proposals   [][]domain.ProposedEdit `json:"proposals"`
	Tree        string                  `json:"tree,omitempty"`
	Error       string                  `json:"error,omitempty"`
}

type vectorFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    string `json:"mode"` // 100644 or 100755
}

var (
	newFile = func(f, c string) domain.ProposedEdit {
		return domain.ProposedEdit{File: f, SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: c}
	}
	edit = func(f, s, r string) domain.ProposedEdit {
		return domain.ProposedEdit{File: f, SearchBlock: s, ReplaceBlock: r}
	}
	binary = func(f string, b []byte) domain.ProposedEdit {
		return domain.ProposedEdit{File: f, SearchBlock: orchestrator.MarkerNewFile, ContentB64: base64.StdEncoding.EncodeToString(b)}
	}
	del = func(f string) domain.ProposedEdit {
		return domain.ProposedEdit{File: f, SearchBlock: orchestrator.MarkerDeleteFile}
	}
)

func rebuildVectors() map[string]rebuildVector {
	base := []vectorFile{
		{"lf.txt", "a\nb\nb\n", "100644"}, {"crlf.txt", "x\r\ny\r\n", "100644"}, {"mixed.txt", "p\r\nq\nr\r\n", "100644"},
		{"run.sh", "echo a\n", "100755"}, {"old.txt", "bye\n", "100644"}, {"dir/only.txt", "alone\n", "100644"},
	}
	v := func(desc string, p ...[]domain.ProposedEdit) rebuildVector {
		return rebuildVector{Description: desc, Base: base, Proposals: p}
	}
	withError := func(x rebuildVector, why string) rebuildVector { x.Error = why; return x }
	return map[string]rebuildVector{
		"01-create-edit-delete": v("A new file, an edit, and a deletion.",
			[]domain.ProposedEdit{newFile("new/hello.txt", "hello\n")}, []domain.ProposedEdit{edit("lf.txt", "a\n", "A\n")}, []domain.ProposedEdit{del("old.txt")}),
		"02-edit-replaces-the-first-match": v("Only the first occurrence is replaced.",
			[]domain.ProposedEdit{edit("lf.txt", "b\n", "B\n")}),
		"03-crlf-file-keeps-crlf": v("The blocks' lines take the file's own CRLF line ends.",
			[]domain.ProposedEdit{edit("crlf.txt", "y\n", "Y\nZ\n")}),
		"04-mixed-newlines-become-lf": v("A file with mixed line ends is read with universal newlines and saved with LF.",
			[]domain.ProposedEdit{edit("mixed.txt", "q\n", "Q\n")}),
		"05-overwrite-keeps-the-mode": v("Overwriting an executable file keeps it executable.",
			[]domain.ProposedEdit{newFile("run.sh", "echo b\n")}),
		"06-new-file-is-not-executable": v("A new file is 100644.",
			[]domain.ProposedEdit{newFile("tool.sh", "echo hi\n")}),
		"07-deleting-the-last-file-removes-its-directory": v("Git trees hold no empty directories.",
			[]domain.ProposedEdit{del("dir/only.txt")}),
		"08-edits-of-a-proposal-apply-in-order": v("Later edits of one proposal see the earlier ones.",
			[]domain.ProposedEdit{newFile("notes.txt", "one\n"), edit("notes.txt", "one\n", "two\n")}),
		"09-create-then-delete": v("A file created and deleted again leaves no trace.",
			[]domain.ProposedEdit{newFile("tmp.txt", "x\n")}, []domain.ProposedEdit{del("tmp.txt")}),
		"10-refused-search-text-missing": withError(v("An edit whose search text is not in the file is refused.",
			[]domain.ProposedEdit{edit("lf.txt", "not there\n", "x\n")}), "search_block not found"),
		"11-refused-path-outside": withError(v("A path outside the repository is refused.",
			[]domain.ProposedEdit{newFile("../escape.txt", "x\n")}), "outside the project root"),
		"12-refused-git-internals": withError(v("A path inside .git is refused, in any case.",
			[]domain.ProposedEdit{newFile("sub/.GIT/hooks/pre-commit", "x\n")}), "repository internals"),
		"13-refused-delete-of-a-missing-file": withError(v("Deleting a file that does not exist is refused.",
			[]domain.ProposedEdit{del("nothing.txt")}), "cannot delete a file that does not exist"),
		"14-refused-edit-of-a-deleted-file": withError(v("Editing a file that an earlier proposal deleted is refused.",
			[]domain.ProposedEdit{del("old.txt")}, []domain.ProposedEdit{edit("old.txt", "bye\n", "hi\n")}), "cannot edit a file that does not exist"),
		"15-binary-new-file": v("A new file that is not valid UTF-8 is a whole-file change with content_b64 (ledger version 2).",
			[]domain.ProposedEdit{binary("img/logo.bin", []byte("\xff\xfe\x00\x80binary"))}),
		"16-binary-overwrites-a-file-and-keeps-its-mode": v("A file overwritten with bytes that are not text keeps its mode.",
			[]domain.ProposedEdit{binary("run.sh", []byte("\x7fELF\xff\x00"))}),
		"17-text-with-nul-bytes-stays-text": v("Content that is valid UTF-8, NUL bytes included, is replace_block, never content_b64.",
			[]domain.ProposedEdit{newFile("nul.txt", "a\x00b\n")}),
		"18-refused-content-b64-that-is-text": withError(v("content_b64 for bytes that are valid UTF-8 is refused: text has one representation.",
			[]domain.ProposedEdit{binary("t.txt", []byte("plain text\n"))}), "valid UTF-8 text"),
		"19-refused-both-replace-block-and-content-b64": withError(v("A change has replace_block or content_b64, not both.",
			[]domain.ProposedEdit{{File: "x.bin", SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: "x", ContentB64: base64.StdEncoding.EncodeToString([]byte("\xff"))}}), "not both"),
		"20-refused-content-b64-that-is-not-base64": withError(v("content_b64 must be base64.",
			[]domain.ProposedEdit{{File: "x.bin", SearchBlock: orchestrator.MarkerNewFile, ContentB64: "***"}}), "is not base64"),
		"21-refused-content-b64-on-an-edit": withError(v("content_b64 belongs to a whole new file, not to a search and replace.",
			[]domain.ProposedEdit{{File: "lf.txt", SearchBlock: "a\n", ContentB64: base64.StdEncoding.EncodeToString([]byte("\xff"))}}), "belongs to a whole new file"),
	}
}

// TestRebuildVectors keeps docs/spec/rebuild-vectors equal to what this
// package derives, and checks that it decides each case as the file says.
// UPDATE_DOCS=1 rewrites the files.
func TestRebuildVectors(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "docs", "spec", "rebuild-vectors")
	for name, v := range rebuildVectors() {
		t.Run(name, func(t *testing.T) {
			repo := t.TempDir()
			git := func(args ...string) string {
				out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
				require.NoError(t, err, "git %v: %s", args, out)
				return strings.TrimSpace(string(out))
			}
			git("init", "-q", "-b", "main")
			git("config", "user.email", "t@t")
			git("config", "user.name", "T")
			for _, f := range v.Base {
				mode := os.FileMode(0o644)
				if f.Mode == "100755" {
					mode = 0o755
				}
				require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(repo, f.Path)), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(repo, f.Path), []byte(f.Content), mode))
			}
			git("add", "-A")
			git("commit", "-q", "-m", "base")
			l := orchestrator.Ledger{Version: 1, Base: git("rev-parse", "HEAD")}
			for _, p := range v.Proposals {
				for _, e := range p {
					if e.ContentB64 != "" {
						l.Version = 2 // a ledger needs version 2 only when it holds a file that is not text
					}
				}
			}
			raw, err := ledgerJSON(l, v.Proposals)
			require.NoError(t, err)
			tree, _, err := orchestrator.RebuildTree(repo, raw)
			if v.Error == "" {
				v.Tree = tree
			}

			want, merr := json.MarshalIndent(v, "", "  ")
			require.NoError(t, merr)
			want = append(want, '\n')
			path := filepath.Join(dir, name+".json")
			if os.Getenv("UPDATE_DOCS") != "" {
				require.NoError(t, os.MkdirAll(dir, 0o755))
				require.NoError(t, os.WriteFile(path, want, 0o644))
			}
			got, rerr := os.ReadFile(path)
			require.NoError(t, rerr)
			assert.Equal(t, string(want), string(got), "%s is out of date: UPDATE_DOCS=1 go test ./src/internal/orchestrator -run TestRebuildVectors", path)
			if v.Error != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), v.Error)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func ledgerJSON(l orchestrator.Ledger, proposals [][]domain.ProposedEdit) ([]byte, error) {
	for i, p := range proposals {
		l.Proposals = append(l.Proposals, orchestrator.LedgerProposal{Seq: i + 1, Source: "operator", Edits: p})
	}
	return json.Marshal(l)
}
