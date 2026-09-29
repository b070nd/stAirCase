// Package plan is what compile produces and run executes: a case's topology,
// prompts and context as canonical JSON, checked before every run.
package plan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/llm"
)

// Version changes whenever a plan's meaning changes; run refuses plans of
// another version instead of running them with different semantics.
const Version = 2

// Plan is everything a compiled case runs: its topology, prompts and context.
// compile writes it as canonical JSON with a sha256 sidecar.
type Plan struct {
	Version         int     `json:"version"`
	CaseID          int64   `json:"case_id"`
	TopologyVersion int     `json:"topology_version"`
	Supervisor      string  `json:"supervisor"`
	Agents          []Agent `json:"agents"`
	Edges           []Edge  `json:"edges"`
	PRD             string  `json:"prd"`
	RepoContext     string  `json:"repo_context"`
	Stories         []Story `json:"stories,omitempty"`
	// Lessons are what reviewers of the project rejected before, with their
	// reasons, newest first: the agents read them in the brief.
	Lessons []string `json:"lessons,omitempty"`
	// Harness is the external agent that runs the case (see Harnesses); empty
	// means the built-in agents above. A harness plan has no agents of its own.
	Harness string `json:"harness,omitempty"`
	// Review is, for the "review" harness, the change made elsewhere that the
	// run reviews: its ref, the exact commit and who made it.
	Review *Review `json:"review,omitempty"`
	// BlueprintHash is the blueprint the case was bound from, if any.
	BlueprintHash string `json:"blueprint_hash,omitempty"`
	// Limits are the blueprint's run limits (drift supervision); zero = none.
	Limits Limits `json:"limits,omitzero"`

	// Digest is the sha256 of the plan file, set by Load.
	Digest string `json:"-"`
}

// Limits bound a run (drift supervision); zero values mean no limit. A
// blueprint declares them (YAML), a plan carries them, policy.json adds its own.
type Limits struct {
	CheckpointEvery    int `yaml:"checkpoint_every" json:"checkpoint_every,omitempty"`         // every Nth proposal goes to a human
	MaxFilesChanged    int `yaml:"max_files_changed" json:"max_files_changed,omitempty"`       // past this many distinct files, a human decides
	MaxScopeViolations int `yaml:"max_scope_violations" json:"max_scope_violations,omitempty"` // more out-of-scope proposals halt the run
	MaxRunSecs         int `yaml:"max_run_secs" json:"max_run_secs,omitempty"`                 // a longer run is halted
}

// Tighter combines two sets of limits, keeping the stricter of each: a
// workspace policy can only tighten a blueprint's limits.
func (l Limits) Tighter(o Limits) Limits {
	return Limits{
		CheckpointEvery:    Stricter(l.CheckpointEvery, o.CheckpointEvery),
		MaxFilesChanged:    Stricter(l.MaxFilesChanged, o.MaxFilesChanged),
		MaxScopeViolations: Stricter(l.MaxScopeViolations, o.MaxScopeViolations),
		MaxRunSecs:         Stricter(l.MaxRunSecs, o.MaxRunSecs),
	}
}

// Stricter is the smaller of two limits, where 0 means no limit.
func Stricter(a, b int) int {
	if a == 0 || (b != 0 && b < a) {
		return b
	}
	return a
}

// Brief is the case as its agents are told it: the PRD, then each story with
// the paths it may change.
func (p Plan) Brief() string {
	var b strings.Builder
	b.WriteString(p.PRD)
	if len(p.Stories) > 0 {
		b.WriteString("\n\nStories:")
		for _, s := range p.Stories {
			fmt.Fprintf(&b, "\n- %s", s.Text)
			if len(s.Allow) > 0 {
				fmt.Fprintf(&b, " (may change only: %s)", strings.Join(s.Allow, ", "))
			}
		}
		b.WriteString("\n\nChanges outside these paths go to a human reviewer and can halt the run.")
	}
	if len(p.Lessons) > 0 {
		b.WriteString("\n\nReviewers of this project rejected before - do not repeat:")
		for _, l := range p.Lessons {
			fmt.Fprintf(&b, "\n- %s", l)
		}
	}
	return strings.TrimSpace(b.String())
}

// Story is one of the case's stories and the paths it may change.
type Story struct {
	ID       int64    `json:"id"`
	Text     string   `json:"text"`
	Allow    []string `json:"allow,omitempty"`
	MaxFiles int      `json:"max_files,omitempty"`
}

