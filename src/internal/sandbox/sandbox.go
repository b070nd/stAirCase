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
// even to this machine. cleanup removes the temp folder.
func Command(ctx context.Context, root, cwd, command, mode string) (cmd *exec.Cmd, sandboxed bool, cleanup func(), err error) {
	tmp, err := os.MkdirTemp("", "staircase-shell-")
	if err != nil {
		return nil, false, func() {}, err
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }
	var wrap []string
	if mode != Off {
		if wrap, err = sandboxWrapper(root, tmp); err != nil && mode == Required {
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

// sandboxWrapper is the command line that puts a command in the sandbox for
// this machine, writable only in root and tmp, or an error when there is none.
func sandboxWrapper(root, tmp string) ([]string, error) {
	var err error
	if root, err = filepath.EvalSymlinks(root); err != nil {
		return nil, err
	}
	if tmp, err = filepath.EvalSymlinks(tmp); err != nil {
		return nil, err
	}
	switch runtime.GOOS {
	case "darwin":
		bin, err := exec.LookPath("sandbox-exec")
		if err != nil {
			return nil, errors.New("no sandbox: sandbox-exec is not available")
		}
		profile := fmt.Sprintf(`(version 1)
(allow default)
(deny network*)
(deny file-write*)
(allow file-write* (subpath %q) (subpath %q)
  (literal "/dev/null") (literal "/dev/zero") (literal "/dev/tty") (regex #"^/dev/fd/"))`, root, tmp)
		return []string{bin, "-p", profile}, nil
	case "linux":
		// ponytail: untested (no bwrap on the dev Mac, TestShellSandbox runs on macOS only).
		bin, err := exec.LookPath("bwrap")
		if err != nil {
			return nil, errors.New("no sandbox: install bubblewrap (bwrap)")
		}
		return []string{bin, "--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc",
			"--bind", root, root, "--bind", tmp, tmp, "--unshare-net", "--die-with-parent", "--"}, nil
	}
	return nil, fmt.Errorf("no sandbox on %s", runtime.GOOS)
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
