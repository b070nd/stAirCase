package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Modes (staircase run --sandbox).
const (
	Auto     = "auto"     // sandbox when this machine can, else run and say so
	Required = "required" // refuse a command that cannot be sandboxed
	Off      = "off"      // no sandbox
)

// Command builds a command to run in cwd inside the worktree
// root. In the sandbox (macOS sandbox-exec, Linux bwrap) it can write only in
// root and in a temp folder of its own (its TMPDIR), and has no network, not
// even to this machine. It cannot read credentials in the home folder
// (Credentials) or the paths in hide (such as the stAirCase workspace),
// except root itself. cleanup removes the temp folder.
func Command(ctx context.Context, root, cwd, command, mode string, hide ...string) (cmd *exec.Cmd, sandboxed bool, cleanup func(), err error) {
	tmp, err := os.MkdirTemp("", "staircase-shell-")
	if err != nil {
		return nil, false, func() {}, err
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }
	var wrap []string
	if mode != Off {
		if wrap, err = sandboxWrapper(root, tmp, hidden(hide)); err != nil && mode == Required {
			cleanup()
			return nil, false, func() {}, err
		}
	}
	args := append(wrap, "/bin/sh", "-c", command)
	cmd = exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = cwd
	cmd.Env = append(Env(), "TMPDIR="+tmp)
	return cmd, wrap != nil, cleanup, nil
}

// Credentials are paths under the home folder that hold keys and tokens.
// ponytail: a list, not an allowlist of reads (tools read all over the
// system); add a path here when a tool keeps its tokens elsewhere.
var Credentials = []string{".ssh", ".aws", ".azure", ".gnupg", ".kube", ".docker", ".netrc", ".git-credentials",
	".npmrc", ".pypirc", ".config/gh", ".config/gcloud", ".config/git", "Library/Keychains",
	// the agents' own login stores
	".claude", ".claude.json", ".codex", ".gemini", ".config/opencode", ".local/share/opencode"}

// hidden are the existing, resolved paths to hide: the credentials under the
// home folder and hide.
func hidden(hide []string) []string {
	var paths []string
	if home, err := os.UserHomeDir(); err == nil {
		for _, c := range Credentials {
			paths = append(paths, filepath.Join(home, c))
		}
	}
	var out []string
	for _, p := range append(paths, hide...) {
		if p == "" {
			continue
		}
		if real, err := filepath.EvalSymlinks(p); err == nil {
			out = append(out, real)
		}
	}
	return out
}

// engine is one way to sandbox a command: its command-line prefix, or why
// it cannot run here.
type engine struct {
	name string
	wrap func(root, tmp string, hide []string) ([]string, error)
}

// engines are this machine's sandboxes, most tested first.
var engines = map[string][]engine{
	"darwin": {{"sandbox-exec", sandboxExec}},
	"linux":  {{"bwrap", bubblewrap}, {"landlock", landlockWrapper}},
}

// only, when set, limits the engines to one (tests).
var only string

// sandboxWrapper is the command line that puts a command in the sandbox for
// this machine, writable only in root and tmp, the hide paths unreadable
// (root stays readable inside them), or an error when there is none.
func sandboxWrapper(root, tmp string, hide []string) ([]string, error) {
	var err error
	if root, err = filepath.EvalSymlinks(root); err != nil {
		return nil, err
	}
	if tmp, err = filepath.EvalSymlinks(tmp); err != nil {
		return nil, err
	}
	why := []string{}
	for _, e := range engines[runtime.GOOS] {
		if only != "" && e.name != only {
			continue
		}
		w, err := e.wrap(root, tmp, hide)
		if err == nil {
			return w, nil
		}
		why = append(why, err.Error())
	}
	if len(why) == 0 {
		why = append(why, "none for "+runtime.GOOS)
	}
	return nil, errors.New("no sandbox: " + strings.Join(why, "; "))
}

func sandboxExec(root, tmp string, hide []string) ([]string, error) {
	bin, err := exec.LookPath("sandbox-exec")
	if err != nil {
		return nil, errors.New("sandbox-exec is not available")
	}
	profile := fmt.Sprintf(`(version 1)
(allow default)
(deny network*)
(deny file-write*)
(allow file-write* (subpath %q) (subpath %q)
  (literal "/dev/null") (literal "/dev/zero") (literal "/dev/tty") (regex #"^/dev/fd/"))`, root, tmp)
	for _, h := range hide {
		profile += fmt.Sprintf("\n(deny file-read* (subpath %q))", h)
	}
	profile += fmt.Sprintf("\n(allow file-read* (subpath %q) (subpath %q))", root, tmp) // later rules win
	return []string{bin, "-p", profile}, nil
}

// bubblewrap is tested in a Linux container (bubblewrap 0.12, arm64).
func bubblewrap(root, tmp string, hide []string) ([]string, error) {
	bin, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, errors.New("install bubblewrap (bwrap)")
	}
	if err := bwrapUsable(bin); err != nil {
		return nil, fmt.Errorf("bwrap cannot run here: %w", err)
	}
	args := []string{bin, "--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc"}
	for _, h := range hide { // an empty folder or file in its place; root is bound back below
		if info, err := os.Stat(h); err == nil && info.IsDir() {
			args = append(args, "--tmpfs", h)
		} else if err == nil {
			args = append(args, "--ro-bind", "/dev/null", h)
		}
	}
	return append(args, "--bind", root, root, "--bind", tmp, tmp, "--unshare-net", "--die-with-parent", "--"), nil
}

// Env is what a command inherits: nothing that could
// carry credentials (SSH agent, cloud or LLM keys) - only what tools need to
// run, find certificates and reach a proxy.
func Env() []string {
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

var bwrapChecked sync.Map // bwrap path → error (nil: it runs)

// bwrapUsable reports whether bin can set up a sandbox here, once per path.
func bwrapUsable(bin string) error {
	if err, ok := bwrapChecked.Load(bin); ok {
		e, _ := err.(error)
		return e
	}
	var err error
	if out, rerr := exec.Command(bin, "--ro-bind", "/", "/", "--unshare-net", "--", "/bin/true").CombinedOutput(); rerr != nil {
		err = fmt.Errorf("%w: %s", rerr, strings.TrimSpace(string(out)))
	}
	bwrapChecked.Store(bin, err)
	return err
}

// readable covers the file system except the hide paths: each hidden path's
// ancestors are replaced by their other entries.
// ponytail: entries created after the command starts next to a hidden path
// (for example a new folder in the home folder) are not readable.
func readable(hide []string) []string {
	allowed := map[string]bool{"/": true}
	for _, h := range hide {
		for a := range allowed {
			if within(h, a) {
				delete(allowed, a) // already expanded inside h
			}
		}
		anc := ""
		for a := range allowed {
			if a == h || within(a, h) {
				anc = a
				break
			}
		}
		if anc == "" {
			continue
		}
		delete(allowed, anc)
		for cur := anc; cur != h; {
			first, _, _ := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(h, cur), "/"), "/")
			next := filepath.Join(cur, first)
			entries, _ := os.ReadDir(cur)
			for _, e := range entries {
				if p := filepath.Join(cur, e.Name()); p != next {
					allowed[p] = true
				}
			}
			cur = next
		}
	}
	out := make([]string, 0, len(allowed))
	for a := range allowed {
		out = append(out, a)
	}
	return out
}

// within reports whether p is strictly inside dir.
func within(dir, p string) bool {
	if dir == "/" {
		return p != "/" && strings.HasPrefix(p, "/")
	}
	return strings.HasPrefix(p, dir+"/")
}
