package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode"

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

// TestNoHiddenUnicode: no tracked file contains characters that make code
// read differently from how it runs (Trojan Source) - the guard staircase
// applies to agents' changes, applied to its own repository. Tests write such
// characters as escapes (\u202e), never raw.
func TestNoHiddenUnicode(t *testing.T) {
	out, err := exec.Command("git", "-C", repoRoot, "ls-files", "-z").Output()
	require.NoError(t, err)
	hidden := regexp.MustCompile("[\u202a-\u202e\u2066-\u2069\u200b-\u200d\u2060\ufeff]")
	for _, f := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		b, err := os.ReadFile(filepath.Join(repoRoot, f))
		if err != nil {
			continue
		}
		if loc := hidden.FindIndex(b); loc != nil {
			t.Errorf("%s: hidden Unicode at byte %d", f, loc[0])
		}
	}
}
