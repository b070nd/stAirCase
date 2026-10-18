package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/agent"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/sandbox"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Governing a session another program starts (ADR 0008): a runner such as Cloudroom names `staircase host claude` as its Claude Code.

var hostCmd = &cobra.Command{
	Use:   "host",
	Short: "Govern Claude Code sessions another program (a runner such as Cloudroom) starts",
	Long: `A runner that starts Claude Code for you can start it through stAirCase instead: name "staircase host claude" as the
runner's Claude Code program. Each session it starts is an away run in the repository it runs in: nobody is asked, a change is
approved only when your checks pass on it, anything that needs you is parked, and the change waits for your review
(staircase decide). The runner's protocol passes through untouched; stAirCase's hooks decide, and nothing the runner passes widens
what the agent may do: the runner's MCP tools are decided like commands (parked while away unless declared), no other
settings, no argument stAirCase does not know.

Set the workspace up once with the scope and the checks:
  staircase host setup --allow "src/**" --check "go test ./..."`,
}

var hostClaudeCmd = &cobra.Command{
	Use:                "claude [the runner's Claude Code arguments]",
	Short:              "Stand in for Claude Code: the runner's session, governed as an away run",
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return hostClaude(args, os.Stdin, os.Stdout)
	},
}

var hostSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Set the scope, checks and credential names for hosted sessions in this workspace",
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(hostSetupAllow) == 0 || len(hostSetupChecks) == 0 {
			return errors.New("hosted sessions are away runs: name the scope with --allow and at least one --check")
		}
		// a hosted session is an away run under these terms: judged now, not when the runner first starts one
		if _, err := orchestrator.NewTerms(orchestrator.Terms{Agreed: "the runner's session", Away: true, Checks: hostSetupChecks,
			Sandbox: hostSetupSandbox, AllowShellExec: len(hostSetupAwayCommands) > 0, AwayCommands: hostSetupAwayCommands,
			AwayTools: hostSetupAwayTools}, "claude-code"); err != nil {
			return err
		}
		p := hostProfile{Allow: hostSetupAllow, Checks: hostSetupChecks, PassEnv: hostSetupPassEnv, Sandbox: hostSetupSandbox, AwayCommands: hostSetupAwayCommands, AwayTools: hostSetupAwayTools}
		b, _ := json.MarshalIndent(p, "", "  ")
		ws := viper.GetString("STAIRCASE_DIR")
		if err := os.MkdirAll(ws, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(ws, "host.json"), append(b, '\n'), 0o600); err != nil {
			return err
		}
		fmt.Printf("Hosted sessions in %s: scope %s, checks %s. Name this program as the runner's Claude Code:\n  staircase host claude\n",
			ws, strings.Join(p.Allow, ", "), strings.Join(p.Checks, "; "))
		return nil
	},
}

var (
	hostSetupAllow, hostSetupChecks, hostSetupPassEnv, hostSetupAwayCommands, hostSetupAwayTools []string
	hostSetupSandbox                                                                             string
)

func init() {
	hostSetupCmd.Flags().StringArrayVar(&hostSetupAllow, "allow", nil, "A path (glob) hosted sessions may change; repeat for more")
	hostSetupCmd.Flags().StringArrayVar(&hostSetupChecks, "check", nil, "A command (such as your tests) a change must pass to be approved; repeat for more")
	hostSetupCmd.Flags().StringArrayVar(&hostSetupAwayTools, "away-tool", nil, "An exact MCP tool (mcp__<server>__<tool>) hosted sessions may call without asking; other tool calls are parked")
	hostSetupCmd.Flags().StringArrayVar(&hostSetupAwayCommands, "away-command", nil, "An exact command hosted sessions may run without asking (ADR 0009); needs a sandbox without network")
	hostSetupCmd.Flags().StringVar(&hostSetupSandbox, "sandbox", "", "Where the checks run, as staircase run --sandbox (for example docker:image=golang:1.26-bookworm)")
	hostSetupCmd.Flags().StringArrayVar(&hostSetupPassEnv, "pass-env", nil,
		"Name of an environment variable Claude Code may inherit, for its credential (for example CLAUDE_CODE_OAUTH_TOKEN); repeat for more")
	hostCmd.AddCommand(hostClaudeCmd, hostSetupCmd)
	rootCmd.AddCommand(hostCmd)
}

// hostProfile is how a workspace hosts sessions: written by `staircase host setup`, read by `staircase host claude`.
type hostProfile struct {
	Allow   []string `json:"allow"`
	Checks  []string `json:"checks"`
	PassEnv []string `json:"pass_env,omitempty"`
	Sandbox string   `json:"sandbox,omitempty"` // as --sandbox; empty is auto
	// AwayCommands are the exact commands a hosted session may run without asking (ADR 0009); they turn commands on for it.
	AwayCommands []string `json:"away_commands,omitempty"`
	// AwayTools are the exact MCP tools (mcp__server__tool) a hosted session may call without asking; other calls are parked.
	AwayTools []string `json:"away_tools,omitempty"`
}

