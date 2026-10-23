// Command setversion makes every place that names the current release say the version the CHANGELOG's newest release heading gives: the
// pinned Action example, the fixture's default, the site's links and its setup guide (see internal/relversion). Run it when you prepare
// a release, after the new "## [X.Y.Z] - date" heading is in CHANGELOG.md, and review the diff. With -check it changes nothing and
// fails if a mention is stale.
//
//	go run ./src/tools/setversion          # or: make version
//	go run ./src/tools/setversion -check
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/relversion"
)

func main() {
	check := flag.Bool("check", false, "change nothing; list the mentions that name another release than the latest, and fail if there are any")
	root := flag.String("root", ".", "the repository's root")
	flag.Parse()

	changelog, err := os.ReadFile(filepath.Join(*root, "CHANGELOG.md"))
	if err != nil {
		fail(err)
	}
	releases := relversion.Releases(changelog)
	if len(releases) == 0 {
		fail(fmt.Errorf("CHANGELOG.md has no \"## [X.Y.Z] - date\" heading"))
	}
	latest := releases[0]

	if *check {
		problems := relversion.Check(*root, latest)
		for _, p := range problems {
			fmt.Println(p)
		}
		if len(problems) > 0 {
			os.Exit(1)
		}
		fmt.Printf("every mention of the current release names v%s\n", latest.Version)
		return
	}

	changed, err := relversion.Apply(*root, latest)
	if err != nil {
		fail(err)
	}
	if len(changed) == 0 {
		fmt.Printf("everything already names v%s\n", latest.Version)
	}
	for _, f := range changed {
		fmt.Println("updated", f)
	}
	if len(releases) > 1 {
		created := slices.ContainsFunc(changed, func(f string) bool { return strings.HasPrefix(f, "site/guides/v"+latest.Version+"/") })
		reviewHint(*root, "v"+releases[1].Version, "v"+latest.Version, created)
	}
}

// reviewHint lists the documentation pages the site links to that changed since the previous release: the site's text about them may
// need to change too, and no script can judge that.
func reviewHint(root, prev, tag string, newGuide bool) {
	links := regexp.MustCompile(`blob/` + regexp.QuoteMeta(tag) + `/([^"#]+)`)
	seen := map[string]bool{}
	var pages []string
	_ = filepath.WalkDir(filepath.Join(root, "site"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".html" {
			return nil
		}
		b, _ := os.ReadFile(p)
		for _, m := range links.FindAllStringSubmatch(string(b), -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				pages = append(pages, m[1])
			}
		}
		return nil
	})
	slices.Sort(pages)
	var changed []string
	for _, p := range pages {
		cmd := exec.Command("git", "-C", root, "diff", "--quiet", prev+"..HEAD", "--", p)
		if err := cmd.Run(); err != nil { // exit status 1: it differs
			changed = append(changed, p)
		}
	}
	if newGuide {
		fmt.Printf("\nThe guide for %s is a copy of the guide for %s: check its commands against the release.\n", tag, prev)
	}
	if len(changed) > 0 {
		fmt.Printf("The site links to these pages, which changed since %s; check the site's text about them:\n", prev)
		for _, p := range changed {
			fmt.Println("  " + p)
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "setversion:", err)
	os.Exit(2)
}
