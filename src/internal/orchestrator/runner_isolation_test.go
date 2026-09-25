package orchestrator_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// extractFixtureRepo unpacks tests/fixtures/repos/<name>/<name>.tar.gz (archived
// as `tar -C repo .`) into a temp dir and returns the repo path.
func extractFixtureRepo(t *testing.T, name string) string {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "..", "tests", "fixtures", "repos", name, name+".tar.gz"))
	require.NoError(t, err)
	defer f.Close()
	gz, err := gzip.NewReader(f)
	require.NoError(t, err)
	dst := t.TempDir()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		p := filepath.Join(dst, h.Name) // #nosec G305 -- trusted in-repo test fixture
		switch h.Typeflag {
		case tar.TypeDir:
			require.NoError(t, os.MkdirAll(p, 0o755))
		case tar.TypeReg:
			require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
			out, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode)&0o777)
			require.NoError(t, err)
			_, err = io.Copy(out, tr) // #nosec G110 -- small trusted fixture
			require.NoError(t, err)
			require.NoError(t, out.Close())
		}
	}
	return dst
}

// checkoutSnapshot captures everything a run must never touch in the
// developer's checkout: HEAD, current branch, index content, status (staged,
// unstaged, untracked), stash list, and the bytes of every working-tree file.
func checkoutSnapshot(t *testing.T, repo string) string {
	t.Helper()
	git := func(args ...string) string {
		out, _ := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		return strings.TrimSpace(string(out))
	}
	// The index is compared by content (entries, modes, blobs, stages, flags),
	// not raw bytes: plain `git status` legitimately refreshes cached stat data.
	lines := []string{
		"HEAD " + git("rev-parse", "HEAD"),
		"BRANCH " + git("symbolic-ref", "-q", "HEAD"),
		"INDEX\n" + git("ls-files", "--stage") + "\n" + git("ls-files", "-v"),
		"STATUS\n" + git("status", "--porcelain=v2", "--untracked-files=all"),
		"STASH " + git("stash", "list"),
	}
	var files []string
	require.NoError(t, filepath.WalkDir(repo, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(repo, p)
			files = append(files, fmt.Sprintf("%s %x", rel, sha256.Sum256(b)))
		}
		return nil
	}))
	sort.Strings(files)
	return strings.Join(append(lines, files...), "\n")
}

// isolationAgent proposes target.txt and writes it into the worktree it is
// given — or, were none given, into the developer checkout (repo), which the
// checkout snapshot would catch. "hang" waits until the run is cancelled and
// "fail" fails after writing.
func isolationAgent(mode, content, repo string) orchestrator.AgentFunc {
	return func(ctx context.Context, env *orchestrator.AgentEnv) error {
		if mode == "hang" {
			<-ctx.Done()
			return ctx.Err()
		}
		ap := env.Propose(ctx, domain.YieldRequest{AgentName: "coder", ActionType: "file_edit",
			ProposedEdits:  []domain.ProposedEdit{{File: "target.txt", SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: content}},
			ReasoningTrace: "isolation", ConfidenceScore: 0.9})
		if !ap.Approved {
			return fmt.Errorf("not approved: %s", ap.Feedback)
		}
		root := env.Worktree
		if root == "" {
			root = repo
		}
		if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte(content), 0o644); err != nil {
			return err
		}
		if mode == "fail" {
			return errors.New("agent failed")
		}
		return nil
	}
}

func TestRun_isolation_developer_checkout_is_never_touched(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test — skipped in -short mode")
	}
	for _, mode := range []string{"success", "fail", "hang", "concurrent"} {
		t.Run(mode, func(t *testing.T) {
			wsDir := t.TempDir()
			prepareAgentWorkspace(t, wsDir)
			s := newTestStore(t)
			repo := extractFixtureRepo(t, "dirty-worktree") // HEAD on main + a staged, uncommitted DIRTY.md
			caseA, projectID := scaffoldForRun(t, s, repo)
			cases := []int64{caseA}
			if mode == "concurrent" {
				c, err := s.CreateCase(projectID)
				require.NoError(t, err)
				cases = append(cases, c.ID)
			}
			agentMode := mode
			if mode == "concurrent" {
				agentMode = "success"
			}
			before := checkoutSnapshot(t, repo)

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if mode == "hang" {
				time.AfterFunc(3*time.Second, cancel)
			}
			errs := make([]error, len(cases))
			var wg sync.WaitGroup
			for i, c := range cases {
				wg.Add(1)
				go func(i int, c int64) {
					defer wg.Done()
					errs[i] = orchestrator.NewRunner(s, wsDir).Run(ctx, c, orchestrator.RunOptions{SkipGates: true,
						Agent: isolationAgent(agentMode, fmt.Sprintf("content from case %d\n", i), repo)})
				}(i, c)
			}
			wg.Wait()

			assert.Equal(t, before, checkoutSnapshot(t, repo), "the developer checkout must be byte-identical after the run")

			want := map[string]string{"success": persistence.RunStatusSuccess, "fail": persistence.RunStatusFailed,
				"hang": persistence.RunStatusKilled, "concurrent": persistence.RunStatusSuccess}[mode]
			for i, c := range cases {
				runs, err := s.ListRunsByCase(c)
				require.NoError(t, err)
				require.Len(t, runs, 1)
				assert.Equal(t, want, runs[0].Status, "case %d: %v", c, errs[i])
				if want == persistence.RunStatusSuccess {
					out, err := exec.Command("git", "-C", repo, "show", fmt.Sprintf("staircase/run-%d:target.txt", runs[0].ID)).CombinedOutput()
					require.NoError(t, err, "run branch must hold the approved file: %s", out)
					assert.Equal(t, fmt.Sprintf("content from case %d\n", i), string(out))
				}
			}
		})
	}
}
