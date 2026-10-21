package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/policy"
	"github.com/b070nd/stAirCase/src/internal/policysim"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var policyTestCmd = &cobra.Command{
	Use:   "test <policy-file>",
	Short: "Show what a policy would have decided differently on the proposals of past runs",
	Long: `Replays every proposal recorded in this workspace's runs against the rules of
a policy file (the same format as policy.json) and shows where it would have
decided differently: above all, changes a person rejected that the policy would
approve. Run it before you put a new rule into policy.json.

Shell commands, proposals refused by the orchestrator and proposals that drift
or a guard sent to a person are never the policy's to decide, so they stay as
they were. The replay applies the rules only, not the per-run limits. A rule's
conditions on paths, lines, files, new files and deletions are checked against
the edits each proposal recorded, so line counts and new files are estimates
here; --scenarios checks them exactly.

The replay also applies the paths only a person may change, whatever the policy says:
the paths that hold the checks' definitions (from the terms each run saved) and the
sensitive paths. A change that a rule, the agreed task, a reviewer model or evidence
approved or rejected there, with no person, is listed in its own section as one that
would now go to a person, and a person's earlier decision on such a path is not
counted as a difference the policy makes.

With --scenarios <file> it asserts instead. Each scenario in the file (JSON: a
base tree, an optional scope, and proposals with the outcome you expect:
approve, reject, refuse or human) is run through the real admission code with
this policy, limits and guards included. The command exits non-zero when any
outcome differs or the file asserts nothing, so CI can keep a policy from
approving what it must not, or from escalating what it should approve.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if path, _ := cmd.Flags().GetString("scenarios"); path != "" {
			return runScenarios(cmd, args[0], path)
		}
		e, err := policy.LoadEngineFile(args[0])
		if err != nil {
			return err
		}
		db, err := persistence.InitDB(viper.GetString("STAIRCASE_DIR"))
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()
		rep, err := policyReplay(persistence.NewStore(db), viper.GetString("STAIRCASE_DIR"), e)
		if err != nil {
			return err
		}
		fmt.Printf("%d past proposal(s) replayed against %s\n", rep.Proposals, args[0])
		fmt.Printf("  %d would be approved by the policy, as a person or the validator did before\n", rep.ApprovedByPersonToo)
		fmt.Printf("  %d stay as they were\n", rep.Unchanged)
		fmt.Printf("  %d would now go to a person, whatever the policy says\n", len(rep.WouldGoToPerson))
		for _, title := range []struct {
			heading string
			items   []string
		}{{"⚠️  a person REJECTED these, the policy would APPROVE them:", rep.RejectedByPerson},
			{"the policy would reject these, which were approved:", rep.WouldReject},
			{"⚠️  these were decided without a person, and a protected or sensitive path now sends them to one:", rep.WouldGoToPerson},
			{"⚠️  the terms these runs saved could not be read, so only the sensitive list was applied to them:", rep.Unchecked},
			{"⚠️  these runs were not replayed:", rep.Skipped}} {
			if len(title.items) > 0 {
				fmt.Printf("\n%s\n  %s\n", title.heading, strings.Join(title.items, "\n  "))
			}
		}
		return nil
	},
}

func init() {
	policyTestCmd.Flags().String("scenarios", "", "assert expected outcomes from this scenarios file instead of replaying past runs")
	policyCmd.AddCommand(policyTestCmd)
}

// runScenarios runs a scenario file against a policy and fails on any mismatch.
func runScenarios(cmd *cobra.Command, policyFile, scenarioFile string) error {
	f, err := policysim.Load(scenarioFile)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	res, err := policysim.Run(ctx, policyFile, f)
	if err != nil {
		return err
	}
	failed := 0
	for _, r := range res {
		if r.Pass {
			continue
		}
		failed++
		what := fmt.Sprintf("proposal %d", r.Index)
		if r.Index == 0 {
			what = "delivery"
		}
		fmt.Printf("FAIL %s, %s: %s\n  decided: %s (%s)\n", r.Scenario, what, r.Detail, r.Got, r.Reason)
	}
	fmt.Printf("%d scenario(s), %d proposal(s) asserted against %s: %d passed, %d failed\n", len(f.Scenarios), len(res), policyFile, len(res)-failed, failed)
	if failed > 0 {
		return fmt.Errorf("%d expectation(s) failed", failed)
	}
	return nil
}

// replayReport is what a policy would have decided differently in the past.
type replayReport struct {
	Proposals           int
	ApprovedByPersonToo int      // the policy approves what was approved before
	RejectedByPerson    []string // the policy approves what a person rejected
	WouldGoToPerson     []string // decided without a person where a protected or sensitive path now sends it to one
	Unchecked           []string // runs whose saved terms could not be read: only the sensitive list was applied to them
	WouldReject         []string // the policy rejects what was approved
	Unchanged           int
	Skipped             []string // runs whose history could not be read, and why
}

// policyReplay replays the workspace's recorded proposals against e.
func policyReplay(store *persistence.Store, wsDir string, e *policy.Engine) (replayReport, error) {
	var rep replayReport
	projects, err := store.ListAllProjects()
	if err != nil {
		return rep, err
	}
	for _, p := range projects {
		cases, err := store.ListCasesByProject(p.ID)
		if err != nil {
			return rep, err
		}
		for _, c := range cases {
			runs, err := store.ListRunsByCase(c.ID)
			if err != nil {
				return rep, err
			}
			for _, r := range runs {
				h, err := history.Read(store, r.ID)
				if err != nil { // a run whose history cannot be trusted is not counted: it is named instead
					rep.Skipped = append(rep.Skipped, err.Error())
					continue
				}
				terms, err := orchestrator.SavedTerms(wsDir, r.ID, h)
				if err != nil {
					terms = orchestrator.Terms{} // the protected paths are unknown, the sensitive list is not
					if !errors.Is(err, os.ErrNotExist) {
						rep.Unchecked = append(rep.Unchecked, fmt.Sprintf("run #%d: %v", r.ID, err))
					}
				}
				for _, d := range h.Decisions {
					if d.Request == nil { // a final review is a decision on the whole change, not a proposal
						continue
					}
					rep.Proposals++
					replayOne(&rep, e, terms, r.ID, d)
				}
			}
		}
	}
	return rep, nil
}

// replayFacts rebuilds the facts of a past proposal from what the run recorded: its edits, and the
// lines its decision counted (a decision a person made; otherwise they are estimated from the
// edit blocks). The base commit is not at hand, so a file is new when its edit says "whole new file"
// and the request did not record a file it replaced. This is an estimate; `policy test --scenarios`
// runs the real deciders.
func replayFacts(req domain.YieldRequest, lines int) policy.Facts {
	var f policy.Facts
	seen := map[string]int{}
	for _, ed := range req.ProposedEdits {
		file := policy.File{Path: ed.File}
		switch ed.SearchBlock {
		case orchestrator.MarkerNewFile:
			_, replaced := req.Before[ed.File]
			file.New = !replaced
			file.Lines = strings.Count(ed.ReplaceBlock, "\n")
		case orchestrator.MarkerDeleteFile:
			file.Deleted = true
		default:
			file.Lines = strings.Count(ed.SearchBlock, "\n") + strings.Count(ed.ReplaceBlock, "\n")
		}
		if i, ok := seen[ed.File]; ok { // one file, edited twice: one changed file
			f.Files[i].Lines += file.Lines
			f.Files[i].Deleted = file.Deleted
			continue
		}
		seen[ed.File] = len(f.Files)
		f.Files = append(f.Files, file)
	}
	for _, file := range f.Files {
		f.Lines += file.Lines
	}
	if lines > 0 {
		f.Lines = lines
	}
	return f
}

// replayOne counts one past decision. Before the policy is asked, the bars the deciders put ahead of it are: a file edit that
// holds a check's definition or touches a sensitive path goes to a person whatever the policy says, so what nobody decided there
// is reported in WouldGoToPerson, and a person's decision on it is not a difference the policy makes.
func replayOne(rep *replayReport, e *policy.Engine, terms orchestrator.Terms, runID int64, d history.Decision) {
	req := d.Request.YieldRequest
	if d.Source == "orchestrator" || d.Drift != "" || d.Guard != "" {
		rep.Unchanged++
		return
	}
	files := make([]string, 0, len(req.ProposedEdits))
	for _, ed := range req.ProposedEdits {
		files = append(files, ed.File)
	}
	what := fmt.Sprintf("run #%d: %s by %s", runID, strings.Join(files, ", "), req.AgentName)
	if req.ActionType == domain.ActionFileEdit {
		paths := d.Paths
		if len(paths) == 0 {
			paths = files
		}
		if bar := terms.PersonBar(paths); bar != "" {
			if d.Source == "operator" || d.Source == "parked" { // a person decided it, or will
				rep.Unchanged++
				return
			}
			verb := map[bool]string{true: "approved", false: "rejected"}[d.Approved]
			rep.WouldGoToPerson = append(rep.WouldGoToPerson, fmt.Sprintf("%s (%s by %s; %s: a person decides, whatever the policy says)", what, verb, d.Source, bar))
			return
		}
	}
	dec := e.Evaluate(req, replayFacts(req, d.Lines))
	if !dec.Matched {
		rep.Unchanged++
		return
	}
	switch {
	case dec.Approved && d.Approved && d.Source == "policy":
		rep.Unchanged++
	case dec.Approved && d.Approved:
		rep.ApprovedByPersonToo++
	case dec.Approved:
		rep.RejectedByPerson = append(rep.RejectedByPerson, what)
	case d.Approved:
		rep.WouldReject = append(rep.WouldReject, what)
	default:
		rep.Unchanged++
	}
}
