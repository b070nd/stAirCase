package orchestrator_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/b070nd/staircase-core/src/internal/domain"
	"github.com/b070nd/staircase-core/src/internal/orchestrator"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pathEscapeAgent is a hostile agent: it proposes creating relFile, a path
// outside the project root, and fails the run if that is ever approved. The
// orchestrator is the trust boundary — no agent can be relied on to police paths.
func pathEscapeAgent(relFile string) orchestrator.AgentFunc {
	return func(ctx context.Context, env *orchestrator.AgentEnv) error {
		ap := env.Propose(ctx, domain.YieldRequest{AgentName: "coder", ActionType: "file_edit",
			ProposedEdits:  []domain.ProposedEdit{{File: relFile, SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: "PWNED"}},
			ReasoningTrace: "path escape", ConfidenceScore: 0.9})
		if ap.Approved {
			return errors.New("an escaping proposal was approved")
		}
		return nil
	}
}

// TestRun_adversarial_path_escape_blocked catches the orchestrator approving a
// path outside the project root: the proposal must be refused before policy or
// a human sees it (here policy would auto-approve any file_edit), audited as
// approval_path_escape, and nothing may be committed.
func TestRun_adversarial_path_escape_blocked(t *testing.T) {
	s, wsDir, _, caseID := setupApprovalRun(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = orchestrator.NewRunner(s, wsDir).Run(ctx, caseID,
		orchestrator.RunOptions{SkipGates: true, Agent: pathEscapeAgent("../escape.txt")})

	runs, err := s.ListRunsByCase(caseID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, persistence.RunStatusSuccess, runs[0].Status,
		"the agent fails the run if the escaping proposal was approved")
	assert.Empty(t, runs[0].GitCommitHash, "nothing may be committed")

	logs, err := s.ListEventLogs(runs[0].ID)
	require.NoError(t, err)
	found := false
	for _, l := range logs {
		if l.EventType == "approval_path_escape" {
			found = true
			assert.Contains(t, l.Payload, "escape.txt")
		}
	}
	assert.True(t, found, "approval_path_escape audit event expected")
}

// TestCleanApprovedPath pins the trust-boundary path rules on a real tree
// (symlink and directory checks need actual filesystem entries).
func TestCleanApprovedPath(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub", "dir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "f.txt"), []byte("x"), 0o644))
	require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(root, "link")))
	require.NoError(t, os.Symlink(filepath.Join(root, "sub", "f.txt"), filepath.Join(root, "alias.txt")))
	ok := map[string]string{
		"file.txt":             "file.txt",
		"sub/f.txt":            "sub/f.txt",
		"./a.txt":              "a.txt",
		"sub/../ok.txt":        "ok.txt",
		"sub/new/deeper/n.txt": "sub/new/deeper/n.txt",
		"sub/.gitignore":       "sub/.gitignore",
		"docs/.github/ci.yml":  "docs/.github/ci.yml",
	}
	for in, want := range ok {
		got, err := orchestrator.CleanApprovedPathForTest(root, in)
		assert.NoError(t, err, "%q", in)
		assert.Equal(t, want, got, "%q", in)
	}
	for _, bad := range []string{
		"", ".", "..", "../escape.txt", "../../etc/passwd", "sub/../../escape.txt", "/etc/passwd",
		".git", ".git/config", "sub/.git/x", ".GIT/hooks/pre-commit", // repository internals
		"sub", "sub/dir", // directories
		"link/x.txt", "alias.txt", // symlinks
	} {
		_, err := orchestrator.CleanApprovedPathForTest(root, bad)
		assert.Error(t, err, "%q must be refused", bad)
	}
}
