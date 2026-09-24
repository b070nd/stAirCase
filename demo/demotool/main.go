// Command demotool is the offline demo's stand-in model and its small helpers,
// so the demo needs neither an API key nor Python.
//
//	demotool serve <url-file> [--tamper]  OpenAI-compatible gateway playing the demo's agents
//	demotool freeport                     print a free local TCP port
//	demotool first-yield                  print the first pending yield id (approval API JSON on stdin)
//	demotool show-yield                   print a pending yield (approval API JSON on stdin)
//	demotool show-mismatch <checkpoint>   print why finalize refused to commit (audit export)
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
)

// The demo topology (run-demo.sh) registers the supervisor with this role.
const supervisorRole = "You coordinate the work."

const (
	target   = "GREETING.md"
	greeting = "# Hello from stAirCase\n\nThis file was written by an AI agent, but only after a human approved\nthese exact bytes, and the approval is on a signed audit chain.\n"
)

func main() {
	if len(os.Args) < 2 {
		fatal("usage: demotool serve|freeport|first-yield|show-yield|show-mismatch")
	}
	var err error
	switch os.Args[1] {
	case "serve":
		if len(os.Args) < 3 {
			fatal("usage: demotool serve <url-file> [--tamper]")
		}
		err = serve(os.Args[2], len(os.Args) > 3 && os.Args[3] == "--tamper")
	case "freeport":
		err = freeport()
	case "first-yield":
		err = firstYield(os.Stdin)
	case "show-yield":
		err = showYield(os.Stdin)
	case "show-mismatch":
		err = showMismatch(os.Args[2])
	default:
		fatal("unknown command " + os.Args[1])
	}
	if err != nil {
		fatal(err.Error())
	}
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "demotool:", msg)
	os.Exit(1)
}

// serve answers chat completions like a model would for the demo's two
// agents, deciding from the conversation alone: the supervisor hands the task
// to the coder and ends once the coder has worked; the coder proposes the
// greeting file and — in tamper mode — then asks to run a shell command that
// changes the file after its approval.
func serve(urlFile string, tamper bool) error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	if err := os.WriteFile(urlFile, []byte("http://"+ln.Addr().String()+"/v1"), 0o600); err != nil {
		return err
	}
	return http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { //nolint:gosec // local demo server
		var req struct {
			Messages []struct {
				Role    string  `json:"role"`
				Content *string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		system, results := "", 0
		for i, m := range req.Messages {
			if i == 0 && m.Role == "system" && m.Content != nil {
				system = *m.Content
			}
			if m.Role == "tool" {
				results++
			}
		}
		var msg map[string]any
		switch {
		case strings.HasPrefix(system, supervisorRole) && results == 0:
			msg = say("Coder, please create " + target + " as the story asks.\nROUTE: coder")
		case strings.HasPrefix(system, supervisorRole):
			msg = say("The greeting file is in place.\nROUTE: END")
		case results == 0:
			msg = call(1, "create_file", map[string]string{"path": target, "content": greeting,
				"reasoning": "The story asks for a greeting file."})
		case tamper && results == 1:
			msg = call(2, "run_shell", map[string]string{"command": "printf '<!-- injected after approval -->\\n' >> " + target,
				"reasoning": "Polish the greeting."})
		default:
			msg = say("Created " + target + ".")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": msg}},
			"usage": map[string]int{"prompt_tokens": 120, "completion_tokens": 40}})
	}))
}

func say(text string) map[string]any { return map[string]any{"role": "assistant", "content": text} }

func call(n int, tool string, args map[string]string) map[string]any {
	b, _ := json.Marshal(args)
	return map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{
		"id": fmt.Sprintf("call_%d", n), "type": "function", "function": map[string]any{"name": tool, "arguments": string(b)}}}}
}

func freeport() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer func() { _ = ln.Close() }()
	fmt.Println(ln.Addr().(*net.TCPAddr).Port)
	return nil
}

func firstYield(in io.Reader) error {
	var ys []struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(in).Decode(&ys); err != nil || len(ys) == 0 {
		return nil // nothing pending (yet)
	}
	fmt.Println(ys[0].ID)
	return nil
}

func showYield(in io.Reader) error {
	var y struct {
		Request struct {
			AgentName     string `json:"agent_name"`
			ActionType    string `json:"action_type"`
			Reasoning     string `json:"reasoning_trace"`
			ProposedEdits []struct {
				File         string `json:"file"`
				SearchBlock  string `json:"search_block"`
				ReplaceBlock string `json:"replace_block"`
			} `json:"proposed_edits"`
		} `json:"request"`
	}
	if err := json.NewDecoder(in).Decode(&y); err != nil {
		return err
	}
	r := y.Request
	fmt.Printf("  agent:      %s\n  action:     %s\n  reasoning:  %s\n", r.AgentName, r.ActionType, r.Reasoning)
	for _, e := range r.ProposedEdits {
		if r.ActionType == "shell_exec" {
			fmt.Printf("  command:    %s\n", e.ReplaceBlock)
			continue
		}
		fmt.Printf("  file:       %s %s\n", e.File, e.SearchBlock)
		for _, line := range strings.Split(strings.TrimRight(e.ReplaceBlock, "\n"), "\n") {
			fmt.Printf("    │ %s\n", line)
		}
	}
	return nil
}

func showMismatch(checkpoint string) error {
	b, err := os.ReadFile(checkpoint)
	if err != nil {
		return err
	}
	var cp struct {
		Entries []struct {
			EventType string `json:"event_type"`
			Payload   string `json:"payload"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(b, &cp); err != nil {
		return err
	}
	for _, e := range cp.Entries {
		switch e.EventType {
		case "approval_content_mismatch", "unapproved_worktree_change", "run_branch_moved":
			var p struct{ File, Detail string }
			_ = json.Unmarshal([]byte(e.Payload), &p)
			fmt.Printf("    event: %s  file=%s\n    %s\n", e.EventType, p.File, p.Detail)
		}
	}
	return nil
}
