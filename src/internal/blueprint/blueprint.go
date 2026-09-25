// Package blueprint reads a blueprint — a project's automation (agents,
// prompts, routing, cases, stories and their scope) kept as files in its own
// repository — into an immutable snapshot identified by its content hash.
package blueprint

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"

	"github.com/b070nd/staircase-core/src/internal/domain"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/b070nd/staircase-core/src/internal/plan"
	"gopkg.in/yaml.v3"
)

// FileName is the blueprint's entry file inside its directory.
const FileName = "blueprint.yaml"

// Blueprint is the resolved content of a blueprint: file references are
// replaced by the files' text, so the snapshot stands on its own.
type Blueprint struct {
	Name       string      `yaml:"name" json:"name"`
	Supervisor string      `yaml:"supervisor" json:"supervisor"`
	Agents     []Agent     `yaml:"agents" json:"agents"`
	Edges      []Edge      `yaml:"edges" json:"edges"`
	Cases      []Case      `yaml:"cases" json:"cases"`
	Limits     plan.Limits `yaml:"limits" json:"limits"` // enforced by drift supervision
}

// Agent is one agent; its prompt is inline or in a file of the blueprint.
type Agent struct {
	Name       string `yaml:"name" json:"name"`
	Model      string `yaml:"model" json:"model"`
	Prompt     string `yaml:"prompt" json:"prompt"`
	PromptFile string `yaml:"prompt_file" json:"-"`
}

// Edge is a possible next step; Condition is the ROUTE label that selects it.
type Edge struct {
	From      string `yaml:"from" json:"from"`
	To        string `yaml:"to" json:"to"`
	Condition string `yaml:"condition" json:"condition,omitempty"`
}

// Case is a unit of work the blueprint creates in every project bound to it.
type Case struct {
	Slug    string  `yaml:"slug" json:"slug"`
	PRD     string  `yaml:"prd" json:"prd"`
	PRDFile string  `yaml:"prd_file" json:"-"`
	Stories []Story `yaml:"stories" json:"stories"`
}

// Story is one acceptance item and the paths it may change.
type Story struct {
	Text  string `yaml:"text" json:"text"`
	Scope Scope  `yaml:"scope" json:"scope"`
}

// Scope limits what a story's run may change.
type Scope struct {
	Allow    []string `yaml:"allow" json:"allow,omitempty"`
	MaxFiles int      `yaml:"max_files" json:"max_files,omitempty"`
}

// Load reads dir/blueprint.yaml, rejecting unknown fields, and resolves its
// file references, which must stay inside dir (no "..", absolute paths or
// symlinks leading out).
func Load(dir string) (Blueprint, error) {
	var b Blueprint
	root, err := os.OpenRoot(dir)
	if err != nil {
		return b, err
	}
	defer func() { _ = root.Close() }()
	src, err := root.ReadFile(FileName)
	if err != nil {
		return b, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)
	if err := dec.Decode(&b); err != nil {
		return b, fmt.Errorf("%s: %w", FileName, err)
	}
	resolve := func(what, inline string, file *string) (string, error) {
		switch {
		case *file == "":
			if inline == "" {
				return "", fmt.Errorf("%s: set its text or a file", what)
			}
			return inline, nil
		case inline != "":
			return "", fmt.Errorf("%s: set its text or a file, not both", what)
		}
		text, err := root.ReadFile(*file)
		if err != nil {
			return "", fmt.Errorf("%s: %w", what, err)
		}
		*file = ""
		return string(text), nil
	}
	for i := range b.Agents {
		a := &b.Agents[i]
		if a.Prompt, err = resolve("agent "+a.Name+" prompt", a.Prompt, &a.PromptFile); err != nil {
			return b, err
		}
	}
	for i := range b.Cases {
		c := &b.Cases[i]
		if c.PRD, err = resolve("case "+c.Slug+" prd", c.PRD, &c.PRDFile); err != nil {
			return b, err
		}
	}
	return b, b.validate()
}

// Parse reads a snapshot stored by JSON.
func Parse(content []byte) (Blueprint, error) {
	var b Blueprint
	dec := json.NewDecoder(bytes.NewReader(content))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return b, err
	}
	return b, b.validate()
}

// JSON is the canonical snapshot: struct field order, resolved text.
func (b Blueprint) JSON() []byte {
	out, _ := json.Marshal(b) // plain strings, ints and slices: cannot fail
	return out
}

