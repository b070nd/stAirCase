// Package agent is stAirCase's own agent runtime: the tools a model may call,
// the model clients, and the topology graph that drives them in-process.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/staircase-core/src/internal/domain"
	"github.com/b070nd/staircase-core/src/internal/orchestrator"
)

// Tool is one function the model may call.
type Tool struct {
	Name        string
	Description string
	Params      json.RawMessage // JSON Schema of the arguments object
	Call        func(ctx context.Context, args json.RawMessage) string
}

func newTool[A any](name, description, params string, run func(context.Context, A) string) Tool {
	return Tool{Name: name, Description: description, Params: json.RawMessage(params),
		Call: func(ctx context.Context, raw json.RawMessage) string {
			var a A
			if err := json.Unmarshal(raw, &a); err != nil {
				return "error: invalid arguments: " + err.Error()
			}
			return run(ctx, a)
		}}
}

const (
	maxReadBytes = 100 * 1024
	shellTimeout = 120 * time.Second
)

// Tools returns the built-in tools of one agent of a run; agent names the
// proposer in audit records. run_shell is offered only when the run allows it.
// Write tools change nothing themselves: the orchestrator derives the approved
// bytes and the tool applies exactly those.
func Tools(env *orchestrator.AgentEnv, agent string) []Tool {
	root := env.Worktree
	edit := func(ctx context.Context, e domain.ProposedEdit, reasoning, done string) string {
		ap := env.ProposeEdit(ctx, agent, reasoning, e)
		if !ap.Approved {
			return ap.Refusal()
		}
		if err := ap.Apply(root); err != nil {
			return "error: " + err.Error()
		}
		return done
	}
	type pathArg struct {
		Path string `json:"path"`
	}
	tools := []Tool{
		newTool("read_file", "Read a source file relative to the project root. Maximum 100 KB.",
			`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`,
			func(_ context.Context, a pathArg) string { return readFile(root, a.Path) }),
		newTool("list_dir", "List files and sub-directories inside a project directory.",
			`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`,
			func(_ context.Context, a pathArg) string { return listDir(root, a.Path) }),
		newTool("request_edit", "Propose an exact search-and-replace edit inside an existing file. No diffs or line numbers. The first occurrence of search_block is replaced; requires approval.",
			`{"type":"object","properties":{"file":{"type":"string"},"search_block":{"type":"string"},"replace_block":{"type":"string"},"reasoning":{"type":"string"}},"required":["file","search_block","replace_block","reasoning"]}`,
			func(ctx context.Context, a struct {
				File         string `json:"file"`
				SearchBlock  string `json:"search_block"`
				ReplaceBlock string `json:"replace_block"`
				Reasoning    string `json:"reasoning"`
			}) string {
				return edit(ctx, domain.ProposedEdit{File: a.File, SearchBlock: a.SearchBlock, ReplaceBlock: a.ReplaceBlock}, a.Reasoning, "applied")
			}),
		newTool("create_file", "Create a new file inside the project root (or overwrite if it already exists). Requires approval; the operator sees the full content. At most 200 KiB.",
			`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"},"reasoning":{"type":"string"}},"required":["path","content","reasoning"]}`,
			func(ctx context.Context, a struct {
				Path      string `json:"path"`
				Content   string `json:"content"`
				Reasoning string `json:"reasoning"`
			}) string {
				return edit(ctx, domain.ProposedEdit{File: a.Path, SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: a.Content}, a.Reasoning, "created "+a.Path)
			}),
		newTool("delete_file", "Delete an existing file inside the project root. Requires approval.",
			`{"type":"object","properties":{"path":{"type":"string"},"reasoning":{"type":"string"}},"required":["path","reasoning"]}`,
			func(ctx context.Context, a struct {
				Path      string `json:"path"`
				Reasoning string `json:"reasoning"`
			}) string {
				return edit(ctx, domain.ProposedEdit{File: a.Path, SearchBlock: orchestrator.MarkerDeleteFile}, a.Reasoning, "deleted "+a.Path)
			}),
	}
	if env.AllowShell {
		tools = append(tools, newTool("run_shell", "Execute a shell command inside the project root after operator approval (never auto-approved). "+
			"Not sandboxed: it runs as the orchestrator's OS user; the operator's approval is the boundary. "+
			"Files it changes are not approved changes and fail the run unless proposed as edits.",
			`{"type":"object","properties":{"command":{"type":"string"},"reasoning":{"type":"string"},"working_dir":{"type":"string"}},"required":["command","reasoning"]}`,
			func(ctx context.Context, a struct {
				Command    string `json:"command"`
				Reasoning  string `json:"reasoning"`
				WorkingDir string `json:"working_dir"`
			}) string {
				if ap := env.ProposeShell(ctx, agent, a.Reasoning, orDefault(a.WorkingDir, "."), a.Command); !ap.Approved {
					return ap.Refusal()
				}
				return runShell(ctx, root, a.WorkingDir, a.Command)
			}))
	}
	return tools
}