// hostedArgs is what of a runner's Claude Code arguments may pass, and what is taken out to be applied by stAirCase.
type hostedArgs struct {
	pass         []string
	settings     map[string]any
	systemPrompt string
	session      string
}

// The runner's arguments the stand-in knows: those that pass as they are (with or without a value), those it takes over, and those
// it drops. Anything else is refused: an unknown argument could widen what the agent may do.
var (
	hostPassFlags = map[string]bool{"-p": true, "--print": true, "--verbose": true, "--include-partial-messages": true, "--replay-user-messages": true, "--dangerously-skip-permissions": true, "--no-chrome": true}
	// --mcp-config passes: every call of the runner's MCP tools is a proposal stAirCase decides (ADR 0008, 1.3), and with
	// --strict-mcp-config only the configuration the runner gave loads.
	hostPassValues = map[string]bool{"--input-format": true, "--output-format": true, "--model": true, "--disallowedTools": true, "--mcp-config": true}
	hostDropValues = map[string]string{
		"--permission-prompts": "prompts are not stAirCase's gate: its hooks decide (and Claude Code 2.1.236 does not know the flag)",
	}
)

func parseHostedArgs(args []string) (hostedArgs, error) {
	h := hostedArgs{settings: map[string]any{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		value := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", a)
			}
			i++
			return args[i], nil
		}
		switch {
		case hostPassFlags[a]:
			h.pass = append(h.pass, a)
		case hostPassValues[a]:
			v, err := value()
			if err != nil {
				return h, err
			}
			h.pass = append(h.pass, a, v)
		case hostDropValues[a] != "":
			if _, err := value(); err != nil {
				return h, err
			}
		case a == "--settings":
			v, err := value()
			if err != nil {
				return h, err
			}
			var s map[string]any
			if json.Unmarshal([]byte(v), &s) != nil {
				return h, errors.New("--settings is given as JSON by a runner; a settings file is not taken")
			}
			for k, x := range s {
				h.settings[k] = x
			}
		case a == "--append-system-prompt":
			v, err := value()
			if err != nil {
				return h, err
			}
			h.systemPrompt = v
		case a == "--session-id":
			v, err := value()
			if err != nil {
				return h, err
			}
			h.session = v
		case a == "--resume" || a == "--fork-session" || a == "--resume-session-at" || a == "--continue":
			return h, fmt.Errorf("%s: a resumed or forked session is not hosted yet: start a new session (ADR 0008, first slice)", a)
		default:
			return h, fmt.Errorf("%s: an argument staircase host does not know, so it is not passed on (it could widen what the agent may do)", a)
		}
	}
	if h.session == "" {
		return h, errors.New("the runner gave no --session-id: a hosted session needs one")
	}
	return h, nil
}

// hostClaude runs Claude Code for a runner's session as an away run. stdin and stdout are the runner's protocol: they reach
// Claude Code untouched, and stAirCase's own output goes to a log in the workspace instead.
func hostClaude(args []string, stdin io.Reader, stdout io.Writer) error {
	h, err := parseHostedArgs(args)
	if err != nil {
		return err
	}
	ws := viper.GetString("STAIRCASE_DIR")
	var p hostProfile
	if b, err := os.ReadFile(filepath.Join(ws, "host.json")); err != nil || json.Unmarshal(b, &p) != nil || len(p.Allow) == 0 || len(p.Checks) == 0 {
		return errors.New("this workspace is not set up to host sessions: staircase host setup --allow <paths> --check <command>")
	}
	if err := os.MkdirAll(filepath.Join(ws, "host"), 0o700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(ws, "host", sanitizeName(h.session)+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = logFile.Close() }()
	realStdout := os.Stdout
	os.Stdout = logFile // stAirCase's own output must not reach the runner's stream
	defer func() { os.Stdout = realStdout }()

	// A hosted session is launched from its profile alone: nothing of this process's own flags carries into it.
	l := launch{
		terms: orchestrator.Terms{Away: true, Checks: p.Checks, Sandbox: orDefaultStr(p.Sandbox, sandbox.Auto),
			AllowShellExec: len(p.AwayCommands) > 0, AwayCommands: p.AwayCommands, AwayTools: p.AwayTools},
		allow: p.Allow, passEnv: p.PassEnv, yes: true, agent: "built-in",
		host: &agent.HostIO{Args: h.pass, Settings: h.settings, SystemPrompt: h.systemPrompt, SessionID: h.session, Stdin: stdin, Stdout: stdout},
	}
	task := fmt.Sprintf("A session started by another program (session %s); its requests reach the agent through that program.", h.session)
	return session(l, "claude-code", "Claude Code", "host", []string{task})
}

// sanitizeName keeps a session id usable as a file name.
func sanitizeName(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return r
		}
		return '_'
	}, s)
}
