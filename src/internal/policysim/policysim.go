// Package policysim runs expected-outcome scenarios against a policy file through
// the same admission code a real run uses (the deciders: policy rules, guards,
// drift supervision, limits, task and evidence approval), so that what a policy
// must do and must not do can be asserted in CI. It is not the replay of past
// runs (`staircase policy test` without --scenarios), which reports differences and
// establishes nothing about limits, guards or checkpoints.
package policysim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/policy"
)

// Expected outcomes of one proposal.
const (
	Approve = "approve" // approved without a person (a policy rule, the agreed task, evidence)
	Reject  = "reject"  // rejected without a person (a policy rule)
	Refuse  = "refuse"  // refused by the orchestrator before anyone decided (a path outside the repository, a search text that is not there)
	Human   = "human"   // a person had to decide
)

// File is the format of a scenarios file.
type File struct {
	Scenarios []Scenario `json:"scenarios"`
}

// Scenario is one run: a repository, and proposals an agent makes in order, each
// with the outcome it must have.
type Scenario struct {
	Name  string            `json:"name"`
	Base  map[string]string `json:"base,omitempty"`  // the repository's files before the run
	Scope []string          `json:"scope,omitempty"` // the paths the story may change (drift supervision); none: no story
	// Options of the run that the policy is exercised under.
	ApproveInScope    bool     `json:"approve_in_scope,omitempty"`
	ApproveOnEvidence bool     `json:"approve_on_evidence,omitempty"`
	Checks            []string `json:"checks,omitempty"`
	HumanAnswer       string   `json:"human_answer,omitempty"` // what a person asked answers: "reject" (default) or "approve"
	Proposals         []Proposal
}

// Proposal is one proposal of the agent and the outcome it must have.
type Proposal struct {
	Agent          string  `json:"agent,omitempty"`
	Confidence     float64 `json:"confidence,omitempty"`
	Edits          []Edit  `json:"edits"`
	Expect         string  `json:"expect"`
	ReasonContains string  `json:"reason_contains,omitempty"` // text the reason (a guard, drift, a rule) must contain
}

// Edit is one file change: Content for a whole new file, Delete, or Search and Replace.
type Edit struct {
	File    string `json:"file"`
	Content string `json:"content,omitempty"`
	Delete  bool   `json:"delete,omitempty"`
	Search  string `json:"search,omitempty"`
	Replace string `json:"replace,omitempty"`
}

// Result is the check of one proposal.
type Result struct {
	Scenario string
	Index    int    // 1-based
	Want     string // the expected outcome
	Got      string // what happened
	Reason   string // the reason the decision recorded
	Pass     bool
	Detail   string // why it failed
}

// Load reads and validates a scenarios file: unknown fields, an empty inventory or a
// scenario that asserts nothing are errors, so a file cannot pass by saying nothing.
func Load(path string) (File, error) {
	var f File
	b, err := os.ReadFile(path)
	if err != nil {
		return f, err
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return f, fmt.Errorf("%s: %w", path, err)
	}
	return f, f.validate()
}

func (f File) validate() error {
	if len(f.Scenarios) == 0 {
		return errors.New("the scenarios file holds no scenario: nothing would be checked")
	}
	var errs []error
	seen := map[string]bool{}
	for i, s := range f.Scenarios {
		where := fmt.Sprintf("scenario %d (%q)", i+1, s.Name)
		if strings.TrimSpace(s.Name) == "" || seen[s.Name] {
			errs = append(errs, fmt.Errorf("scenario %d: its name must be set and unique", i+1))
		}
		seen[s.Name] = true
		if len(s.Proposals) == 0 {
			errs = append(errs, fmt.Errorf("%s asserts nothing: it has no proposals", where))
		}
		if s.HumanAnswer != "" && !slices.Contains([]string{"approve", "reject"}, s.HumanAnswer) {
			errs = append(errs, fmt.Errorf("%s: human_answer is approve or reject", where))
		}
		for j, p := range s.Proposals {
			pw := fmt.Sprintf("%s, proposal %d", where, j+1)
			if !slices.Contains([]string{Approve, Reject, Refuse, Human}, p.Expect) {
				errs = append(errs, fmt.Errorf("%s: expect is one of approve, reject, refuse, human, not %q", pw, p.Expect))
			}
			if len(p.Edits) == 0 {
				errs = append(errs, fmt.Errorf("%s has no edits", pw))
			}
			for _, e := range p.Edits {
				kinds := 0
				for _, set := range []bool{e.Content != "", e.Delete, e.Search != ""} {
					if set {
						kinds++
					}
				}
				if e.File == "" || kinds != 1 {
					errs = append(errs, fmt.Errorf("%s: an edit names a file and exactly one of content, delete, search+replace", pw))
				}
			}
		}
	}
	return errors.Join(errs...)
}

