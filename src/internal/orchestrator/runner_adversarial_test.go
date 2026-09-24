package orchestrator_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
// path guard entirely: it proposes a file_edit whose path escapes the project
// root (`relFile`), gets it auto-approved, then writes `content` to the
// absolute escaping location `writeAbs`. The orchestrator is the trust
// boundary here — a compromised runtime cannot be relied on to police paths.
func pathEscapeScript(relFile, contentHash, writeAbs, content string) string {
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
    "proposed_edits":[{"file":` + fmt.Sprintf("%q", relFile) + `,"search_block":"(new file)",
                       "replace_block":"preview","content_hash":` + fmt.Sprintf("%q", contentHash) + `}],
    "reasoning_trace":"path escape","confidence_score":0.9
})+"\n").encode())
buf = b""
while b"\n" not in buf:
    buf += s.recv(4096)
resp = json.loads(buf.split(b"\n")[0])
assert resp.get("approved") == True, resp
open(` + fmt.Sprintf("%q", writeAbs) + `, "w").write(` + fmt.Sprintf("%q", content) + `)
s.close()
sys.exit(0)
`
}

// TestRun_adversarial_path_escape_blocked is the headline B1 scenario: a
// compromised agent gets an out-of-repo file_edit approved and writes the file
// to disk outside the project root. The orchestrator must refuse to commit,
// fail the run, and record an approval_path_escape audit event — proving the
// Go trust boundary independently enforces the repo sandbox even when the
// Python-side guard is bypassed.
func TestRun_adversarial_path_escape_blocked(t *testing.T) {
	s, wsDir, repoPath, caseID := setupContentHashRun(t)

	// `../escape.txt` resolves to a sibling of the repo — outside the sandbox.
	const evil = "PWNED — written outside the repo\n"
	escapeAbs := filepath.Join(filepath.Dir(repoPath), "escape.txt")
	sum := sha256.Sum256([]byte(evil))
	sumHex := hex.EncodeToString(sum[:])
	script := pathEscapeScript("../escape.txt", sumHex, escapeAbs, evil)
	require.NoError(t, os.WriteFile(
		filepath.Join(wsDir, "tmp", fmt.Sprintf("graph_exec_case%d.py", caseID)), []byte(script), 0o600))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = orchestrator.NewRunner(s, wsDir).Run(ctx, caseID,
		orchestrator.RunOptions{SkipGates: true})

	runs, err := s.ListRunsByCase(caseID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, persistence.RunStatusFailed, runs[0].Status,
		"a path-escaping approved edit must fail the run")
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
