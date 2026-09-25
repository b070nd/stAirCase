// Package runtest runs an in-process agent against a throwaway workspace and
// repository, for tests of the orchestrator and of agents.
package runtest

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/b070nd/staircase-core/src/internal/crypto"
	"github.com/b070nd/staircase-core/src/internal/domain"
	"github.com/b070nd/staircase-core/src/internal/orchestrator"
	"github.com/b070nd/staircase-core/src/internal/persistence"
)

// File is a file committed to the repository before the run.
type File struct {
	Content string
	Mode    os.FileMode
}

// Options describes one run. Setup may add secrets, limits or a webhook to
// the case's project before the run starts.
type Options struct {
	Base  map[string]File
	Setup func(s *persistence.Store, wsDir string, projectID int64)
	Agent orchestrator.Agent
	Run   orchestrator.RunOptions // Agent and SkipGates are set by Run
}

// Result is what a run left behind.
type Result struct {
	Store  *persistence.Store
	Run    persistence.Run
	Events []domain.RunEventLog
	Repo   string
	WsDir  string // the workspace, removed when the test ends
	Err    error  // Runner.Run's error
}

// Types returns the run's audit event types, in order.
func (r Result) Types() []string {
	out := make([]string, len(r.Events))
	for i, e := range r.Events {
		out[i] = e.EventType
	}
	return out
}

// OnBranch returns file's content on the run branch.
func (r Result) OnBranch(file string) (string, error) {
	out, err := exec.Command("git", "-C", r.Repo, "show", fmt.Sprintf("staircase/run-%d:%s", r.Run.ID, file)).CombinedOutput()
	return string(out), err
}

// Run runs o.Agent in-process on a fresh workspace (key, a policy that
// auto-approves file_edit, tmp/) and repository, then checks the audit chain.
func Run(t testing.TB, o Options) Result {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test — skipped in -short mode")
	}
	wsDir, err := os.MkdirTemp("", "strc-rt-") // short: some paths derive from it
	must(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(wsDir) })
	must(t, crypto.GenerateKey(wsDir))
	must(t, os.WriteFile(filepath.Join(wsDir, "policy.json"),
		[]byte(`{"rules":[{"action_types":["file_edit"],"effect":"approve"}]}`), 0o600))
	must(t, os.MkdirAll(filepath.Join(wsDir, "tmp"), 0o700))

	repo := t.TempDir()
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "config", "user.email", "test@test.com")
	git(t, repo, "config", "user.name", "Test")
	for name, f := range o.Base {
		p := filepath.Join(repo, name)
		must(t, os.MkdirAll(filepath.Dir(p), 0o755))
		must(t, os.WriteFile(p, []byte(f.Content), f.Mode))
	}
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "base")

	db, err := persistence.InitDB(t.TempDir())
	must(t, err)
	t.Cleanup(func() { _ = db.Close() })
	s := persistence.NewStore(db)
	v, err := s.CreateVendor("V")
	must(t, err)
	p, err := s.CreateProject(v.ID, "P", repo)
	must(t, err)
	_, err = s.CreateSwarmTopology(p.ID, "sup", "memory", "langgraph")
	must(t, err)
	c, err := s.CreateCase(p.ID)
	must(t, err)
	if o.Setup != nil {
		o.Setup(s, wsDir, p.ID)
	}

	opts := o.Run
	opts.Agent, opts.SkipGates = o.Agent, true
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	runErr := orchestrator.NewRunner(s, wsDir).Run(ctx, c.ID, opts)

	runs, err := s.ListRunsByCase(c.ID)
	must(t, err)
	if len(runs) != 1 {
		t.Fatalf("want 1 run, got %d (run error: %v)", len(runs), runErr)
	}
	events, err := s.ListEventLogs(runs[0].ID)
	must(t, err)
	must(t, s.VerifyChain(runs[0].ID))
	return Result{Store: s, Run: runs[0], Events: events, Repo: repo, WsDir: wsDir, Err: runErr}
}

func git(t testing.TB, dir string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