// Run runs every scenario under the policy in policyFile and returns the check of
// every proposal. The policy is loaded as a real run loads it (strict).
func Run(ctx context.Context, policyFile string, f File) ([]Result, error) {
	if err := f.validate(); err != nil {
		return nil, err
	}
	pol, err := os.ReadFile(policyFile)
	if err != nil {
		return nil, err
	}
	if _, err := policy.LoadEngineFile(policyFile); err != nil {
		return nil, err
	}
	// The runner reports progress on the standard streams; a scenario run is quiet.
	if null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0); err == nil {
		out, errOut := os.Stdout, os.Stderr
		os.Stdout, os.Stderr = null, null
		defer func() { os.Stdout, os.Stderr = out, errOut; _ = null.Close() }()
	}
	var all []Result
	for _, s := range f.Scenarios {
		rs, err := runScenario(ctx, pol, s)
		if err != nil {
			return all, fmt.Errorf("scenario %q: %w", s.Name, err)
		}
		all = append(all, rs...)
	}
	return all, nil
}

func runScenario(ctx context.Context, pol []byte, s Scenario) ([]Result, error) {
	ws, err := os.MkdirTemp("", "strc-sim-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(ws) }()
	if err := crypto.GenerateKey(ws); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(ws, "policy.json"), pol, 0o600); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(ws, "tmp"), 0o700); err != nil {
		return nil, err
	}
	repo, err := os.MkdirTemp("", "strc-sim-repo-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(repo) }()
	git := func(args ...string) error {
		if out, err := exec.Command("git", append([]string{"-C", repo, "-c", "core.hooksPath=/dev/null"}, args...)...).CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %w: %s", args, err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	for _, c := range [][]string{{"init", "-q", "-b", "main"}, {"config", "user.email", "sim@staircase.local"}, {"config", "user.name", "policy scenarios"}} {
		if err := git(c...); err != nil {
			return nil, err
		}
	}
	for name, content := range s.Base {
		p := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return nil, err
		}
	}
	if err := git("add", "-A"); err != nil {
		return nil, err
	}
	if err := git("commit", "-q", "--allow-empty", "-m", "base"); err != nil {
		return nil, err
	}

	db, err := persistence.InitDB(ws)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	store := persistence.NewStore(db)
	v, err := store.CreateVendor("scenarios")
	if err != nil {
		return nil, err
	}
	proj, err := store.CreateProject(v.ID, "scenarios", repo)
	if err != nil {
		return nil, err
	}
	if _, err := store.CreateSwarmTopology(proj.ID, "sup", "memory", "langgraph"); err != nil {
		return nil, err
	}
	c, err := store.CreateCase(proj.ID)
	if err != nil {
		return nil, err
	}
	if len(s.Scope) > 0 {
		st, err := store.CreateUserStory(c.ID, "scenario story")
		if err != nil {
			return nil, err
		}
		scope, _ := json.Marshal(map[string][]string{"allow": s.Scope})
		if err := store.SetUserStoryScope(st.ID, string(scope)); err != nil {
			return nil, err
		}
	}
	// a person, asked through the project's webhook
	var mu sync.Mutex
	var asked []domain.YieldRequest
	approve := s.HumanAnswer == "approve"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req domain.YieldRequest
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &req)
		mu.Lock()
		asked = append(asked, req)
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(domain.YieldResponse{Type: "yield_response", Approved: approve, Feedback: "answered by the scenario"})
	}))
	defer srv.Close()
	if err := store.UpdateProjectWebhook(proj.ID, srv.URL); err != nil {
		return nil, err
	}

	agent := orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
		for _, p := range s.Proposals {
			who := p.Agent
			if who == "" {
				who = "agent"
			}
			var edits []domain.ProposedEdit
			for _, e := range p.Edits {
				switch {
				case e.Delete:
					edits = append(edits, domain.ProposedEdit{File: e.File, SearchBlock: orchestrator.MarkerDeleteFile})
				case e.Search != "":
					edits = append(edits, domain.ProposedEdit{File: e.File, SearchBlock: e.Search, ReplaceBlock: e.Replace})
				default:
					edits = append(edits, domain.ProposedEdit{File: e.File, SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: e.Content})
				}
			}
			ap := env.Propose(ctx, domain.YieldRequest{AgentName: who, ActionType: domain.ActionFileEdit, ProposedEdits: edits, ConfidenceScore: p.Confidence})
			if ap.Approved {
				if err := ap.Apply(env.Worktree); err != nil {
					return err
				}
			}
		}
		return nil
	})
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	opts := orchestrator.RunOptions{Agent: agent, SkipGates: true, ApproveInScope: s.ApproveInScope, ApproveOnEvidence: s.ApproveOnEvidence, Checks: s.Checks}
	if s.ApproveInScope || s.ApproveOnEvidence {
		opts.Agreed = "scenarios"
	}
	// A run that does not succeed (a final review that is answered "reject", a failed
	// check) is no error here: the decisions it recorded are what is asserted.
	if err := orchestrator.NewRunner(store, ws).Run(runCtx, c.ID, opts); err != nil && !errors.Is(err, orchestrator.ErrRunNotSuccessful) {
		return nil, fmt.Errorf("the run could not be made: %w", err)
	}
	runs, err := store.ListRunsByCase(c.ID)
	if err != nil || len(runs) != 1 {
		return nil, fmt.Errorf("the scenario's run was not recorded: %v", err)
	}
	events, err := store.ListEventLogs(runs[0].ID)
	if err != nil {
		return nil, err
	}
	var decided []struct {
		Source, Feedback, Guard, Drift, Review string
		Approved                               bool
		ActionType                             string `json:"action_type"`
	}
	for _, e := range events {
		if e.EventType != "yield_decided" {
			continue
		}
		var d struct {
			Source, Feedback, Guard, Drift, Review string
			Approved                               bool
			ActionType                             string `json:"action_type"`
		}
		if json.Unmarshal([]byte(e.Payload), &d) == nil && d.ActionType == domain.ActionFileEdit {
			decided = append(decided, d)
		}
	}
	mu.Lock()
	humanAsked := slices.Clone(asked)
	mu.Unlock()
	var out []Result
	humans := 0
	for i, p := range s.Proposals {
		r := Result{Scenario: s.Name, Index: i + 1, Want: p.Expect}
		if i >= len(decided) {
			r.Got, r.Detail = "undecided", "the run ended before this proposal was decided"
			out = append(out, r)
			continue
		}
		d := decided[i]
		reasons := nonEmpty(d.Guard, d.Drift, d.Review, d.Feedback)
		switch {
		case d.Source == "operator":
			r.Got = Human
			if humans < len(humanAsked) { // what the person was told about why they were asked
				q := humanAsked[humans]
				reasons = append(nonEmpty(q.Guard, q.Drift, q.Review), reasons...)
			}
			humans++
		case d.Source == "orchestrator":
			r.Got = Refuse
		case d.Approved:
			r.Got = Approve
		default:
			r.Got = Reject
		}
		r.Reason = strings.TrimSpace(strings.Join(reasons, "; "))
		r.Pass = r.Got == p.Expect
		if !r.Pass {
			r.Detail = fmt.Sprintf("expected %s, got %s (decided by %s)", p.Expect, r.Got, d.Source)
		} else if p.ReasonContains != "" && !strings.Contains(r.Reason, p.ReasonContains) {
			r.Pass, r.Detail = false, fmt.Sprintf("the reason %q does not contain %q", r.Reason, p.ReasonContains)
		}
		out = append(out, r)
	}
	return out, nil
}

func nonEmpty(s ...string) []string {
	var out []string
	for _, x := range s {
		if strings.TrimSpace(x) != "" {
			out = append(out, x)
		}
	}
	return out
}
