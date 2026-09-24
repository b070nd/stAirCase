package orchestrator_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

	"github.com/b070nd/staircase-core/src/internal/orchestrator"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	scaffoldtpl "github.com/b070nd/staircase-core/src/internal/template"
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

// isolationAgent uses the real embedded IPC client, dumps its environment
// keys to envOut, and (unless hanging) proposes and writes target.txt into the
// project path it is given — the worktree, not the developer checkout.
func isolationAgent(mode, content, envOut, fallbackProject string) string {
	return fmt.Sprintf(`import hashlib, json, os, sys, time
boot = json.loads(sys.stdin.readline())
json.dump(sorted(os.environ), open(%q, "w"))
sys.path.insert(0, boot["runner_path"])
from staircase_runner.ipc import IPCClient
project = boot.get("project_path") or %q
ipc = IPCClient(boot["socket_path"], boot["token"])
if %q == "hang":
    time.sleep(60)
content = %q
r = ipc.yield_request("coder", "file_edit", [{"file": "target.txt", "search_block": "(new file)",
    "replace_block": content, "content_hash": hashlib.sha256(content.encode()).hexdigest()}], "isolation")
assert r.get("approved"), r
with open(os.path.join(project, "target.txt"), "w") as f:
    f.write(content)
ipc.close()
sys.exit(1 if %q == "fail" else 0)
`, envOut, fallbackProject, mode, content, mode)
}

func TestRun_isolation_developer_checkout_is_never_touched(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test — skipped in -short mode")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	t.Setenv("STAIRCASE_TEST_SENTINEL", "must-not-reach-the-agent")

	for _, mode := range []string{"success", "fail", "hang", "concurrent"} {
		t.Run(mode, func(t *testing.T) {
			wsDir, err := os.MkdirTemp("", "strc-iso-")
			require.NoError(t, err)
			t.Cleanup(func() { os.RemoveAll(wsDir) })
			prepareRunWorkspace(t, wsDir)
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
			envOut := filepath.Join(t.TempDir(), "env.json")
			for i, c := range cases {
				script := isolationAgent(agentMode, fmt.Sprintf("content from case %d\n", i), envOut, repo)
				require.NoError(t, os.WriteFile(filepath.Join(wsDir, "tmp", fmt.Sprintf("graph_exec_case%d.py", c)), []byte(script), 0o600))
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
					errs[i] = orchestrator.NewRunner(s, wsDir).Run(ctx, c, orchestrator.RunOptions{SkipGates: true})
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

			var env []string
			b, err := os.ReadFile(envOut)
			require.NoError(t, err, "agent must have started")
			require.NoError(t, json.Unmarshal(b, &env))
			assert.NotContains(t, env, "STAIRCASE_TEST_SENTINEL", "the agent must not inherit the operator's environment")
			assert.Contains(t, env, "PATH")
		})
	}
}

// TestRun_refuses_scripts_from_another_template: a compiled script embeds its
// runtime, so one produced by another stAirCase version (older IPC client,
// writes outside the worktree) must be refused, not run.
func TestRun_refuses_scripts_from_another_template(t *testing.T) {
	s, wsDir, _, caseID := setupContentHashRun(t)
	const content = "fingerprinted\n"
	script := []byte(contentHashScript(content, content))
	path := filepath.Join(wsDir, "tmp", fmt.Sprintf("graph_exec_case%d.py", caseID))
	require.NoError(t, os.WriteFile(path, script, 0o600))
	scriptSum := sha256.Sum256(script)
	require.NoError(t, os.WriteFile(path+".sha256", []byte(hex.EncodeToString(scriptSum[:])), 0o600))

	require.NoError(t, os.WriteFile(path+".tmpl", []byte("fingerprint-of-an-older-release"), 0o600))
	err := orchestrator.NewRunner(s, wsDir).Run(context.Background(), caseID, orchestrator.RunOptions{SkipGates: true})
	require.ErrorContains(t, err, "compiled by a different stAirCase version")

	require.NoError(t, os.WriteFile(path+".tmpl", []byte(scaffoldtpl.Fingerprint()), 0o600))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	require.NoError(t, orchestrator.NewRunner(s, wsDir).Run(ctx, caseID, orchestrator.RunOptions{SkipGates: true}))
}
