package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
)

// HookFileEnv names the file that tells `staircase hook` where the governed
// run is: {"url", "token"}, readable only by the user, removed when the run
// ends. staircase starts the agent with it set; the agent's hooks inherit it.
const HookFileEnv = "STAIRCASE_HOOK_FILE"

// hookAgents are the agents whose hook calls a run can decide.
var hookAgents = []string{"claude-code", "codex"}

// hookClient waits as long as a person needs to decide; the agent's own hook
// timeout bounds it. No proxy: the token only ever goes to the local run.
var hookClient = &http.Client{Transport: &http.Transport{}}

// RunHook is `staircase hook <agent> [--governed]`: it passes one hook call
// on stdin to the governed run and prints the run's answer. The result is
// the exit code. Every failure is 2, which blocks the tool call: agents let
// the call run when a hook fails any other way. Without --governed and
// without a session (a hook installed once for a user or a company), the
// call passes through.
func RunHook(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	block := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "staircase hook: "+format+"\n", a...)
		return 2
	}
	if slices.Contains(args, "-h") || slices.Contains(args, "--help") {
		fmt.Fprintln(stdout, "usage: staircase hook <agent> [--governed]   agents: "+strings.Join(hookAgents, ", "))
		return 0
	}
	var name string
	governed := false
	for _, a := range args {
		switch {
		case a == "--governed":
			governed = true
		case strings.HasPrefix(a, "-") || name != "":
			return block("unexpected argument %q", a)
		default:
			name = a
		}
	}
	if !slices.Contains(hookAgents, name) {
		return block("unknown agent %q (supported: %s)", name, strings.Join(hookAgents, ", "))
	}

	path := os.Getenv(HookFileEnv)
	if path == "" {
		if governed {
			return block("%s is not set: no governed run to ask", HookFileEnv)
		}
		return 0
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return block("read the run's hook file: %v", err)
	}
	var run struct{ URL, Token string }
	if err := json.Unmarshal(b, &run); err != nil || run.URL == "" {
		return block("the run's hook file %s is not valid", path)
	}

	req, err := http.NewRequest(http.MethodPost, run.URL, stdin)
	if err != nil {
		return block("%v", err)
	}
	req.Header.Set("Authorization", "Bearer "+run.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := hookClient.Do(req)
	if err != nil {
		return block("ask the run: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return block("the run answered %s", resp.Status)
	}
	if _, err := io.Copy(stdout, resp.Body); err != nil {
		return block("read the run's answer: %v", err)
	}
	return 0
}
