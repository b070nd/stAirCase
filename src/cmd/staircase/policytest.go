package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/domain"
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
they were. The replay applies the rules only, not the per-run limits.

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
		rep, err := policyReplay(persistence.NewStore(db), e)
		if err != nil {
			return err
		}
		fmt.Printf("%d past proposal(s) replayed against %s\n", rep.Proposals, args[0])
		fmt.Printf("  %d would be approved by the policy, as a person or the validator did before\n", rep.ApprovedByPersonToo)
		fmt.Printf("  %d stay as they were\n", rep.Unchanged)
		for _, title := range []struct {
			heading string
			items   []string
		}{{"⚠️  a person REJECTED these, the policy would APPROVE them:", rep.RejectedByPerson},
			{"the policy would reject these, which were approved:", rep.WouldReject}} {
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
	WouldReject         []string // the policy rejects what was approved
	Unchanged           int
}

// policyReplay replays the workspace's recorded proposals against e.
func policyReplay(store *persistence.Store, e *policy.Engine) (replayReport, error) {
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
				events, err := store.ListEventLogs(r.ID)
				if err != nil {
					return rep, err
				}
				var req domain.YieldRequest
				for _, ev := range events {
					switch ev.EventType {
					case "yield_request":
						req = domain.YieldRequest{}
						_ = json.Unmarshal([]byte(ev.Payload), &req)
					case "yield_decided":
						var d struct {
							Source, Drift, Guard string
							Approved             bool
						}
						if json.Unmarshal([]byte(ev.Payload), &d) != nil {
							continue
						}
						rep.Proposals++
						replayOne(&rep, e, r.ID, req, d.Source, d.Drift, d.Guard, d.Approved)
					}
				}
			}
		}
	}
	return rep, nil
}

func replayOne(rep *replayReport, e *policy.Engine, runID int64, req domain.YieldRequest, source, drift, guard string, approved bool) {
	dec := e.Evaluate(req)
	if source == "orchestrator" || drift != "" || guard != "" || !dec.Matched {
		rep.Unchanged++
		return
	}
	files := make([]string, 0, len(req.ProposedEdits))
	for _, ed := range req.ProposedEdits {
		files = append(files, ed.File)
	}
	what := fmt.Sprintf("run #%d: %s by %s", runID, strings.Join(files, ", "), req.AgentName)
	switch {
	case dec.Approved && approved && source == "policy":
		rep.Unchanged++
	case dec.Approved && approved:
		rep.ApprovedByPersonToo++
	case dec.Approved:
		rep.RejectedByPerson = append(rep.RejectedByPerson, what)
	case approved:
		rep.WouldReject = append(rep.WouldReject, what)
	default:
		rep.Unchanged++
	}
}
