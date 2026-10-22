package sandbox

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestParseMode: the modes --sandbox takes, and docker's options, are checked before anything runs.
func TestParseMode(t *testing.T) {
	for _, ok := range []string{Auto, Required, Off, "docker:image=golang:1.26-bookworm", "docker:image=alpine:3.20,memory=512m,cpus=1.5"} {
		if err := ParseMode(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for bad, why := range map[string]string{
		"docker":                     "image",
		"docker:memory=1g":           "image",
		"docker:image=a,net=on":      "net",
		"docker:image=a,memory=lots": "memory",
		"docker:image=a,cpus=-1":     "cpus",
		"docker:image=a b":           "image",
		"docker:image=--privileged":  "image",
		"sometimes":                  "sometimes",
	} {
		err := ParseMode(bad)
		if err == nil || !strings.Contains(err.Error(), why) {
			t.Errorf("%q: want an error naming %q, got %v", bad, why, err)
		}
	}
}

const testImage = "golang:1.26-bookworm" // present on the machine; a test never pulls one

func needDocker(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test - skipped in -short mode")
	}
	if err := exec.Command("docker", "image", "inspect", testImage).Run(); err != nil {
		if os.Getenv("STAIRCASE_REQUIRE_SANDBOX") != "" && strings.Contains(" "+os.Getenv("STAIRCASE_REQUIRE_SANDBOX")+" ", " docker ") {
			t.Fatalf("docker is required here, and the image %s is not available: %v", testImage, err)
		}
		t.Skipf("docker or the image %s is not available: %v", testImage, err)
	}
}

// TestDocker_a_command_runs_in_a_container_that_can_write_only_the_worktree: the same promise as the other engines, kept by a
// container: it writes in the worktree (as the user, so the files are the user's), nowhere else; it has no network; it cannot see
// the home folder at all; and the memory limit holds.
func TestDocker_a_command_runs_in_a_container_that_can_write_only_the_worktree(t *testing.T) {
	t.Run("docker", testDockerContained) // an engine's subtest: optional where the machine has no docker image (packaging/evidence.sh)
}

func testDockerContained(t *testing.T) {
	needDocker(t)
	root, _ := filepath.EvalSymlinks(t.TempDir())
	home, _ := os.UserHomeDir()
	script := `echo ok > out.txt
touch /etc/staircase-probe 2>/dev/null && echo WROTE_ETC
test -e ` + home + ` && echo SAW_HOME
getent hosts example.com >/dev/null 2>&1 && echo NETWORK
id -u
cat /sys/fs/cgroup/memory.max`
	cmd, sandboxed, cleanup, err := Command(context.Background(), root, root, script, "docker:image="+testImage+",memory=256m,cpus=1")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if !sandboxed {
		t.Error("a docker command is sandboxed")
	}
	got := out.String()
	for _, leak := range []string{"WROTE_ETC", "SAW_HOME", "NETWORK"} {
		if strings.Contains(got, leak) {
			t.Errorf("%s: %s", leak, got)
		}
	}
	if !strings.Contains(got, strconv.Itoa(os.Getuid())+"\n") {
		t.Errorf("it runs as the user (uid %d): %s", os.Getuid(), got)
	}
	if !strings.Contains(got, "268435456") {
		t.Errorf("the memory limit (256m) holds: %s", got)
	}
	b, err := os.ReadFile(filepath.Join(root, "out.txt"))
	if err != nil || string(b) != "ok\n" {
		t.Errorf("it writes in the worktree: %q %v", b, err)
	}
}

// TestDocker_a_cancelled_command_leaves_no_container: killing the docker client does not stop its container, so cleanup does.
func TestDocker_a_cancelled_command_leaves_no_container(t *testing.T) {
	t.Run("docker", testDockerCancelled)
}

func testDockerCancelled(t *testing.T) {
	needDocker(t)
	root, _ := filepath.EvalSymlinks(t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd, _, cleanup, err := Command(ctx, root, root, "sleep 60", "docker:image="+testImage)
	if err != nil {
		t.Fatal(err)
	}
	KillGroup(cmd) // as the callers do
	_ = cmd.Run()
	cleanup()
	out, _ := exec.Command("docker", "ps", "-aq", "--filter", "label=staircase.root="+root).Output()
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("a container is left: %s", out)
	}
}

// TestDocker_the_environment_is_a_read_only_mount: with a real Docker (skipped where the image is not on the machine; run it on a proper
// environment), a command sees the user's directory where the checkout has none, and cannot change it.
func TestDocker_the_environment_is_a_read_only_mount(t *testing.T) {
	needDocker(t)
	source, root := userCheckout(t)
	overlays := []Overlay{{Source: filepath.Join(source, "vendor"), Target: "vendor"}}
	out, sandboxed, err := run(t, root, "test -f vendor/autoload.php && ! (echo x > vendor/new.txt) 2>/dev/null && ! rm vendor/autoload.php 2>/dev/null", "docker:image="+testImage, overlays)
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if !sandboxed {
		t.Error("a container is a sandbox")
	}
	if _, err := os.Stat(filepath.Join(source, "vendor", "new.txt")); err == nil {
		t.Error("a write through the mount reached the user's directory")
	}
	if _, err := os.Stat(filepath.Join(source, "vendor", "autoload.php")); err != nil {
		t.Errorf("the user's file is gone: %v", err)
	}
}