// inside resolves path under root, following symlinks, and refuses anything
// that ends up outside it.
func inside(root, path string) (string, error) {
	r, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	full, err := filepath.EvalSymlinks(filepath.Join(r, path))
	if err != nil {
		return "", err
	}
	if full != r && !strings.HasPrefix(full, r+string(filepath.Separator)) {
		return "", errEscape
	}
	return full, nil
}

var errEscape = errors.New("path escapes project root")

func readFile(root, path string) string {
	full, err := inside(root, path)
	switch {
	case errors.Is(err, errEscape):
		return "error: path escapes project root: " + path
	case errors.Is(err, fs.ErrNotExist):
		return "error: file not found: " + path
	case err != nil:
		return fmt.Sprintf("error reading %s: %v", path, err)
	}
	info, err := os.Stat(full)
	switch {
	case err != nil:
		return fmt.Sprintf("error reading %s: %v", path, err)
	case info.IsDir():
		return "error: " + path + " is a directory, not a file"
	case info.Size() > maxReadBytes:
		return fmt.Sprintf("error: %s is too large (%d bytes); use a narrower path", path, info.Size())
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return fmt.Sprintf("error reading %s: %v", path, err)
	}
	return string(b)
}

func listDir(root, path string) string {
	full, err := inside(root, orDefault(path, "."))
	switch {
	case errors.Is(err, errEscape):
		return "error: path escapes project root: " + path
	case errors.Is(err, fs.ErrNotExist):
		return "error: directory not found: " + path
	case err != nil:
		return fmt.Sprintf("error listing %s: %v", path, err)
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		if info, serr := os.Stat(full); serr == nil && !info.IsDir() {
			return "error: " + path + " is not a directory"
		}
		return fmt.Sprintf("error listing %s: %v", path, err)
	}
	sort.SliceStable(entries, func(i, j int) bool { // directories first, then by name
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return entries[i].Name() < entries[j].Name()
	})
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
		if e.IsDir() {
			names[i] += "/"
		}
	}
	if len(names) == 0 {
		return "(empty directory)"
	}
	return strings.Join(names, "\n")
}

// runShell runs an approved command in the worktree with the runtime's
// environment allowlist, returning the exit code and the tails of its output.
func runShell(ctx context.Context, root, dir, command string) string {
	cwd, err := inside(root, orDefault(dir, "."))
	if err != nil {
		return "error: working_dir escapes project root"
	}
	ctx, cancel := context.WithTimeout(ctx, shellTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Dir, cmd.Env = cwd, shellEnv()
	cmd.WaitDelay = 5 * time.Second // a child holding the pipes open cannot hang the tool
	killProcessGroup(cmd)
	stdout, stderr := &tail{max: 4096}, &tail{max: 2048}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err = cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Sprintf("error: command timed out after %d s", int(shellTimeout.Seconds()))
	}
	code := 0
	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	case err != nil:
		return "error running command: " + err.Error()
	}
	out := fmt.Sprintf("exit=%d\n%s", code, stdout)
	if s := stderr.String(); s != "" {
		out += "STDERR: " + s
	}
	return out
}

// shellEnv is what an approved shell command inherits: nothing that could
// carry credentials (SSH agent, cloud or LLM keys) — only what tools need to
// run, find certificates and reach a proxy.
func shellEnv() []string {
	keep := func(k string) bool {
		switch k {
		case "PATH", "HOME", "TMPDIR", "LANG", "TZ", "SSL_CERT_FILE", "SSL_CERT_DIR", "REQUESTS_CA_BUNDLE",
			"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy":
			return true
		}
		return strings.HasPrefix(k, "LC_")
	}
	var env []string
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); keep(k) {
			env = append(env, kv)
		}
	}
	return env
}

// tail keeps the last max bytes written to it.
type tail struct {
	max int
	b   []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = t.b[len(t.b)-t.max:]
	}
	return len(p), nil
}

func (t *tail) String() string { return strings.ToValidUTF8(string(t.b), "") }

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
