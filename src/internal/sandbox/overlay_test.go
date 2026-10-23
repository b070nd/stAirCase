package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// userCheckout is the user's own checkout with an ignored directory, and root is a throwaway checkout of a commit that has none of it.
func userCheckout(t *testing.T) (source, root string) {
	t.Helper()
	source, root = t.TempDir(), t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(source, "vendor", "bin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(source, "vendor", "autoload.php"), []byte("<?php\n"), 0o644))
	var err error
	source, err = filepath.EvalSymlinks(source)
	require.NoError(t, err)
	root, err = filepath.EvalSymlinks(root)
	require.NoError(t, err)
	return source, root
}

func run(t *testing.T, root, command, mode string, overlays []Overlay) (out string, sandboxed bool, err error) {
	t.Helper()
	cmd, sandboxed, cleanup, err := CommandWith(context.Background(), root, root, command, mode, overlays)
	if err != nil {
		return "", false, err
	}
	defer cleanup()
	b, runErr := cmd.CombinedOutput()
	return string(b), sandboxed, runErr
}

// TestOverlay_a_command_sees_the_environment_and_cannot_write_through_it: where the engine keeps the user's checkout out of reach of
// writes (the macOS sandbox), a command sees the declared directory in its root and cannot change it; with no sandbox it sees it too,
// and the command is reported as not sandboxed, which is what the certificate says.
func TestOverlay_a_command_sees_the_environment_and_cannot_write_through_it(t *testing.T) {
	source, root := userCheckout(t)
	overlays := []Overlay{{Source: filepath.Join(source, "vendor"), Target: "vendor"}}

	_, _, err := run(t, root, "test -f vendor/autoload.php", Off, nil)
	assert.Error(t, err, "without the environment the file is not there")

	out, sandboxed, err := run(t, root, "test -f vendor/autoload.php", Off, overlays)
	require.NoError(t, err, out)
	assert.False(t, sandboxed, "with the sandbox off the command says so")

	if runtime.GOOS != "darwin" {
		t.Skip("documented limit: the write protection is proved with the macOS sandbox; the Linux engines refuse a declared environment (TestOverlay_engines_that_cannot_guarantee_it_refuse)")
	}
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skip("no sandbox-exec here")
	}
	_, root2 := userCheckout(t)
	out, sandboxed, err = run(t, root2, "test -f vendor/autoload.php && ! (echo x > vendor/new.txt) 2>/dev/null && ! rm vendor/autoload.php 2>/dev/null", Required, overlays)
	require.NoError(t, err, out)
	assert.True(t, sandboxed)
	assert.NoFileExists(t, filepath.Join(source, "vendor", "new.txt"), "a write through the environment did not reach the user's checkout")
	assert.FileExists(t, filepath.Join(source, "vendor", "autoload.php"))
}

func TestOverlay_a_path_the_checkout_already_has_is_refused(t *testing.T) {
	source, root := userCheckout(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "vendor"), 0o755)) // tracked in the commit
	_, _, err := run(t, root, "true", Off, []Overlay{{Source: filepath.Join(source, "vendor"), Target: "vendor"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is in the checkout already")
}

// TestOverlay_engines_that_cannot_guarantee_it_refuse: bubblewrap and Landlock are not built for a declared environment (they are tested
// later, on a Linux machine): a run on either refuses, with a message that names the engine and the ways out. Stand-in engines prove
// the contract on any machine.
func TestOverlay_engines_that_cannot_guarantee_it_refuse(t *testing.T) {
	saved := engines[runtime.GOOS]
	t.Cleanup(func() { engines[runtime.GOOS] = saved })
	for _, name := range []string{"bwrap", "landlock"} {
		t.Run(name, func(t *testing.T) {
			engines[runtime.GOOS] = []engine{{name, func(root, tmp string, hide []string) ([]string, error) { return []string{"env"}, nil }}}
			source, root := userCheckout(t)
			_, _, err := run(t, root, "true", Required, []Overlay{{Source: filepath.Join(source, "vendor"), Target: "vendor"}})
			require.Error(t, err)
			assert.Contains(t, err.Error(), name)
			assert.Contains(t, err.Error(), "declared environment")
			assert.Contains(t, OverlayRefusal(Required), name, "the run is refused when it starts")
			assert.Contains(t, OverlayRefusal(Auto), name)
			assert.Empty(t, OverlayRefusal(Off))
			assert.Empty(t, OverlayRefusal("docker:image=php:8.2"))
		})
	}
	t.Run("an engine that is tested for it", func(t *testing.T) {
		engines[runtime.GOOS] = []engine{{"sandbox-exec", func(root, tmp string, hide []string) ([]string, error) { return []string{"env"}, nil }}}
		assert.Empty(t, OverlayRefusal(Required))
	})
}

// fakeDocker puts a `docker` program first on the path that logs its arguments and answers the image check, so the read-only mount
// arguments and the refusals are tested on any machine (a real Docker is tested in docker_test.go where the image exists).
func fakeDocker(t *testing.T, imagePresent bool) (logFile string) {
	t.Helper()
	dir := t.TempDir()
	logFile = filepath.Join(dir, "docker.log")
	script := "#!/bin/sh\necho \"$*\" >> " + logFile + "\n" +
		"if [ \"$1 $2\" = \"image inspect\" ]; then " + map[bool]string{true: "echo sha256:fake; exit 0", false: "echo 'no such image' >&2; exit 1"}[imagePresent] + "; fi\nexit 0\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logFile
}

func TestOverlay_docker_mounts_the_environment_read_only(t *testing.T) {
	logFile := fakeDocker(t, true)
	source, root := userCheckout(t)
	cmd, sandboxed, cleanup, err := CommandWith(context.Background(), root, root, "vendor/bin/phpunit", "docker:image=php:8.2", []Overlay{{Source: filepath.Join(source, "vendor"), Target: "vendor"}})
	require.NoError(t, err)
	defer cleanup()
	assert.True(t, sandboxed)
	require.NoError(t, cmd.Run())
	b, err := os.ReadFile(logFile)
	require.NoError(t, err)
	log := string(b)
	assert.Contains(t, log, "-v "+filepath.Join(source, "vendor")+":"+filepath.Join(root, "vendor")+":ro", "the environment is mounted read-only")
	assert.Contains(t, log, "--pull never", "the image is never pulled")
	assert.Contains(t, log, "--network none")
	assert.Contains(t, log, "-v "+root+":"+root+" ", "the checkout itself is still the writable root")
	assert.NotContains(t, log, "docker pull")
}

func TestOverlay_docker_with_the_image_missing_refuses_and_says_how_to_pull_it(t *testing.T) {
	logFile := fakeDocker(t, false)
	source, root := userCheckout(t)
	_, _, _, err := CommandWith(context.Background(), root, root, "true", "docker:image=php:8.2", []Overlay{{Source: filepath.Join(source, "vendor"), Target: "vendor"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "docker pull php:8.2")
	b, _ := os.ReadFile(logFile)
	assert.NotContains(t, strings.ReplaceAll(string(b), "image inspect", ""), "pull php", "it never pulls the image itself")
}
