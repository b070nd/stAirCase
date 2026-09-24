package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/b070nd/staircase-core/src/internal/llm"
)

// PlanVersion changes whenever a plan's meaning changes; run refuses plans of
// another version instead of running them with different semantics.
const PlanVersion = 1

// Plan is everything a compiled case runs: its topology, prompts and context.
// compile writes it as canonical JSON with a sha256 sidecar.
type Plan struct {
	Version         int         `json:"version"`
	CaseID          int64       `json:"case_id"`
	TopologyVersion int         `json:"topology_version"`
	Supervisor      string      `json:"supervisor"`
	Agents          []AgentSpec `json:"agents"`
	Edges           []Edge      `json:"edges"`
	PRD             string      `json:"prd"`
	RepoContext     string      `json:"repo_context"`
}

// AgentSpec is one agent of the topology.
type AgentSpec struct {
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

func isEnd(node string) bool { return node == "END" || node == "__end__" }

// builtinTools every agent has; naming one as an extra tool changes nothing.
var builtinTools = map[string]bool{"read_file": true, "list_dir": true, "request_edit": true,
	"create_file": true, "delete_file": true, "run_shell": true}

// Validate reports what would make the plan fail to run.
func (p Plan) Validate() error {
	var errs []error
	known := map[string]bool{}
	for _, a := range p.Agents {
		known[a.Name] = true
		if llm.SecretFor(a.Model) == "" {
			errs = append(errs, fmt.Errorf("agent %q: no provider serves model %q", a.Name, a.Model))
		}
		for _, t := range a.Tools {
			if !builtinTools[t] {
				errs = append(errs, fmt.Errorf("agent %q: unknown tool %q — only the built-in tools exist", a.Name, t))
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
		if !known[e.To] && !isEnd(e.To) {
			errs = append(errs, fmt.Errorf("edge %s → %s: unknown agent %q", e.From, e.To, e.To))
		}
	}
	return errors.Join(errs...)
}

// WritePlan validates p and writes it to path with a sha256 sidecar.
func WritePlan(path string, p Plan) error {
	p.Version = PlanVersion
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

// LoadPlan reads a plan written by WritePlan, refusing one that was changed
// after compile, written by another plan version, or no longer valid.
func LoadPlan(path string) (Plan, error) {
	var p Plan
	b, err := os.ReadFile(path)
	if err != nil {
		return p, err
	}
	want, err := os.ReadFile(path + ".sha256")
	if err != nil {
		return p, fmt.Errorf("plan has no checksum — recompile: %w", err)
	}
	if sum := sha256.Sum256(b); hex.EncodeToString(sum[:]) != strings.TrimSpace(string(want)) {
		return p, errors.New("plan was modified after compile — recompile")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, fmt.Errorf("read plan: %w", err)
	}
	if p.Version != PlanVersion {
		return p, fmt.Errorf("plan version %d, this staircase runs version %d — recompile", p.Version, PlanVersion)
	}
	return p, p.Validate()
}
