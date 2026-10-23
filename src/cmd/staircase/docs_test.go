package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// repoRoot is the repository root, seen from this package's directory.
const repoRoot = "../../.."

// cliReference renders every command's usage, description and flags as
// Markdown, from the command tree itself, so the reference cannot drift from
// the binary.
func cliReference() string {
	var b strings.Builder
	b.WriteString("# CLI reference\n\n")
	b.WriteString("<!-- Generated from the command tree by TestCLIReference. Do not edit by hand:\n")
	b.WriteString("     UPDATE_DOCS=1 go test ./src/cmd/staircase -run TestCLIReference -->\n\n")
	b.WriteString("Every `staircase` command, as `staircase <command> --help` prints it. For what the\n")
	b.WriteString("commands are for, start with [Getting started](../QUICKSTART.md) and [Concepts](concepts.md).\n\n")
	b.WriteString("Global flag, accepted by every command:\n\n```\n")
	b.WriteString(rootCmd.PersistentFlags().FlagUsages())
	b.WriteString("```\n\nThe workspace directory can also be set with the `STAIRCASE_DIR` environment variable.\n")

	var visit func(c *cobra.Command)
	visit = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if !sub.IsAvailableCommand() || sub.Name() == "help" || sub.Name() == "completion" {
				continue
			}
			if sub.Runnable() {
				writeCommand(&b, sub)
			}
			if sub.HasAvailableSubCommands() {
				visit(sub)
			}
		}
	}
	visit(rootCmd)
	return b.String()
}

// writeCommand renders one runnable command: usage, description and flags.
func writeCommand(b *strings.Builder, c *cobra.Command) {
	fmt.Fprintf(b, "\n## %s\n\n%s\n\n```\n%s\n```\n", c.CommandPath(), c.Short, c.UseLine())
	if long := strings.TrimSpace(c.Long); long != "" && long != c.Short {
		fmt.Fprintf(b, "\n%s\n", long)
	}
	if flags := c.NonInheritedFlags().FlagUsages(); strings.TrimSpace(flags) != "" {
		fmt.Fprintf(b, "\nFlags:\n\n```\n%s```\n", flags)
	}
}

// TestCLIReference keeps docs/cli.md equal to what the command tree says.
// Regenerate with UPDATE_DOCS=1.
func TestCLIReference(t *testing.T) {
	path := filepath.Join(repoRoot, "docs", "cli.md")
	want := cliReference()
	if os.Getenv("UPDATE_DOCS") == "1" {
		require.NoError(t, os.WriteFile(path, []byte(want), 0o644))
	}
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, want, string(got), "docs/cli.md is out of date: UPDATE_DOCS=1 go test ./src/cmd/staircase -run TestCLIReference")
}

var (
	mdLink    = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	mdHeading = regexp.MustCompile(`(?m)^#{1,6}\s+(.+?)\s*$`)
	codeFence = regexp.MustCompile("(?s)```.*?```")
)

