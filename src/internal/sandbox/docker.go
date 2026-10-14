package sandbox

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// The docker engine (the sandbox specification's container mode): a command runs in a throwaway container that sees only the
// worktree and its own temp folder, as the user, without network, capabilities or a writable system, and within the memory and CPU
// limits given. It is chosen explicitly, never by "auto": the image is the user's choice, and it is never pulled implicitly.
//
// ponytail: its options travel as one mode string (docker:image=...,memory=...,cpus=...), the same string --sandbox, the run's saved
// terms and the agent already carry, so a continued run gets the same container; a struct when a second engine needs options.

// DockerPrefix starts a docker mode.
const DockerPrefix = "docker"

type dockerSpec struct{ image, memory, cpus string }

var (
	imageRef  = regexp.MustCompile(`^[a-z0-9][a-zA-Z0-9._/:@-]*$`)
	memoryRef = regexp.MustCompile(`^[1-9][0-9]*[kmg]$`)
)

// ParseMode checks a sandbox mode before anything runs: auto, required, off, or docker with an image and optional limits.
func ParseMode(mode string) error {
	switch mode {
	case Auto, Required, Off:
		return nil
	}
	if mode == DockerPrefix || strings.HasPrefix(mode, DockerPrefix+":") {
		_, err := parseDocker(mode)
		return err
	}
	return fmt.Errorf("unknown sandbox mode %q: auto, required, off or docker:image=<image>[,memory=<n>m|g][,cpus=<n>]", mode)
}

func parseDocker(mode string) (dockerSpec, error) {
	var s dockerSpec
	opts, _ := strings.CutPrefix(mode, DockerPrefix)
	opts = strings.TrimPrefix(opts, ":")
	for _, kv := range strings.Split(opts, ",") {
		if kv == "" {
			continue
		}
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "image":
			if !imageRef.MatchString(v) {
				return s, fmt.Errorf("docker sandbox: %q is not an image name", v)
			}
			s.image = v
		case "memory":
			if !memoryRef.MatchString(v) {
				return s, fmt.Errorf("docker sandbox: memory must look like 512m or 2g, not %q", v)
			}
			s.memory = v
		case "cpus":
			if f, err := strconv.ParseFloat(v, 64); err != nil || f <= 0 {
				return s, fmt.Errorf("docker sandbox: cpus must be a positive number, not %q", v)
			}
			s.cpus = v
		default:
			return s, fmt.Errorf("docker sandbox: unknown option %q (image, memory, cpus)", k)
		}
	}
	if s.image == "" {
		return s, errors.New("docker sandbox: name the image: docker:image=<image>")
	}
	return s, nil
}

// dockerCommand is Command in a container. cleanup removes the container too, since killing the docker client leaves it running.
func dockerCommand(ctx context.Context, root, cwd, tmp, command, mode string) (*exec.Cmd, func(), error) {
	s, err := parseDocker(mode)
	if err != nil {
		return nil, nil, err
	}
	bin, err := exec.LookPath("docker")
	if err != nil {
		return nil, nil, errors.New("docker sandbox: the docker program is not installed")
	}
	if out, err := exec.CommandContext(ctx, bin, "image", "inspect", "--format", "{{.Id}}", s.image).CombinedOutput(); err != nil {
		return nil, nil, fmt.Errorf("docker sandbox: the image %s is not on this machine (docker pull %s): %s", s.image, s.image, strings.TrimSpace(string(out)))
	}
	name := "staircase-" + strings.ToLower(rand.Text()[:12])
	args := []string{"run", "--rm", "--init", "--pull", "never", "--name", name, "--label", "staircase.root=" + root,
		"--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--pids-limit", "512",
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"-v", root + ":" + root, "-v", tmp + ":" + tmp, "-w", cwd, "-e", "TMPDIR=" + tmp, "-e", "HOME=" + tmp}
	if s.memory != "" {
		args = append(args, "--memory", s.memory, "--memory-swap", s.memory) // no swap beyond the limit
	}
	if s.cpus != "" {
		args = append(args, "--cpus", s.cpus)
	}
	args = append(args, s.image, "/bin/sh", "-c", command)
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = cwd
	cmd.Env = Env() // for the docker client only: the container gets nothing of it
	cleanup := func() { _ = exec.Command(bin, "rm", "-f", name).Run() }
	return cmd, cleanup, nil
}
