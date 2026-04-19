package engine

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const tokenBudgetWarn = 100_000

// RepoMap generates a compact textual "repo map" for a repository:
// a directory tree annotated with function/class signatures for known file types.
// Respects both .gitignore and .staircaseignore patterns.
func RepoMap(repoPath string) (string, error) {
	ignorePatterns, ignoreErr := loadIgnorePatterns(repoPath)
	if ignoreErr != nil {
		// Non-fatal: warn but continue with whatever patterns were loaded.
		// A corrupt .gitignore should not prevent the repo map from being built.
		fmt.Fprintf(os.Stderr, "warning: %v\n", ignoreErr)
	}

	var sb strings.Builder
	err := filepath.WalkDir(repoPath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		rel, _ := filepath.Rel(repoPath, path)
		if rel == "." {
			return nil
		}

		if d.IsDir() {
			if shouldIgnoreDir(rel, ignorePatterns) {
				return filepath.SkipDir
			}
			return nil
		}
		if shouldIgnoreFile(rel, ignorePatterns) {
			return nil
		}

		sb.WriteString(rel)
		for _, sig := range extractSignatures(path) {
			sb.WriteString("\n  ")
			sb.WriteString(sig)
		}
		sb.WriteByte('\n')
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk repo: %w", err)
	}
	return sb.String(), nil
}

// PackXML wraps each file's content in Anthropic-optimised XML tags.
// files is a map of relative path → content.
// Path values are XML-attribute escaped so special characters in directory
// names do not break the document structure.
func PackXML(files map[string]string) string {
	var sb strings.Builder
	for path, content := range files {
		fmt.Fprintf(&sb, "<file path=\"%s\">\n%s\n</file>\n", xmlEscape(path), content)
	}
	return sb.String()
}

// xmlEscape escapes characters that are invalid inside an XML attribute value.
func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// TokenEstimate returns a fast token count estimate (4 chars ≈ 1 token heuristic).
func TokenEstimate(text string) int {
	return len(text) / 4
}

// WarnTokenBudget returns a non-empty warning string if the estimated token
// count exceeds the 100k threshold that warrants user confirmation.
func WarnTokenBudget(text string) string {
	est := TokenEstimate(text)
	if est > tokenBudgetWarn {
		return fmt.Sprintf("⚠️  Context is ~%dk tokens (exceeds 100k) — this may be slow or costly.", est/1000)
	}
	return ""
}

// ─── Ignore patterns ─────────────────────────────────────────────────────────

var defaultIgnoreDirs = map[string]bool{
	".git": true, "node_modules": true, "__pycache__": true,
	".venv": true, "venv": true, ".mypy_cache": true,
	"dist": true, "build": true, "target": true, "bin": true,
	".idea": true, ".vscode": true, ".staircase-workspace": true,
}

var defaultIgnoreExts = map[string]bool{
	".lock": true, ".sum": true, ".png": true, ".jpg": true,
	".jpeg": true, ".gif": true, ".ico": true, ".woff": true,
	".woff2": true, ".ttf": true, ".eot": true, ".pdf": true,
	".zip": true, ".tar": true, ".gz": true, ".exe": true,
	".bin": true, ".so": true, ".dylib": true,
}

func loadIgnorePatterns(repoPath string) ([]string, error) {
	var patterns []string
	var errs []string
	for _, name := range []string{".gitignore", ".staircaseignore"} {
		f, err := os.Open(filepath.Join(repoPath, name))
		if err != nil {
			if !os.IsNotExist(err) {
				// File exists but is unreadable — record the error so the
				// caller can surface it rather than silently skipping patterns.
				errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			}
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line != "" && !strings.HasPrefix(line, "#") {
				patterns = append(patterns, line)
			}
		}
		f.Close()
		if err := sc.Err(); err != nil {
			errs = append(errs, fmt.Sprintf("%s: read error: %v", name, err))
		}
	}
	if len(errs) > 0 {
		return patterns, fmt.Errorf("loadIgnorePatterns: %s", strings.Join(errs, "; "))
	}
	return patterns, nil
}

func shouldIgnoreDir(rel string, patterns []string) bool {
	base := filepath.Base(rel)
	if defaultIgnoreDirs[base] {
		return true
	}
	rel = filepath.ToSlash(rel)
	for _, p := range patterns {
		p = strings.TrimSuffix(p, "/")
		if matchGlob(p, base) || matchGlob(p, rel) {
			return true
		}
	}
	return false
}

func shouldIgnoreFile(rel string, patterns []string) bool {
	ext := strings.ToLower(filepath.Ext(rel))
	if defaultIgnoreExts[ext] {
		return true
	}
	base := filepath.Base(rel)
	// Normalise to forward slashes for consistent pattern matching.
	rel = filepath.ToSlash(rel)
	for _, p := range patterns {
		if matchGlob(p, base) || matchGlob(p, rel) {
			return true
		}
	}
	return false
}

// matchGlob matches name against pattern with support for the ** multi-segment
// wildcard used in .gitignore files (which filepath.Match does not support).
// Single-segment patterns fall through to filepath.Match.
func matchGlob(pattern, name string) bool {
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

// ─── Signature extraction ─────────────────────────────────────────────────────

var (
	reFuncGo      = regexp.MustCompile(`^func\s`)
	reFuncPython  = regexp.MustCompile(`^(?:async\s+)?def\s+\w`)
	reClassPython = regexp.MustCompile(`^class\s+\w`)
	reFuncTS      = regexp.MustCompile(`^(?:export\s+)?(?:async\s+)?function\s+\w|^\s*(?:export\s+)?(?:const|let)\s+\w+\s*=\s*(?:async\s+)?\(`)
)

func extractSignatures(path string) []string {
	ext := strings.ToLower(filepath.Ext(path))

	var matchers []*regexp.Regexp
	switch ext {
	case ".go":
		matchers = []*regexp.Regexp{reFuncGo}
	case ".py":
		matchers = []*regexp.Regexp{reFuncPython, reClassPython}
	case ".ts", ".tsx", ".js", ".jsx":
		matchers = []*regexp.Regexp{reFuncTS}
	default:
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var sigs []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		for _, re := range matchers {
			if re.MatchString(line) {
				s := strings.TrimSpace(line)
				if len(s) > 120 {
					s = s[:120] + "…"
				}
				sigs = append(sigs, s)
				break
			}
		}
	}
	return sigs
}