// Hash identifies the snapshot: the sha256 of its canonical JSON.
func (b Blueprint) Hash() string {
	sum := sha256.Sum256(b.JSON())
	return hex.EncodeToString(sum[:])
}

func (b Blueprint) validate() error {
	var errs []error
	if b.Name == "" {
		errs = append(errs, errors.New("name is required"))
	}
	p := b.topology()
	if err := p.Validate(); err != nil {
		errs = append(errs, err)
	}
	slugs := map[string]bool{}
	for _, c := range b.Cases {
		if c.Slug == "" || slugs[c.Slug] {
			errs = append(errs, fmt.Errorf("case slug %q: must be set and unique", c.Slug))
		}
		slugs[c.Slug] = true
		for _, s := range c.Stories {
			if s.Text == "" {
				errs = append(errs, fmt.Errorf("case %s: a story has no text", c.Slug))
			}
		}
	}
	return errors.Join(errs...)
}

// topology is the blueprint's agents and routing as a plan.
func (b Blueprint) topology() plan.Plan {
	p := plan.Plan{Supervisor: b.Supervisor}
	for _, a := range b.Agents {
		p.Agents = append(p.Agents, plan.Agent{Name: a.Name, Role: a.Prompt, Model: a.Model})
	}
	for _, e := range b.Edges {
		p.Edges = append(p.Edges, plan.Edge(e))
	}
	return p
}

// Check reports how a compiled plan differs from what the blueprint defines
// for case slug: its topology, PRD and stories (text and scope).
func (b Blueprint) Check(p plan.Plan, slug string) error {
	var c *Case
	for i := range b.Cases {
		if b.Cases[i].Slug == slug {
			c = &b.Cases[i]
		}
	}
	if c == nil {
		return fmt.Errorf("blueprint %s has no case %q", b.Name, slug)
	}
	want := b.topology()
	var errs []error
	if p.Supervisor != want.Supervisor {
		errs = append(errs, fmt.Errorf("supervisor is %q, the blueprint says %q", p.Supervisor, want.Supervisor))
	}
	if !reflect.DeepEqual(p.Agents, want.Agents) {
		errs = append(errs, errors.New("agents differ from the blueprint"))
	}
	if !reflect.DeepEqual(p.Edges, want.Edges) {
		errs = append(errs, errors.New("edges differ from the blueprint"))
	}
	if p.PRD != c.PRD {
		errs = append(errs, errors.New("PRD differs from the blueprint"))
	}
	var got, wantStories []Story
	for _, s := range p.Stories {
		got = append(got, Story{Text: s.Text, Scope: Scope{Allow: s.Allow, MaxFiles: s.MaxFiles}})
	}
	for _, s := range c.Stories {
		wantStories = append(wantStories, Story{Text: s.Text, Scope: Scope{Allow: s.Scope.Allow, MaxFiles: s.Scope.MaxFiles}})
	}
	if !reflect.DeepEqual(got, wantStories) {
		errs = append(errs, errors.New("stories differ from the blueprint"))
	}
	if p.Limits != b.Limits {
		errs = append(errs, errors.New("limits differ from the blueprint"))
	}
	return errors.Join(errs...)
}

// Binding is what binding a project to this blueprint creates.
func (b Blueprint) Binding() persistence.Binding {
	out := persistence.Binding{Hash: b.Hash(), Supervisor: b.Supervisor}
	for _, a := range b.Agents {
		out.Agents = append(out.Agents, domain.AgentNode{Name: a.Name, Role: a.Prompt, Model: a.Model})
	}
	for _, e := range b.Edges {
		out.Edges = append(out.Edges, domain.Edge{FromNode: e.From, ToNode: e.To, Condition: e.Condition})
	}
	for _, c := range b.Cases {
		bc := persistence.BoundCase{Slug: c.Slug, PRD: c.PRD}
		for _, s := range c.Stories {
			cfg := ""
			if s.Scope.Allow != nil || s.Scope.MaxFiles != 0 {
				j, _ := json.Marshal(s.Scope)
				cfg = string(j)
			}
			bc.Stories = append(bc.Stories, domain.UserStory{Description: s.Text, CustomConfig: cfg})
		}
		out.Cases = append(out.Cases, bc)
	}
	return out
}