// Agent is one agent of the topology.
type Agent struct {
	Name  string   `json:"name"`
	Role  string   `json:"role"` // its system prompt
	Model string   `json:"model"`
	Tools []string `json:"tools,omitempty"` // extra tools; only built-ins exist
}

// Edge is a possible next step from one agent to another (or to END);
// Condition, when set, is the ROUTE label that selects it.
type Edge struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Condition string `json:"condition,omitempty"`
}

// IsEnd reports whether node is the END sentinel.
func IsEnd(node string) bool { return node == "END" || node == "__end__" }

// builtinTools every agent has; naming one as an extra tool changes nothing.
var builtinTools = map[string]bool{"read_file": true, "list_dir": true, "request_edit": true,
	"create_file": true, "delete_file": true, "run_shell": true}

// Harnesses are the external agents a case can be run by instead of the
// built-in agents: they bring their own model and login, and every tool
// call goes through the run's hooks.
var Harnesses = []string{"claude-code", "codex", "gemini", "review"}

// Review is a change made elsewhere (for example a cloud agent's pull request)
// that a run reviews file by file.
type Review struct {
	Ref     string `json:"ref"`
	Commit  string `json:"commit"`
	By      string `json:"by"`
	Message string `json:"message,omitempty"` // the commit's own message (staircase seal)
}

// Validate reports what would make the plan fail to run.
func (p Plan) Validate() error {
	if p.Harness != "" {
		switch {
		case !slices.Contains(Harnesses, p.Harness):
			return fmt.Errorf("unknown harness %q - supported: %s", p.Harness, strings.Join(Harnesses, ", "))
		case len(p.Agents) > 0 || len(p.Edges) > 0 || p.Supervisor != "":
			return fmt.Errorf("the plan runs harness %q but also has agents of its own", p.Harness)
		case (p.Harness == "review") != (p.Review != nil && p.Review.Commit != ""):
			return errors.New("only a review plan names the commit it reviews, and it must")
		}
		return nil
	}
	var errs []error
	known := map[string]bool{}
	for _, a := range p.Agents {
		known[a.Name] = true
		if llm.SecretFor(a.Model) == "" {
			errs = append(errs, fmt.Errorf("agent %q: no provider serves model %q", a.Name, a.Model))
		}
		for _, t := range a.Tools {
			if !builtinTools[t] {
				errs = append(errs, fmt.Errorf("agent %q: unknown tool %q - only the built-in tools exist", a.Name, t))
			}
		}
	}
	if !known[p.Supervisor] {
		errs = append(errs, fmt.Errorf("supervisor %q is not an agent of the topology", p.Supervisor))
	}
	for _, e := range p.Edges {
		if !known[e.From] {
			errs = append(errs, fmt.Errorf("edge %s → %s: unknown agent %q", e.From, e.To, e.From))
		}
		if !known[e.To] && !IsEnd(e.To) {
			errs = append(errs, fmt.Errorf("edge %s → %s: unknown agent %q", e.From, e.To, e.To))
		}
	}
	return errors.Join(errs...)
}

// Write validates p and writes it to path with a sha256 sidecar.
func Write(path string, p Plan) error {
	p.Version = Version
	if err := p.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	sum := sha256.Sum256(b)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return err
	}
	return os.WriteFile(path+".sha256", []byte(hex.EncodeToString(sum[:])), 0o600)
}

// Load reads a plan written by Write, refusing one that was changed
// after compile, written by another plan version, or no longer valid.
func Load(path string) (Plan, error) {
	var p Plan
	b, err := os.ReadFile(path)
	if err != nil {
		return p, err
	}
	want, err := os.ReadFile(path + ".sha256")
	if err != nil {
		return p, fmt.Errorf("plan has no checksum - recompile: %w", err)
	}
	if sum := sha256.Sum256(b); hex.EncodeToString(sum[:]) != strings.TrimSpace(string(want)) {
		return p, errors.New("plan was modified after compile - recompile")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, fmt.Errorf("read plan: %w", err)
	}
	p.Digest = strings.TrimSpace(string(want))
	if p.Version != Version {
		return p, fmt.Errorf("plan version %d, this staircase runs version %d - recompile", p.Version, Version)
	}
	return p, p.Validate()
}
