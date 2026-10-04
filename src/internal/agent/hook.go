package agent

import (
	"bytes"
	"encoding/json"
	"errors"
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
var hookAgents = []string{"claude-code", "codex", "gemini"}

// hookEvents are the events each agent's hook is registered for. A call for any
// other event is not something the run decides, and is blocked.
var hookEvents = map[string][]string{
	"claude-code": {"SessionStart", "PreToolUse", "PostToolUse", "Stop"},
	"codex":       {"SessionStart", "PreToolUse", "PostToolUse", "Stop"},
	"gemini":      {"SessionStart", "BeforeTool", "AfterTool"},
}

// maxHookBody bounds what a hook call or the run's answer may be.
const maxHookBody = 32 << 20

// checkReply decides whether the run's answer is one the agent can act on. A call
// that needs a decision (before a tool runs) needs a complete one: an empty,
// truncated or unrecognised answer is read by agents as "no objection", so it is
// refused here, where it can be turned into a block. The other events accept an
// empty answer (an acknowledgement) or a JSON object.
func checkReply(agentName, event string, reply []byte) error {
	reply = bytes.TrimSpace(reply)
	needsDecision := event == "PreToolUse" || event == "BeforeTool"
	if len(reply) == 0 {
		if needsDecision {
			return errors.New("empty answer to a call that needs a decision")
		}
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(reply, &obj); err != nil || obj == nil {
		return errors.New("the answer is not a complete JSON object")
	}
	if !needsDecision {
		return nil
	}
	var decision any
	if agentName == "gemini" {
		decision = obj["decision"]
	} else if hso, ok := obj["hookSpecificOutput"].(map[string]any); ok {
		decision = hso["permissionDecision"]
	}
	if d, _ := decision.(string); d != "allow" && d != "deny" {
		return fmt.Errorf("the answer holds no allow or deny decision (%v)", decision)
	}
	return nil
}

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
		fmt.Fprintln(stdout, "usage: staircase hook <agent> [--governed] [--file <session file>]   agents: "+strings.Join(hookAgents, ", "))
		return 0
	}
	var name, file string
	governed, require := false, false
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--governed":
			governed = true
		case a == "--require":
			require = true
		case a == "--file" && i+1 < len(args): // the session file, for an agent that does not pass our environment on
			i++
			file = args[i]
		case strings.HasPrefix(a, "-") || name != "":
			return block("unexpected argument %q", a)
		default:
			name = a
		}
	}
	if !slices.Contains(hookAgents, name) {
		return block("unknown agent %q (supported: %s)", name, strings.Join(hookAgents, ", "))
	}

	path := file
	if path == "" {
		path = os.Getenv(HookFileEnv)
	}
	if require && path == "" { // a company's managed hook: no ungoverned sessions
		return block("this organisation runs %s only through stAirCase: start a session with staircase %s \"<task>\"",
			name, map[string]string{"claude-code": "claude", "codex": "codex", "gemini": "gemini"}[name])
	}
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

	call, err := io.ReadAll(io.LimitReader(stdin, maxHookBody+1))
	if err != nil || len(call) > maxHookBody {
		return block("read the hook call: %v", err)
	}
	var head struct {
		Event string `json:"hook_event_name"`
	}
	if json.Unmarshal(call, &head) != nil || head.Event == "" {
		return block("the hook call names no event")
	}
	if !slices.Contains(hookEvents[name], head.Event) {
		return block("%s has no hook for the event %q", name, head.Event)
	}
	req, err := http.NewRequest(http.MethodPost, run.URL, bytes.NewReader(call))
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
	reply, err := io.ReadAll(io.LimitReader(resp.Body, maxHookBody+1))
	if err != nil || len(reply) > maxHookBody {
		return block("read the run's answer: %v", err)
	}
	if err := checkReply(name, head.Event, reply); err != nil {
		return block("%s: %v", head.Event, err)
	}
	if _, err := stdout.Write(reply); err != nil {
		return block("write the answer: %v", err)
	}
	return 0
}
