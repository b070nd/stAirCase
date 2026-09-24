package orchestrator_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/b070nd/staircase-core/src/internal/orchestrator"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pathEscapeScript builds a hostile agent that bypasses the Python template's
// path guard and client entirely, speaking raw IPC: it proposes creating
// relFile, a path outside the project root, and exits 0 only if the
// orchestrator refuses it. A compromised runtime cannot be relied on to police
// paths; the orchestrator is the trust boundary.
func pathEscapeScript(relFile string) string {
	return `import sys, json, socket, time
boot = json.loads(sys.stdin.readline())
s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
for _ in range(30):
    try:
        s.connect(boot["socket_path"]); break
    except OSError:
        time.sleep(0.05)
s.sendall((json.dumps({"type":"auth","token":boot["token"]})+"\n").encode())
buf = b""
while b"\n" not in buf:
    buf += s.recv(4096)
assert json.loads(buf.split(b"\n")[0]).get("type") == "auth_ok"
s.sendall((json.dumps({
    "type":"yield_request","agent_name":"coder","action_type":"file_edit",
    "proposed_edits":[{"file":` + fmt.Sprintf("%q", relFile) + `,"search_block":"(new file)","replace_block":"PWNED"}],
    "reasoning_trace":"path escape","confidence_score":0.9
})+"\n").encode())
buf = b""
while b"\n" not in buf:
    buf += s.recv(4096)
resp = json.loads(buf.split(b"\n")[0])
assert resp.get("approved") is False, resp
s.close()
sys.exit(0)
`
}

// TestRun_adversarial_path_escape_blocked catches the orchestrator approving a
// path outside the project root: the proposal must be refused before policy or
// a human sees it (here policy would auto-approve any file_edit), audited as
// approval_path_escape, and nothing may be committed.
func TestRun_adversarial_path_escape_blocked(t *testing.T) {
	s, wsDir, _, caseID := setupApprovalRun(t)
	require.NoError(t, os.WriteFile(
		filepath.Join(wsDir, "tmp", fmt.Sprintf("graph_exec_case%d.py", caseID)), []byte(pathEscapeScript("../escape.txt")), 0o600))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = orchestrator.NewRunner(s, wsDir).Run(ctx, caseID,
		orchestrator.RunOptions{SkipGates: true})

	runs, err := s.ListRunsByCase(caseID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, persistence.RunStatusSuccess, runs[0].Status,
		"the agent exits 0 only if the escaping proposal was refused")
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
