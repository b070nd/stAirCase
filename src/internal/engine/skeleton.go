package engine

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

const tokenBudgetWarn = 100_000

// RepoMap generates a compact textual "repo map" for a repository:
// a directory tree annotated with function/class signatures for known file types.
// Respects both .gitignore and .staircaseignore patterns.
//
// In a git repository with a commit the map is of that commit (HEAD): agents
// work on the last commit, so files that were never committed and edits that
// were not are not in it. Anywhere else it is of the folder as it is.
func RepoMap(repoPath string) (string, error) {
	if m, ok, err := repoMapAtHead(repoPath); ok {
		return m, err
	}
	return repoMapOfFolder(repoPath)
}

func repoMapOfFolder(repoPath string) (string, error) {
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
		if shouldIgnoreFile(rel, ignorePatterns) || !d.Type().IsRegular() { // a link could lead outside the repository
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

// repoMapAtHead is the map of repoPath's last commit; ok is false when it has
// none (not a git repository, or no commit yet).
func repoMapAtHead(repoPath string) (m string, ok bool, err error) {
	git := func(stdin string, args ...string) ([]byte, error) {
		cmd := exec.Command("git", append([]string{"-C", repoPath, "-c", "core.hooksPath=/dev/null"}, args...)...)
		cmd.Stdin = strings.NewReader(stdin)
		return cmd.Output()
	}
	if _, err := git("", "rev-parse", "--verify", "-q", "HEAD^{commit}"); err != nil {
		return "", false, nil
	}
	tree, err := git("", "ls-tree", "-r", "-z", "HEAD")
	if err != nil {
		return "", true, fmt.Errorf("list the last commit: %w", err)
	}
	type blob struct{ path, sha string }
	var blobs []blob
	for _, entry := range strings.Split(strings.TrimRight(string(tree), "\x00"), "\x00") {
		meta, path, found := strings.Cut(entry, "\t")
		f := strings.Fields(meta)
		if !found || len(f) != 3 || f[1] != "blob" || (f[0] != "100644" && f[0] != "100755") {
			continue // a symbolic link or a submodule is not a file of the repository
		}
		blobs = append(blobs, blob{path, f[2]})
	}
	sort.Slice(blobs, func(i, j int) bool { // as a walk lists them: by name, folder by folder
		return slices.Compare(strings.Split(blobs[i].path, "/"), strings.Split(blobs[j].path, "/")) < 0
	})
	// one git process serves every blob read, however many source files there are
	var want []string
	for _, b := range blobs {
		if matchersFor(b.path) != nil || b.path == ".gitignore" || b.path == ".staircaseignore" {
			want = append(want, b.sha)
		}
	}
	contents := readBlobs(repoPath, want)
	content := func(sha string) []byte { return contents[sha] }
	var patterns []string
	for _, name := range []string{".gitignore", ".staircaseignore"} {
		for _, b := range blobs {
			if b.path == name {
				for _, line := range strings.Split(string(content(b.sha)), "\n") {
					if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
						patterns = append(patterns, line)
					}
				}
			}
		}
	}
	var sb strings.Builder
	for _, b := range blobs {
		if ignoredInTree(b.path, patterns) {
			continue
		}
		sb.WriteString(b.path)
		if matchersFor(b.path) != nil { // only source files are read
			for _, sig := range signaturesOf(b.path, content(b.sha)) {
				sb.WriteString("\n  ")
				sb.WriteString(sig)
			}
		}
		sb.WriteByte('\n')
	}
	return sb.String(), true, nil
}

// ignoredInTree applies the ignore rules to a file by its path in a tree: its
// folders as a walk would have, then the file itself.
// readBlobs reads the blobs of the given ids with a single `git cat-file --batch`.
func readBlobs(repoPath string, shas []string) map[string][]byte {
	out := map[string][]byte{}
	if len(shas) == 0 {
		return out
	}
	cmd := exec.Command("git", "-C", repoPath, "-c", "core.hooksPath=/dev/null", "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(strings.Join(shas, "\n") + "\n")
	raw, err := cmd.Output()
	if err != nil {
		return out
	}
	r := bufio.NewReader(bytes.NewReader(raw))
	for range shas {
		header, err := r.ReadString('\n')
		if err != nil {
			break
		}
		f := strings.Fields(header) // "<sha> blob <size>"
		if len(f) != 3 {
			continue // "<sha> missing"
		}
		size, err := strconv.Atoi(f[2])
		if err != nil {
			break
		}
		b := make([]byte, size+1) // the content and the line feed after it
		if _, err := io.ReadFull(r, b); err != nil {
			break
		}
		out[f[0]] = b[:size]
	}
	return out
}

func ignoredInTree(path string, patterns []string) bool {
	parts := strings.Split(path, "/")
	for i := 1; i < len(parts); i++ {
		if shouldIgnoreDir(strings.Join(parts[:i], "/"), patterns) {
			return true
		}
	}
	return shouldIgnoreFile(path, patterns)
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
		return fmt.Sprintf("⚠️  Context is ~%dk tokens (exceeds 100k) - this may be slow or costly.", est/1000)
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
				// File exists but is unreadable - record the error so the
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
		_ = f.Close()
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
		if MatchGlob(p, base) || MatchGlob(p, rel) {
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
		if MatchGlob(p, base) || MatchGlob(p, rel) {
			return true
		}
	}
	return false
}

// ─── Signature extraction ─────────────────────────────────────────────────────

var (
	reFuncGo      = regexp.MustCompile(`^func\s`)
	reFuncPython  = regexp.MustCompile(`^(?:async\s+)?def\s+\w`)
	reClassPython = regexp.MustCompile(`^class\s+\w`)
	reFuncTS      = regexp.MustCompile(`^(?:export\s+)?(?:async\s+)?function\s+\w|^\s*(?:export\s+)?(?:const|let)\s+\w+\s*=\s*(?:async\s+)?\(`)
)

func extractSignatures(path string) []string {
	if matchersFor(path) == nil {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return signaturesOf(path, b)
}

func matchersFor(path string) []*regexp.Regexp {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return []*regexp.Regexp{reFuncGo}
	case ".py":
		return []*regexp.Regexp{reFuncPython, reClassPython}
	case ".ts", ".tsx", ".js", ".jsx":
		return []*regexp.Regexp{reFuncTS}
	}
	return nil
}

// signaturesOf are the function and class lines of a file's content, by its name.
func signaturesOf(path string, content []byte) []string {
	matchers := matchersFor(path)
	if matchers == nil {
		return nil
	}
	var sigs []string
	sc := bufio.NewScanner(bytes.NewReader(content))
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
