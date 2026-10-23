package site

import (
	"fmt"
	"html"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

var (
	comment = regexp.MustCompile(`(?s)<!--.*?-->`)
	tagRe   = regexp.MustCompile(`<(/?)([a-zA-Z][a-zA-Z0-9-]*)((?:[^>"']|"[^"]*"|'[^']*')*)>`)
	emDash  = regexp.MustCompile("(?i)\u2014|&mdash;|&#8212;|&#x2014;")
	enDash  = regexp.MustCompile("(?i)\u2013|&ndash;|&#8211;|&#x2013;")
	title   = regexp.MustCompile(`(?is)<title>\s*[^\s<][^<]*</title>`)
	attrRe  = regexp.MustCompile(`([a-zA-Z_:][-a-zA-Z0-9_:.]*)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>]+)))?`)
)

// tag is one start or end tag of a page.
type tag struct {
	end   bool
	name  string
	attrs map[string]string // a present attribute without a value maps to ""
}

// tags reads the tags of an HTML page, in order, leaving out comments.
func tags(src string) []tag {
	var out []tag
	for _, m := range tagRe.FindAllStringSubmatch(comment.ReplaceAllString(src, ""), -1) {
		t := tag{end: m[1] == "/", name: strings.ToLower(m[2]), attrs: map[string]string{}}
		for _, a := range attrRe.FindAllStringSubmatch(m[3], -1) {
			t.attrs[strings.ToLower(a[1])] = a[2] + a[3] + a[4]
		}
		out = append(out, t)
	}
	return out
}

// checkSite checks every page under root and returns one line per problem.
func checkSite(root string) []string {
	var problems []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(path, ".go") {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if emDash.Match(src) {
			problems = append(problems, rel+": an em dash: write a hyphen")
		}
		if enDash.Match(src) {
			problems = append(problems, rel+": an en dash: write a hyphen")
		}
		if !strings.HasSuffix(path, ".html") {
			return nil
		}
		ts := tags(string(src))
		for _, p := range append(checkPage(string(src), ts), checkLinks(root, rel, ts)...) {
			problems = append(problems, rel+": "+p)
		}
		return nil
	})
	return problems
}

// external is a link with a scheme (https:, mailto:) or a host of its own.
var external = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9+.-]*:|//)`)

// checkLinks checks that every internal link of the page at rel (a slash path under root) names a file of the
// site, and, with a fragment, an id on that page.
func checkLinks(root, rel string, ts []tag) []string {
	var problems []string
	for _, t := range ts {
		if t.end {
			continue
		}
		link, ok := t.attrs["href"]
		if t.name == "img" || t.name == "script" {
			link, ok = t.attrs["src"]
		} else if t.name != "a" && t.name != "link" {
			continue
		}
		link = html.UnescapeString(link)
		if !ok || external.MatchString(link) {
			continue
		}
		target, frag, _ := strings.Cut(link, "#")
		file := rel
		if target != "" {
			file = path.Clean(path.Join(path.Dir(rel), target))
			if file == ".." || strings.HasPrefix(file, "../") {
				problems = append(problems, fmt.Sprintf("link %q: outside the site", link))
				continue
			}
			if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(file))); err == nil && info.IsDir() {
				file = path.Join(file, "index.html")
			}
			if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(file))); err != nil || info.IsDir() {
				problems = append(problems, fmt.Sprintf("link %q: no such file", link))
				continue
			}
		}
		if frag == "" || !strings.HasSuffix(file, ".html") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			problems = append(problems, fmt.Sprintf("link %q: %v", link, err))
			continue
		}
		found := false
		for _, other := range tags(string(src)) {
			found = found || (!other.end && other.attrs["id"] == frag)
		}
		if !found {
			problems = append(problems, fmt.Sprintf("link %q: no id %q", link, frag))
		}
	}
	return problems
}

