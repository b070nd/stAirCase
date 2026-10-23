// Package relversion keeps every place that names the current release in step with the CHANGELOG, which is the one source of the version.
// Apply moves the mentions that mean "the current release" (the pinned Action example, the fixture's default, the site's links and its
// setup guide) and Check reports any that were left behind. Mentions that are history, such as "since 1.2.0", are never touched: only
// the fixed phrases below are.
package relversion

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Release is one heading of the CHANGELOG: "## [1.2.0] - 2026-10-23".
type Release struct{ Version, Date string }

var heading = regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+)\] - (\d{4}-\d{2}-\d{2})`)

// Releases lists the releases the changelog names, newest first (the order it is written in).
func Releases(changelog []byte) []Release {
	var out []Release
	for _, m := range heading.FindAllSubmatch(changelog, -1) {
		out = append(out, Release{Version: string(m[1]), Date: string(m[2])})
	}
	return out
}

// A phrase is text that names a release; %s stands for the tag, such as v1.2.0.
type pin struct {
	File    string
	Phrases []string
}

// pins are files that name the current release in fixed phrases.
var pins = []pin{
	{"docs/audit.md", []string{"uses: b070nd/stAirCase@%s ", "version: %s ", "- V=%s "}},
	{"demo/ruleset-fixture.sh", []string{`VERSION="${2:-%s}"`, "(default version: %s)"}},
	{"docs/testing.md", []string{"default %s)"}},
	{"docs/release/marketplace-checklist.md", []string{"release, %s (or", "verify-release.sh %s`"}},
}

// sitePhrases name the current release in every page of the site except the guide index and the guides of earlier releases.
var sitePhrases = []string{"blob/%s/", "releases/tag/%s", "stAirCase@%s", "version: %s ", "guides/%s/", "stAirCase %s.", "guide for %s"}

// indexPhrases are the only ones the guide index has for the current release: its sections link to each release's own files.
var indexPhrases = []string{"stAirCase %s.", `blob/%s/docs/README.md">documentation in the repository`, `blob/%s/LICENSE"`}

const tagPattern = `v\d+\.\d+\.\d+`

// phraseRegexp matches the phrase with any release tag in place of %s; the tag is capture group 1.
func phraseRegexp(phrase string) *regexp.Regexp {
	before, after, _ := strings.Cut(phrase, "%s")
	return regexp.MustCompile(regexp.QuoteMeta(before) + "(" + tagPattern + ")" + regexp.QuoteMeta(after))
}

// moveTo returns text with every occurrence of the phrase naming tag, and says whether there was one.
func moveTo(text, phrase, tag string) (string, bool) {
	re := phraseRegexp(phrase)
	if !re.MatchString(text) {
		return text, false
	}
	return re.ReplaceAllLiteralString(text, fmt.Sprintf(phrase, tag)), true
}

func mustRead(root, rel string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	return string(b), err
}

func writeFile(root, rel, content string) error {
	return os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(content), 0o644)
}

var guideDir = regexp.MustCompile(`^site/guides/(v\d+\.\d+\.\d+)/`)

// sitePages lists the site's pages as slash paths from root, sorted.
func sitePages(root string) []string {
	var pages []string
	_ = filepath.WalkDir(filepath.Join(root, "site"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".html") {
			rel, _ := filepath.Rel(root, p)
			pages = append(pages, filepath.ToSlash(rel))
		}
		return nil
	})
	slices.Sort(pages)
	return pages
}

// Check reports every mention that names a release other than latest where it should name it, and every guide of an earlier release that
// names another version than its own.
func Check(root string, latest Release) []string {
	tag := "v" + latest.Version
	var problems []string
	check := func(file, text string, phrases []string, want string, mustHold bool) {
		for _, p := range phrases {
			ms := phraseRegexp(p).FindAllStringSubmatch(text, -1)
			if mustHold && len(ms) == 0 {
				problems = append(problems, fmt.Sprintf("%s no longer holds %q: the release steps cannot keep it current", file, p))
			}
			for _, m := range ms {
				if m[1] != want {
					problems = append(problems, fmt.Sprintf("%s names %s where it should name %s (%q)", file, m[1], want, p))
					break
				}
			}
		}
	}
	for _, p := range pins {
		text, err := mustRead(root, p.File)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", p.File, err))
			continue
		}
		check(p.File, text, p.Phrases, tag, true)
	}
	for _, page := range sitePages(root) {
		text, err := mustRead(root, page)
		if err != nil {
			continue
		}
		switch m := guideDir.FindStringSubmatch(page); {
		case m != nil: // a guide is written for one release and keeps naming it
			for _, v := range regexp.MustCompile(tagPattern).FindAllString(text, -1) {
				if v != m[1] {
					problems = append(problems, fmt.Sprintf("%s names %s, but it is the guide for %s", page, v, m[1]))
					break
				}
			}
		case page == "site/guides/index.html":
			check(page, text, indexPhrases, tag, false)
			if !strings.Contains(text, "guides/"+tag+"/install.html") {
				problems = append(problems, fmt.Sprintf("%s does not list the guide for %s", page, tag))
			}
			if strings.Count(text, "the latest release") != 1 {
				problems = append(problems, fmt.Sprintf("%s must call exactly one release the latest", page))
			}
		default:
			check(page, text, sitePhrases, tag, false)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "site", "guides", tag)); err != nil {
		problems = append(problems, fmt.Sprintf("site/guides/%s does not exist", tag))
	}
	return problems
}

