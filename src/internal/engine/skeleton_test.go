package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/staircase-core/src/internal/engine"
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
