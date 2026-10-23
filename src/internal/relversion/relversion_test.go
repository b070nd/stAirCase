package relversion

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const changelog = `# Changelog

## [Unreleased]

## [1.3.0] - 2026-11-01

Notes.

## [1.2.0] - 2026-10-23

## [1.1.0] - 2026-10-18
`

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	require.NoError(t, err)
	return string(b)
}

// repoAt is a small repository whose pins and site name release v (a stand-in for the real files).
func repoAt(t *testing.T, v string) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, "docs/audit.md", "- uses: b070nd/stAirCase@"+v+"   # pin\n    version: "+v+"   # pin\n    - V="+v+"   # pin\n")
	write(t, root, "demo/ruleset-fixture.sh", "# (default version: "+v+")\nVERSION=\"${2:-"+v+"}\"\n")
	write(t, root, "docs/testing.md", "(`FIXTURE_VERIFIER_VERSION`, default "+v+"), and the\n")
	write(t, root, "docs/release/marketplace-checklist.md", "Publish from the current release, "+v+" (or the then-current one)\n`packaging/verify-release.sh "+v+"` (checksums)\n")
	page := func(extra string) string {
		return `<a href="https://github.com/b070nd/stAirCase/blob/` + v + `/docs/README.md">docs</a> <a href="guides/` + v + `/install.html">install guide for ` + v + `</a> ` + extra +
			`<footer>These pages describe stAirCase ` + v + `. See <a href="https://github.com/b070nd/stAirCase/blob/` + v + `/LICENSE">MIT</a></footer>`
	}
	write(t, root, "site/index.html", page(""))
	write(t, root, "site/use-cases/ci.html", page("uses: b070nd/stAirCase@"+v+"\n  version: "+v+"      # the verifier"))
	write(t, root, "site/guides/index.html", `<h1>Guides</h1>
<h2 id="v1-2-0">`+v+` (2026-10-23), the latest release</h2>
<ul>
  <li><a href="../guides/`+v+`/install.html">Install</a></li>
</ul>
<h2 id="before">Before you start</h2>
`)
	write(t, root, "site/guides/"+v+"/install.html", "<title>Install "+v+"</title> staircase_"+strings.TrimPrefix(v, "v")+"_linux.tar.gz "+v+" release")
	return root
}

func TestReleases_reads_the_headings_in_order(t *testing.T) {
	got := Releases([]byte(changelog))
	assert.Equal(t, []Release{{"1.3.0", "2026-11-01"}, {"1.2.0", "2026-10-23"}, {"1.1.0", "2026-10-18"}}, got)
}

func TestCheck_names_every_stale_mention(t *testing.T) {
	root := repoAt(t, "v1.2.0")
	assert.Empty(t, Check(root, Release{"1.2.0", "2026-10-23"}))

	problems := Check(root, Release{"1.3.0", "2026-11-01"})
	joined := strings.Join(problems, "\n")
	for _, f := range []string{"docs/audit.md", "demo/ruleset-fixture.sh", "docs/testing.md", "docs/release/marketplace-checklist.md", "site/index.html", "site/use-cases/ci.html"} {
		assert.Contains(t, joined, f, "a stale mention in %s is reported", f)
	}
	assert.Contains(t, joined, "v1.2.0", "the stale version is named")
}

func TestApply_moves_every_current_mention_and_adds_the_guide(t *testing.T) {
	root := repoAt(t, "v1.2.0")
	changed, err := Apply(root, Release{"1.3.0", "2026-11-01"})
	require.NoError(t, err)
	assert.NotEmpty(t, changed)
	assert.Empty(t, Check(root, Release{"1.3.0", "2026-11-01"}), "after Apply nothing is stale")

	assert.Contains(t, read(t, root, "docs/audit.md"), "b070nd/stAirCase@v1.3.0   # pin")
	assert.Contains(t, read(t, root, "demo/ruleset-fixture.sh"), `VERSION="${2:-v1.3.0}"`)
	assert.Contains(t, read(t, root, "site/index.html"), "blob/v1.3.0/docs/README.md")

	// the new release's guide is a copy of the previous one, with its version moved; the previous guide stays as it was
	guide := read(t, root, "site/guides/v1.3.0/install.html")
	assert.Contains(t, guide, "<title>Install v1.3.0</title>")
	assert.Contains(t, guide, "staircase_1.3.0_linux.tar.gz")
	assert.NotContains(t, guide, "1.2.0")
	assert.Contains(t, read(t, root, "site/guides/v1.2.0/install.html"), "<title>Install v1.2.0</title>")

	// the index lists the new guide first, as the latest, and the old one no longer claims it
	index := read(t, root, "site/guides/index.html")
	assert.Less(t, strings.Index(index, `id="v1-3-0"`), strings.Index(index, `id="v1-2-0"`))
	assert.Contains(t, index, "v1.3.0 (2026-11-01), the latest release")
	assert.Contains(t, index, "v1.2.0 (2026-10-23)</h2>")
	assert.Equal(t, 1, strings.Count(index, "the latest release"))
}

func TestApply_is_repeatable(t *testing.T) {
	root := repoAt(t, "v1.2.0")
	_, err := Apply(root, Release{"1.3.0", "2026-11-01"})
	require.NoError(t, err)
	before := read(t, root, "site/guides/index.html")
	changed, err := Apply(root, Release{"1.3.0", "2026-11-01"})
	require.NoError(t, err)
	assert.Empty(t, changed, "a second run changes nothing")
	assert.Equal(t, before, read(t, root, "site/guides/index.html"))
}

func TestCheck_a_frozen_guide_keeps_its_own_version(t *testing.T) {
	root := repoAt(t, "v1.2.0")
	_, err := Apply(root, Release{"1.3.0", "2026-11-01"})
	require.NoError(t, err)
	write(t, root, "site/guides/v1.2.0/install.html", "<title>Install v1.3.0</title>") // a copy that was not finished
	problems := strings.Join(Check(root, Release{"1.3.0", "2026-11-01"}), "\n")
	assert.Contains(t, problems, "site/guides/v1.2.0/install.html")
}

func TestApply_a_pin_that_is_gone_is_an_error(t *testing.T) {
	root := repoAt(t, "v1.2.0")
	write(t, root, "docs/testing.md", "no version here\n")
	_, err := Apply(root, Release{"1.3.0", "2026-11-01"})
	require.ErrorContains(t, err, "docs/testing.md")
}
