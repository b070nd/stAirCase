package engine

import (
	"path"
	"path/filepath"
	"strings"
)

// MatchAny reports whether name matches any of the patterns (see MatchGlob).
func MatchAny(patterns []string, name string) bool {
	for _, p := range patterns {
		if MatchGlob(p, name) {
			return true
		}
	}
	return false
}

// MatchAnyFold is MatchAny in any letter case: the file systems of macOS and
// Windows do not tell "makefile" from "Makefile".
func MatchAnyFold(patterns []string, name string) bool {
	lower := make([]string, len(patterns))
	for i, p := range patterns {
		lower[i] = strings.ToLower(p)
	}
	return MatchAny(lower, strings.ToLower(name))
}

// ValidGlob reports whether pattern is well formed for MatchGlob: not empty, and every
// path segment other than ** a pattern filepath.Match accepts.
func ValidGlob(pattern string) bool {
	if pattern == "" {
		return false
	}
	for _, seg := range strings.Split(filepath.ToSlash(pattern), "/") {
		if seg == "**" {
			continue
		}
		if _, err := filepath.Match(seg, ""); err != nil {
			return false
		}
	}
	return true
}

// MatchGlob matches name against pattern with support for the ** multi-segment
// wildcard used in .gitignore files (which filepath.Match does not support).
// Single-segment patterns fall through to filepath.Match.
func MatchGlob(pattern, name string) bool {
	if !strings.Contains(pattern, "**") {
		m, _ := filepath.Match(pattern, name)
		return m
	}
	// Normalise separators then do component-level matching.
	pattern = filepath.ToSlash(pattern)
	name = filepath.ToSlash(name)
	return globMatchParts(
		strings.Split(pattern, "/"),
		strings.Split(name, "/"),
	)
}

// globMatchParts recursively matches pattern components against name components.
// A "**" component matches zero or more name components.
func globMatchParts(pat, name []string) bool {
	for len(pat) > 0 {
		switch pat[0] {
		case "**":
			if len(pat) == 1 {
				return true // ** at end matches everything remaining
			}
			// Try consuming 0, 1, 2, … name components with **.
			for i := 0; i <= len(name); i++ {
				if globMatchParts(pat[1:], name[i:]) {
					return true
				}
			}
			return false
		default:
			if len(name) == 0 {
				return false
			}
			m, _ := filepath.Match(pat[0], name[0])
			if !m {
				return false
			}
			pat, name = pat[1:], name[1:]
		}
	}
	return len(name) == 0
}

// CleanRepoPath is p cleaned, when it names something inside a repository from its root: not empty, not absolute, not the root itself,
// not outside it. A pattern with ** or * is cleaned the same way.
func CleanRepoPath(p string) (string, bool) {
	clean := path.Clean(p)
	if p == "" || clean == "." || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return clean, true
}
