package engine_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── TokenEstimate / WarnTokenBudget ─────────────────────────────────────────

func TestTokenEstimate_zero_for_empty(t *testing.T) {
	assert.Equal(t, 0, engine.TokenEstimate(""))
}

func TestTokenEstimate_four_chars_one_token(t *testing.T) {
	assert.Equal(t, 1, engine.TokenEstimate("abcd"))
}

func TestTokenEstimate_proportional(t *testing.T) {
	assert.Equal(t, 25, engine.TokenEstimate(strings.Repeat("a", 100)))
}

func TestWarnTokenBudget_no_warn_below_threshold(t *testing.T) {
	assert.Empty(t, engine.WarnTokenBudget(strings.Repeat("a", 100)))
}

func TestWarnTokenBudget_warns_above_threshold(t *testing.T) {
	// >100k tokens = >400k chars
	bigText := strings.Repeat("x", 400_005)
	w := engine.WarnTokenBudget(bigText)
	assert.NotEmpty(t, w)
	assert.Contains(t, w, "100k")
}

// ─── PackXML ─────────────────────────────────────────────────────────────────

func TestPackXML_wraps_content_in_file_tags(t *testing.T) {
	files := map[string]string{"src/main.go": "package main"}
	xml := engine.PackXML(files)
	assert.Contains(t, xml, `<file path="src/main.go">`)
	assert.Contains(t, xml, "package main")
	assert.Contains(t, xml, "</file>")
}

func TestPackXML_empty_map(t *testing.T) {
	assert.Empty(t, engine.PackXML(nil))
}

func TestPackXML_multiple_files(t *testing.T) {
	files := map[string]string{
		"a.go": "package a",
		"b.go": "package b",
	}
	xml := engine.PackXML(files)
	assert.Equal(t, 2, strings.Count(xml, "<file "))
	assert.Equal(t, 2, strings.Count(xml, "</file>"))
}

// ─── RepoMap: directory tree ──────────────────────────────────────────────────

func TestRepoMap_includes_files(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644))

	result, err := engine.RepoMap(dir)
	require.NoError(t, err)
	assert.Contains(t, result, "main.go")
}

func TestRepoMap_respects_gitignore_extension_pattern(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.log"), []byte("log"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644))

	result, err := engine.RepoMap(dir)
	require.NoError(t, err)
	assert.NotContains(t, result, "app.log")
	assert.Contains(t, result, "main.go")
}

func TestRepoMap_skips_default_ignored_dirs(t *testing.T) {
	for _, ignoredDir := range []string{"node_modules", ".git", "__pycache__", "venv"} {
		t.Run(ignoredDir, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(dir, ignoredDir), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, ignoredDir, "file.js"), []byte("x"), 0o644))

			result, err := engine.RepoMap(dir)
			require.NoError(t, err)
			assert.NotContains(t, result, ignoredDir, "ignored dir should not appear in repo map")
		})
	}
}

func TestRepoMap_skips_binary_extensions(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "logo.png"), []byte("PNG"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.sum"), []byte("sum"), 0o644))

	result, err := engine.RepoMap(dir)
	require.NoError(t, err)
	assert.NotContains(t, result, "logo.png")
	assert.NotContains(t, result, "go.sum")
}

// ─── Signature extraction ─────────────────────────────────────────────────────

func TestRepoMap_extracts_go_functions(t *testing.T) {
	dir := t.TempDir()
	src := "package main\n\nfunc Foo(x int) error {\n\treturn nil\n}\n\nfunc bar() {}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.go"), []byte(src), 0o644))

	result, err := engine.RepoMap(dir)
	require.NoError(t, err)
	assert.Contains(t, result, "func Foo(x int) error {")
	assert.Contains(t, result, "func bar() {}")
}

func TestRepoMap_extracts_python_defs_and_classes(t *testing.T) {
	dir := t.TempDir()
	src := "class MyClass:\n    pass\n\ndef sync_fn(x):\n    pass\n\nasync def async_fn():\n    pass\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mod.py"), []byte(src), 0o644))

	result, err := engine.RepoMap(dir)
	require.NoError(t, err)
	assert.Contains(t, result, "class MyClass:")
	assert.Contains(t, result, "def sync_fn(x):")
	assert.Contains(t, result, "async def async_fn():")
}