// checkPage checks one page on its own: its source and its tags.
func checkPage(src string, ts []tag) []string {
	var problems []string
	lang := ""
	for _, t := range ts {
		if t.name == "html" && !t.end {
			lang = strings.TrimSpace(t.attrs["lang"])
		}
	}
	if lang == "" {
		problems = append(problems, "no lang attribute on <html>")
	}
	if !title.MatchString(src) {
		problems = append(problems, "no <title>, or an empty one")
	}
	viewport := false
	for _, t := range ts {
		viewport = viewport || (t.name == "meta" && t.attrs["name"] == "viewport" && strings.Contains(t.attrs["content"], "width=device-width"))
	}
	if !viewport {
		problems = append(problems, "no viewport meta (width=device-width): the page would not fit a narrow screen")
	}
	for _, t := range ts {
		if t.name == "img" && !t.end && strings.TrimSpace(t.attrs["alt"]) == "" {
			problems = append(problems, fmt.Sprintf("<img src=%q> has no alt text", t.attrs["src"]))
		}
	}
	h1s, prev := 0, 0
	for _, t := range ts {
		if t.end || len(t.name) != 2 || t.name[0] != 'h' || t.name[1] < '1' || t.name[1] > '6' {
			continue
		}
		level := int(t.name[1] - '0')
		switch {
		case level == 1:
			h1s++
		case prev == 0:
			problems = append(problems, fmt.Sprintf("<%s> before the h1", t.name))
		case level > prev+1:
			problems = append(problems, fmt.Sprintf("<%s> follows <h%d>: a heading level is skipped", t.name, prev))
		}
		prev = level
	}
	if h1s != 1 {
		problems = append(problems, fmt.Sprintf("%d h1 headings, want exactly 1", h1s))
	}
	return problems
}

