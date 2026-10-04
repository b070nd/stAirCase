package agent

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/tui"
)

// Review brings a change made elsewhere (for example a cloud agent's pull
// request) into the run's worktree, one file at a time, and has each file
// reviewed after the fact: approved, it is kept; rejected, it is reverted.
// The branch's own diff since its merge base with the run's base is applied,
// so newer work on the current branch is not undone; a file whose diff does
// not apply is left out and reported.
type Review struct {
	Commit string // the exact commit reviewed
	By     string // who made it, as the certificate names them
}

// Run applies and proposes each changed file.
func (r *Review) Run(ctx context.Context, env *orchestrator.AgentEnv) error {
	git := func(stdin []byte, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", env.Worktree, "-c", "core.hooksPath=/dev/null"}, args...)...)
		var stderr bytes.Buffer
		cmd.Stdin, cmd.Stderr = bytes.NewReader(stdin), &stderr
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git %s: %w: %s", args[0], err, bytes.TrimSpace(stderr.Bytes()))
		}
		return out, nil
	}
	mb, err := git(nil, "merge-base", "HEAD", r.Commit)
	if err != nil {
		return fmt.Errorf("%.12s has no common history with this branch: %w", r.Commit, err)
	}
	base := strings.TrimSpace(string(mb))
	names, err := git(nil, "diff", "--name-only", "-z", "--no-renames", base, r.Commit)
	if err != nil {
		return err
	}
	for _, file := range strings.Split(strings.TrimRight(string(names), "\x00"), "\x00") {
		if file == "" {
			continue
		}
		patch, err := git(nil, "diff", "--binary", "--no-renames", base, r.Commit, "--", file)
		if err != nil {
			return err
		}
		if _, err := git(patch, "apply", "--whitespace=nowarn", "-"); err != nil {
			fmt.Printf("   ⚠️  %s: left out, its change does not apply to this branch (%v)\n", tui.Safe(file), err)
			continue
		}
		env.ProposeWorktreeChanges(ctx, r.By, fmt.Sprintf("%s changed %s in commit %.12s.", r.By, file, r.Commit))
	}
	return nil
}