// Apply makes every mention that means "the current release" name latest, adds the site's guide for it, and returns the files it changed.
// It is repeatable: a second run changes nothing.
func Apply(root string, latest Release) (changed []string, err error) {
	tag := "v" + latest.Version
	update := func(rel, text string, phrases []string, mustHold bool) error {
		before := text
		for _, p := range phrases {
			next, ok := moveTo(text, p, tag)
			if !ok && mustHold {
				return fmt.Errorf("%s no longer holds %q: add it back or change the phrase in internal/relversion", rel, p)
			}
			text = next
		}
		if text != before {
			if err := writeFile(root, rel, text); err != nil {
				return err
			}
			changed = append(changed, rel)
		}
		return nil
	}
	for _, p := range pins {
		text, err := mustRead(root, p.File)
		if err != nil {
			return changed, fmt.Errorf("%s: %w", p.File, err)
		}
		if err := update(p.File, text, p.Phrases, true); err != nil {
			return changed, err
		}
	}
	guides, err := addGuide(root, latest)
	if err != nil {
		return changed, err
	}
	changed = append(changed, guides...)
	for _, page := range sitePages(root) {
		if guideDir.MatchString(page) {
			continue
		}
		text, err := mustRead(root, page)
		if err != nil {
			return changed, err
		}
		phrases := sitePhrases
		if page == "site/guides/index.html" {
			phrases = indexPhrases
		}
		if err := update(page, text, phrases, false); err != nil {
			return changed, err
		}
	}
	slices.Sort(changed)
	return slices.Compact(changed), nil
}

// guideVersions lists the release guides the site has, as versions without the v, oldest first.
func guideVersions(root string) []string {
	entries, _ := os.ReadDir(filepath.Join(root, "site", "guides"))
	var vs []string
	for _, e := range entries {
		if v, ok := strings.CutPrefix(e.Name(), "v"); ok && e.IsDir() {
			vs = append(vs, v)
		}
	}
	slices.SortFunc(vs, compareVersions)
	return vs
}

func compareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x - y
		}
	}
	return len(pa) - len(pb)
}

const guideEntry = `<h2 id="%s">%s (%s), the latest release</h2>
<ul>
  <li><a href="../guides/%s/install.html">Install and verify a download</a></li>
  <li><a href="../guides/%s/first-run.html">Your first run, and onboarding a project</a></li>
</ul>
<p>The repository's documentation at that release is the reference: <a href="https://github.com/b070nd/stAirCase/blob/%s/docs/README.md">docs at %s</a>, and <a href="https://github.com/b070nd/stAirCase/blob/%s/CHANGELOG.md">what changed in each release</a>.</p>

`

// addGuide makes the guide for latest from the newest earlier guide (a copy with the version moved: the author then checks it against
// the release) and lists it first in the guide index as the latest. A guide that exists is left alone.
func addGuide(root string, latest Release) (changed []string, err error) {
	tag := "v" + latest.Version
	dst := filepath.Join(root, "site", "guides", tag)
	if _, statErr := os.Stat(dst); statErr != nil {
		var prev string
		for _, v := range guideVersions(root) {
			if compareVersions(v, latest.Version) < 0 {
				prev = v
			}
		}
		if prev == "" {
			return nil, fmt.Errorf("site/guides has no guide of an earlier release to copy for %s", tag)
		}
		src := filepath.Join(root, "site", "guides", "v"+prev)
		entries, err := os.ReadDir(src)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return nil, err
		}
		for _, e := range entries {
			b, err := os.ReadFile(filepath.Join(src, e.Name()))
			if err != nil {
				return nil, err
			}
			text := strings.ReplaceAll(string(b), prev, latest.Version)
			if err := os.WriteFile(filepath.Join(dst, e.Name()), []byte(text), 0o644); err != nil {
				return nil, err
			}
			changed = append(changed, "site/guides/"+tag+"/"+e.Name())
		}
	}
	index, err := mustRead(root, "site/guides/index.html")
	if err != nil {
		return changed, err
	}
	id := `id="v` + strings.ReplaceAll(latest.Version, ".", "-") + `"`
	if strings.Contains(index, id) {
		return changed, nil
	}
	first := strings.Index(index, `<h2 id="v`)
	if first < 0 {
		return changed, fmt.Errorf("site/guides/index.html has no release section to put %s before", tag)
	}
	entry := fmt.Sprintf(guideEntry, id[len(`id="`):len(id)-1], tag, latest.Date, tag, tag, tag, tag, tag)
	index = strings.ReplaceAll(index[:first]+entry+index[first:], ", the latest release", "")
	index = strings.Replace(index, tag+" ("+latest.Date+")</h2>", tag+" ("+latest.Date+"), the latest release</h2>", 1)
	if err := writeFile(root, "site/guides/index.html", index); err != nil {
		return changed, err
	}
	return append(changed, "site/guides/index.html"), nil
}