// writeSite writes a throwaway site: path to content.
func writeSite(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// page is a valid page around body.
func page(body string) string {
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>T</title></head><body>` + body + `</body></html>`
}

// The check finds each kind of problem, and names the page it is on.
func TestCheckFindsEachProblem(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"no lang", map[string]string{"index.html": `<!doctype html><html><head><title>T</title></head><body><h1>A</h1></body></html>`}, "index.html: no lang attribute"},
		{"empty lang", map[string]string{"index.html": `<!doctype html><html lang=""><head><title>T</title></head><body><h1>A</h1></body></html>`}, "index.html: no lang attribute"},
		{"no h1", map[string]string{"index.html": page(`<h2>A</h2>`)}, "index.html: 0 h1 headings, want exactly 1"},
		{"two h1", map[string]string{"a/b.html": page(`<h1>A</h1><h1>B</h1>`)}, "a/b.html: 2 h1 headings, want exactly 1"},
		{"skipped level", map[string]string{"index.html": page(`<h1>A</h1><h3>B</h3>`)}, "index.html: <h3> follows <h1>"},
		{"heading before h1", map[string]string{"index.html": page(`<h2>A</h2><h1>B</h1>`)}, "index.html: <h2> before the h1"},
		{"img without alt", map[string]string{"index.html": page(`<h1>A</h1><img src="a.svg">`), "a.svg": "<svg/>"}, `index.html: <img src="a.svg"> has no alt text`},
		{"img with empty alt", map[string]string{"index.html": page(`<h1>A</h1><img src="a.svg" alt=" ">`), "a.svg": "<svg/>"}, `index.html: <img src="a.svg"> has no alt text`},
		{"em dash in a page", map[string]string{"index.html": page("<h1>A \u2014 B</h1>")}, "index.html: an em dash"},
		{"em dash as an entity", map[string]string{"index.html": page("<h1>A &mdash; B</h1>")}, "index.html: an em dash"},
		{"em dash in the stylesheet", map[string]string{"index.html": page("<h1>A</h1>"), "style.css": "/* a \u2014 b */"}, "style.css: an em dash"},
		{"en dash in a page", map[string]string{"index.html": page("<h1>A \u2013 B</h1>")}, "index.html: an en dash"},
		{"en dash as an entity", map[string]string{"index.html": page("<h1>A &ndash; B</h1>")}, "index.html: an en dash"},
		{"en dash as a numeric entity", map[string]string{"index.html": page("<h1>A &#8211; B</h1>")}, "index.html: an en dash"},
		{"en dash in the stylesheet", map[string]string{"index.html": page("<h1>A</h1>"), "style.css": "/* a \u2013 b */"}, "style.css: an en dash"},
		{"link to a missing page", map[string]string{"index.html": page(`<h1>A</h1><a href="missing.html">x</a>`)}, `index.html: link "missing.html": no such file`},
		{"link to a missing stylesheet", map[string]string{"index.html": strings.Replace(page(`<h1>A</h1>`), "<head>", `<head><link rel="stylesheet" href="style.css">`, 1)}, `index.html: link "style.css": no such file`},
		{"image that is not there", map[string]string{"index.html": page(`<h1>A</h1><img src="a.svg" alt="A">`)}, `index.html: link "a.svg": no such file`},
		{"link out of the site", map[string]string{"a/index.html": page(`<h1>A</h1><a href="../../x.html">x</a>`)}, `a/index.html: link "../../x.html": outside the site`},
		{"missing fragment here", map[string]string{"index.html": page(`<h1 id="top">A</h1><a href="#nope">x</a>`)}, `index.html: link "#nope": no id "nope"`},
		{"missing fragment there", map[string]string{"index.html": page(`<h1>A</h1><a href="b.html#nope">x</a>`), "b.html": page(`<h1 id="yes">B</h1>`)}, `index.html: link "b.html#nope": no id "nope"`},
		{"no viewport", map[string]string{"index.html": strings.Replace(page(`<h1>A</h1>`), `<meta name="viewport" content="width=device-width, initial-scale=1">`, "", 1)}, "index.html: no viewport meta"},
		{"no title", map[string]string{"index.html": strings.Replace(page(`<h1>A</h1>`), "<title>T</title>", "", 1)}, "index.html: no <title>"},
		{"directory without an index", map[string]string{"index.html": page(`<h1>A</h1><a href="guides/">x</a>`), "guides/a.html": page(`<h1>G</h1>`)}, `index.html: link "guides/": no such file`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := checkSite(writeSite(t, c.files))
			if !strings.Contains(strings.Join(got, "\n"), c.want) {
				t.Errorf("want a problem %q, got %q", c.want, got)
			}
		})
	}
}

// A site whose pages and links are all in order has no problems.
func TestCheckPassesAValidSite(t *testing.T) {
	root := writeSite(t, map[string]string{
		"index.html": strings.Replace(page(`<h1 id="top">A</h1><h2>B</h2><h3>C</h3><h2>D</h2>
			<a href="#top">top</a> <a href="guides/">guides</a> <a href="guides/v1.html#install">install</a>
			<a href="https://example.com/x.html">out</a> <a href="mailto:a@example.com">mail</a>
			<img src="run.svg" alt="How a run works"><!-- <a href="gone.html"> -->`),
			"<head>", `<head><link rel="stylesheet" href="style.css">`, 1),
		"style.css":         "body { color: black; }",
		"run.svg":           "<svg/>",
		"guides/index.html": page(`<h1>Guides</h1><a href="../index.html">home</a> <a href="./v1.html">v1</a>`),
		"guides/v1.html":    page(`<h1>V1</h1><h2 id="install">Install</h2>`),
	})
	if got := checkSite(root); len(got) != 0 {
		t.Errorf("want no problems, got %q", got)
	}
}

// pages is every page the site has. A page added or removed is named here, so the check cannot pass on a site
// that lost its pages.
var pages = []string{
	"index.html",
	"assurance.html",
	"sample.html",
	"use-cases/governed-runs.html",
	"use-cases/away.html",
	"use-cases/review-and-seal.html",
	"use-cases/ci.html",
	"use-cases/teams.html",
	"use-cases/evidence.html",
	"guides/index.html",
}

// guideDir is the folder of one release's setup guide; each holds the same two pages.
var guideDir = regexp.MustCompile(`^guides/(v\d+\.\d+\.\d+)/`)

var guidePages = []string{"install.html", "first-run.html"}

// Every page of the site passes the check.
func TestSite(t *testing.T) {
	var found []string
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".html") {
			found = append(found, filepath.ToSlash(path))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := append([]string(nil), pages...)
	for _, f := range found { // every release's guide holds both its pages: a missing one is reported
		if m := guideDir.FindStringSubmatch(f); m != nil {
			for _, g := range guidePages {
				want = append(want, "guides/"+m[1]+"/"+g)
			}
		}
	}
	sort.Strings(want)
	want = slices.Compact(want)
	sort.Strings(found)
	if strings.Join(found, "\n") != strings.Join(want, "\n") {
		t.Errorf("the site's pages are\n%s\nwant\n%s", strings.Join(found, "\n"), strings.Join(want, "\n"))
	}
	for _, p := range checkSite(".") {
		t.Error(p)
	}
}