// anchor is GitHub's heading slug: lower case, spaces to hyphens, most
// punctuation dropped.
func anchor(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(heading) {
		switch {
		case r == ' ':
			b.WriteRune('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

func anchors(file string) (map[string]bool, error) {
	src, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, m := range mdHeading.FindAllStringSubmatch(codeFence.ReplaceAllString(string(src), ""), -1) {
		h := strings.NewReplacer("`", "", "*", "").Replace(m[1])
		a := anchor(h)
		for i := 1; out[a]; i++ { // GitHub numbers repeated headings
			a = fmt.Sprintf("%s-%d", anchor(h), i)
		}
		out[a] = true
	}
	return out, nil
}

// TestDocLinks checks every relative link in the project's Markdown: the
// target file exists and, when the link names a heading, the heading does.
func TestDocLinks(t *testing.T) {
	var files []string
	for _, pattern := range []string{"*.md", "docs/*.md", "docs/adr/*.md", "demo/*.md"} {
		m, err := filepath.Glob(filepath.Join(repoRoot, pattern))
		require.NoError(t, err)
		files = append(files, m...)
	}
	sort.Strings(files)
	require.NotEmpty(t, files)
	for _, file := range files {
		if strings.HasSuffix(file, "CHANGELOG.md") {
			continue // history: links there describe past states
		}
		src, err := os.ReadFile(file)
		require.NoError(t, err)
		for _, m := range mdLink.FindAllStringSubmatch(codeFence.ReplaceAllString(string(src), ""), -1) {
			link := m[1]
			if strings.Contains(link, "://") || strings.HasPrefix(link, "mailto:") {
				continue
			}
			target, frag, _ := strings.Cut(link, "#")
			targetFile := file
			if target != "" {
				targetFile = filepath.Join(filepath.Dir(file), target)
				if _, err := os.Stat(targetFile); err != nil {
					t.Errorf("%s: link %q: %s does not exist", rel(file), link, target)
					continue
				}
			}
			if frag == "" || !strings.HasSuffix(targetFile, ".md") {
				continue
			}
			as, err := anchors(targetFile)
			require.NoError(t, err)
			if !as[frag] {
				t.Errorf("%s: link %q: no heading #%s in %s", rel(file), link, frag, rel(targetFile))
			}
		}
	}
}

func rel(p string) string {
	r, err := filepath.Rel(repoRoot, p)
	if err != nil {
		return p
	}
	return r
}

// TestNoHiddenUnicode: no tracked file holds an invisible character: direction controls (Trojan Source), zero-width and format
// characters, variation selectors, or Unicode tag characters, which can carry hidden text. Tests write them as escapes (\u202e).
func TestNoHiddenUnicode(t *testing.T) {
	out, err := exec.Command("git", "-C", repoRoot, "ls-files", "-z").Output()
	require.NoError(t, err)
	hidden := regexp.MustCompile("[\u00ad\u061c\u180e\u200b-\u200f\u2028-\u202e\u2060-\u2064\u2066-\u2069\ufeff\ufe00-\ufe0e\U000e0000-\U000e007f]")
	for _, f := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		b, err := os.ReadFile(filepath.Join(repoRoot, f))
		if err != nil {
			continue
		}
		if !utf8.Valid(b) { // an archive or an image: its bytes are not text
			continue
		}
		if loc := hidden.FindIndex(b); loc != nil {
			t.Errorf("%s: hidden Unicode at byte %d", f, loc[0])
		}
		if i := bytes.Index(b, []byte("\ufffd")); i >= 0 { // text damaged by an encoding mix-up
			t.Errorf("%s: replacement character (corrupted text) at byte %d", f, i)
		}
	}
}

// TestNoEmojiInOutput: what the program prints, the demo scripts and the pages that show the output use words, not pictographs, so the
// output reads the same in every terminal, log and screen reader. Emoji, dingbats such as the check marks, and the symbol blocks are
// refused; arrows and typographic dashes are fine. Tests may use them as data. The CHANGELOG and the recorded evidence are history.
func TestNoEmojiInOutput(t *testing.T) {
	out, err := exec.Command("git", "-C", repoRoot, "ls-files", "-z").Output()
	require.NoError(t, err)
	pictograph := regexp.MustCompile("[\u231a\u231b\u23e9-\u23fa\u2600-\u27bf\u2b00-\u2bff\U0001f000-\U0001faff]\ufe0f?")
	checked := func(f string) bool {
		switch {
		case strings.HasSuffix(f, "_test.go"), f == "CHANGELOG.md", strings.HasPrefix(f, "docs/release-evidence/"):
			return false
		case strings.HasSuffix(f, ".go"), strings.HasSuffix(f, ".html"), strings.HasSuffix(f, ".md"):
			return true
		case strings.HasPrefix(f, "demo/") && strings.HasSuffix(f, ".sh"), f == "Makefile":
			return true
		}
		return false
	}
	for _, f := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if !checked(f) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(repoRoot, f))
		if err != nil {
			continue
		}
		for n, line := range strings.Split(string(b), "\n") {
			if m := pictograph.FindString(line); m != "" {
				t.Errorf("%s:%d: %q: say it in words", f, n+1, m)
			}
		}
	}
}

// TestNoBuiltBinaryIsTracked: nothing the build leaves behind is in the repository. A `go build` run inside this directory writes a
// binary of tens of megabytes here, and a careless `git add -A` once took one in; the largest file the repository holds is far smaller
// than the limit.
func TestNoBuiltBinaryIsTracked(t *testing.T) {
	const limit = 2 << 20
	out, err := exec.Command("git", "-C", repoRoot, "ls-files", "-z").Output()
	require.NoError(t, err)
	for _, f := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		info, err := os.Stat(filepath.Join(repoRoot, f))
		if err != nil {
			continue
		}
		assert.LessOrEqual(t, info.Size(), int64(limit), "%s is %d bytes: a built binary, or a file that does not belong in the repository", f, info.Size())
	}
}

// latestRelease is the newest released version the changelog names: the first "## [X.Y.Z]" heading under [Unreleased].
func latestRelease(t *testing.T) string {
	src, err := os.ReadFile(filepath.Join(repoRoot, "CHANGELOG.md"))
	require.NoError(t, err)
	m := regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+)\] - `).FindSubmatch(src)
	require.NotNil(t, m, "no released version in the changelog")
	return string(m[1])
}

// TestVersionsNameTheLatestRelease: what tells a person which version to use must name the version the changelog says is the latest, so a
// release cannot leave the documented pinned Action example, or the fixture that exercises it, on an older one. The release steps in
// CONTRIBUTING.md say to bump both when preparing a release.
func TestVersionsNameTheLatestRelease(t *testing.T) {
	v := "v" + latestRelease(t)
	pinned := func(path string, patterns ...string) {
		src, err := os.ReadFile(filepath.Join(repoRoot, path))
		require.NoError(t, err)
		for _, p := range patterns {
			assert.Contains(t, string(src), fmt.Sprintf(p, v), "%s does not name the latest release %s (%q)", path, v, p)
		}
	}
	pinned("docs/audit.md", "uses: b070nd/stAirCase@%s ", "version: %s ")
	pinned("demo/ruleset-fixture.sh", `VERSION="${2:-%s}"`, "(default version: %s)")
}

// TestChangelogHasNoLinkToAnotherProgramsRelease: the changelog keeps the earlier Bash implementation's 1.0.0 to 1.2.0 as history, but
// a link to the tag v1.0.0 of this repository now opens this program's 1.0.0, not the Bash line's; so those sections carry no links.
func TestChangelogHasNoLinkToAnotherProgramsRelease(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRoot, "CHANGELOG.md"))
	require.NoError(t, err)
	for _, l := range strings.Split(string(src), "\n") {
		assert.False(t, strings.HasPrefix(l, "[bash "), "a reference link for the Bash line points at a tag of this repository: %s", l)
	}
}
