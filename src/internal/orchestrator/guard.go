package orchestrator

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Guards send a file change to a person when it adds something risky, even
// when a policy rule or the validator would approve it. They can only make a
// decision stricter. Only what the change adds counts: something already in
// the file before is not flagged again.

// hiddenUnicode are characters that make code read differently from how it
// runs ("Trojan Source"): bidirectional controls and zero-width characters.
var hiddenUnicode = regexp.MustCompile("[\u202a-\u202e\u2066-\u2069\u200b-\u200d\u2060\ufeff]")

// secretPatterns look like credentials written into code.
var secretPatterns = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----|AKIA[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{40,}|xox[abprs]-[A-Za-z0-9-]{10,}|sk-(ant-)?[A-Za-z0-9_-]{20,}|AIza[0-9A-Za-z_-]{35}`)

// dependencyFiles are manifests and lock files of common package managers.
var dependencyFiles = []string{"go.mod", "go.sum", "package.json", "package-lock.json", "yarn.lock", "pnpm-lock.yaml",
	"requirements.txt", "pyproject.toml", "poetry.lock", "Pipfile", "Pipfile.lock", "uv.lock", "Cargo.toml", "Cargo.lock",
	"Gemfile", "Gemfile.lock", "composer.json", "composer.lock", "pom.xml", "build.gradle", "build.gradle.kts", "Package.swift"}

// guard says why a person must decide the change next, or "" when no guard
// applies. before gives a path's content before the change (nil if new).
func guard(next map[string]*approvedFile, before func(p string) *approvedFile) string {
	var why []string
	add := func(reason string) {
		if !slices.Contains(why, reason) {
			why = append(why, reason)
		}
	}
	for p, f := range next {
		if base := path.Base(p); slices.Contains(dependencyFiles, base) || strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt") {
			add("it changes dependencies (" + p + ")")
		}
		if f.deleted {
			continue
		}
		old := ""
		if b := before(p); b != nil && !b.deleted {
			old = string(b.content)
		}
		if !utf8.Valid(f.content) { // a person cannot read it, so no rule, task or model approves it
			add(fmt.Sprintf("it changes a file that is not text and cannot be reviewed (%s, %d bytes)", p, len(f.content)))
		}
		if addsMatch(hiddenUnicode, old, string(f.content)) {
			add("it adds hidden Unicode characters that make code read differently from how it runs (" + p + ")")
		}
		if addsMatch(secretPatterns, old, string(f.content)) {
			add("it writes what looks like a secret (" + p + ")")
		}
	}
	slices.Sort(why)
	return strings.Join(why, "; ")
}

// addsMatch reports whether new has a match of re that old did not have.
func addsMatch(re *regexp.Regexp, old, new string) bool {
	for _, m := range re.FindAllString(new, -1) {
		if strings.Count(new, m) > strings.Count(old, m) {
			return true
		}
	}
	return false
}