func TestRepoMap_extracts_typescript_functions(t *testing.T) {
	dir := t.TempDir()
	src := "export function greet(name: string): string {\n  return name;\n}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "util.ts"), []byte(src), 0o644))

	result, err := engine.RepoMap(dir)
	require.NoError(t, err)
	assert.Contains(t, result, "export function greet")
}

func TestRepoMap_truncates_long_signatures(t *testing.T) {
	dir := t.TempDir()
	// Signature longer than 120 chars
	longSig := "func " + strings.Repeat("a", 120) + "(x int) error {"
	src := "package main\n\n" + longSig + "\n\treturn nil\n}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "long.go"), []byte(src), 0o644))

	result, err := engine.RepoMap(dir)
	require.NoError(t, err)
	// The truncated form should appear (ends with …)
	assert.Contains(t, result, "…")
}

func TestRepoMap_staircaseignore(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".staircaseignore"), []byte("secret.go\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "secret.go"), []byte("package main\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "public.go"), []byte("package main\n"), 0o644))

	result, err := engine.RepoMap(dir)
	require.NoError(t, err)
	assert.NotContains(t, result, "secret.go")
	assert.Contains(t, result, "public.go")
}

func TestRepoMap_double_star_gitignore(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "src", "internal")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "foo_test.go"), []byte("package x\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "foo.go"), []byte("package x\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("**/*_test.go\n"), 0o644))

	result, err := engine.RepoMap(dir)
	require.NoError(t, err)
	assert.NotContains(t, result, "foo_test.go", "** pattern should exclude test files at any depth")
	assert.Contains(t, result, "foo.go")
}

// ─── MatchGlob unit tests ─────────────────────────────────────────────────────

func TestMatchGlob_literal_match(t *testing.T) {
	assert.True(t, engine.MatchGlob("foo.go", "foo.go"))
	assert.False(t, engine.MatchGlob("foo.go", "bar.go"))
}

func TestMatchGlob_single_star(t *testing.T) {
	assert.True(t, engine.MatchGlob("*.go", "main.go"))
	assert.False(t, engine.MatchGlob("*.go", "main.ts"))
}

func TestMatchGlob_double_star_prefix(t *testing.T) {
	assert.True(t, engine.MatchGlob("**/*.go", "src/main.go"))
	assert.True(t, engine.MatchGlob("**/*.go", "a/b/c/main.go"))
	assert.False(t, engine.MatchGlob("**/*.go", "a/b/c/main.ts"))
}

func TestMatchGlob_double_star_suffix(t *testing.T) {
	assert.True(t, engine.MatchGlob("node_modules/**", "node_modules/foo/bar.js"))
	assert.True(t, engine.MatchGlob("node_modules/**", "node_modules/foo"))
	assert.False(t, engine.MatchGlob("node_modules/**", "src/foo.js"))
}

func TestMatchGlob_double_star_infix(t *testing.T) {
	assert.True(t, engine.MatchGlob("src/**/*.test.ts", "src/components/Button.test.ts"))
	assert.True(t, engine.MatchGlob("src/**/*.test.ts", "src/a/b/c/X.test.ts"))
	assert.False(t, engine.MatchGlob("src/**/*.test.ts", "src/Button.spec.ts"))
}

func TestMatchGlob_double_star_zero_segments(t *testing.T) {
	// ** should match zero components too: "**/*.go" matches "main.go"
	assert.True(t, engine.MatchGlob("**/*.go", "main.go"))
}

// ─── loadIgnorePatterns error surfacing (E-1) ─────────────────────────────────

func TestRepoMap_unreadable_gitignore_warns_but_continues(t *testing.T) {
	dir := t.TempDir()
	// Create a .gitignore that is not readable.
	gitignorePath := filepath.Join(dir, ".gitignore")
	require.NoError(t, os.WriteFile(gitignorePath, []byte("*.log\n"), 0o000))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.go"), []byte("package main\n"), 0o644))
	t.Cleanup(func() { os.Chmod(gitignorePath, 0o644) }) // restore for cleanup

	// RepoMap should not hard-fail; it writes a warning to stderr and continues.
	result, err := engine.RepoMap(dir)
	require.NoError(t, err, "unreadable .gitignore must not return an error from RepoMap")
	assert.Contains(t, result, "app.go", "files should still be included despite .gitignore read failure")
}

func TestPackXML_escapes_special_chars_in_path(t *testing.T) {
	files := map[string]string{`path/with "quotes" & <tags>`: "content"}
	xml := engine.PackXML(files)
	assert.Contains(t, xml, `&quot;`)
	assert.Contains(t, xml, `&amp;`)
	assert.Contains(t, xml, `&lt;`)
	assert.NotContains(t, xml, `"quotes"`)
}

// TestRepoMap_never_reads_through_a_symlink: the map goes to the model; a link
// to a file outside the repository must not put that file's lines into it.
func TestRepoMap_never_reads_through_a_symlink(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "secret.go")
	require.NoError(t, os.WriteFile(outside, []byte("package x\nfunc LeakedSecretSignature() {}\n"), 0o644))
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "link.go")))

	result, err := engine.RepoMap(dir)
	require.NoError(t, err)
	assert.Contains(t, result, "func main()")
	assert.NotContains(t, result, "LeakedSecretSignature")
}

// repoWithCommit makes a git repository whose last commit holds files.
func repoWithCommit(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "t@t")
	git("config", "user.name", "T")
	for name, content := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	return dir
}

// TestRepoMap_is_the_last_commit_not_the_working_tree: agents work on the last
// commit, so the map they are given describes it, and says nothing of files
// that were never committed or of edits that were not.
func TestRepoMap_is_the_last_commit_not_the_working_tree(t *testing.T) {
	dir := repoWithCommit(t, map[string]string{
		"main.go":       "package main\nfunc committed() {}\n",
		"pkg/util.go":   "package pkg\nfunc Helper() {}\n",
		".gitignore":    "ignored.log\n",
		"pkg/other.txt": "x\n",
	})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc committed() {}\nfunc UncommittedEdit() {}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "scratch_notes.go"), []byte("package main\nfunc Untracked() {}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "credentials.json"), []byte("{}"), 0o644))
	require.NoError(t, os.Remove(filepath.Join(dir, "pkg", "util.go")))

	result, err := engine.RepoMap(dir)
	require.NoError(t, err)
	assert.Contains(t, result, "main.go")
	assert.Contains(t, result, "func committed()")
	assert.Contains(t, result, "pkg/util.go", "deleted in the working tree, still in the commit")
	assert.Contains(t, result, "func Helper()")
	assert.NotContains(t, result, "UncommittedEdit")
	assert.NotContains(t, result, "scratch_notes.go")
	assert.NotContains(t, result, "credentials.json")
}

// TestRepoMap_of_a_commit_keeps_the_ignore_rules: the same files and order as a
// walk of a checkout would give, with the default and .staircaseignore rules.
func TestRepoMap_of_a_commit_keeps_the_ignore_rules(t *testing.T) {
	dir := repoWithCommit(t, map[string]string{
		"b.go":                  "package b\nfunc B() {}\n",
		"a-b.go":                "package a\n",
		"a/x.go":                "package a\nfunc X() {}\n",
		"node_modules/dep/i.js": "function dep() {}\n",
		"logo.png":              "png",
		"gen/out.go":            "package gen\n",
		".staircaseignore":      "gen\n",
		"vendor/v.go":           "package v\n",
	})
	require.NoError(t, os.Symlink("/etc/hosts", filepath.Join(dir, "link.go")))

	result, err := engine.RepoMap(dir)
	require.NoError(t, err)
	assert.NotContains(t, result, "node_modules")
	assert.NotContains(t, result, "logo.png")
	assert.NotContains(t, result, "gen/out.go", ".staircaseignore applies, read from the commit")
	assert.NotContains(t, result, "link.go")
	var order []string
	for _, line := range strings.Split(result, "\n") {
		if line != "" && !strings.HasPrefix(line, " ") {
			order = append(order, line)
		}
	}
	assert.Equal(t, []string{".staircaseignore", "a/x.go", "a-b.go", "b.go", "vendor/v.go"}, order, "directory entries sort by name, as in a walk")
}

// TestRepoMap_of_a_folder_inside_a_repository: only that folder, with paths
// relative to it, as a walk of it would give.
func TestRepoMap_of_a_folder_inside_a_repository(t *testing.T) {
	dir := repoWithCommit(t, map[string]string{
		"top.go":        "package top\nfunc Top() {}\n",
		"svc/api/a.go":  "package api\nfunc A() {}\n",
		"other/leak.go": "package other\nfunc Leak() {}\n",
	})
	result, err := engine.RepoMap(filepath.Join(dir, "svc"))
	require.NoError(t, err)
	assert.Equal(t, "api/a.go\n  func A() {}\n", result)
}
