package main

import (
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/b070nd/stAirCase/src/internal/changeset"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/engine"
	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/profile"
	"github.com/b070nd/stAirCase/src/internal/tui"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// changeSetFlag adds --change-set to a command that starts a run.
func changeSetFlag(cmd *cobra.Command) {
	cmd.Flags().StringVar(&cli.terms.ChangeSet, "change-set", "",
		"The change set this run belongs to (staircase changeset create): the id goes on the run's record and certificate. The run stays one repository")
}

// checkChangeSet refuses a run for a change set that does not exist: its id is written on a certificate, so a misspelt one must not be.
func checkChangeSet(l *launch, wsDir string) error {
	if l.terms.ChangeSet == "" {
		return nil
	}
	_, err := changeset.Load(wsDir, l.terms.ChangeSet)
	return err
}

var changesetCmd = &cobra.Command{
	Use:   "changeset",
	Short: "Tie runs of several repositories into one piece of work, and verify them together",
	Long: `A change that spans a backend, a middleware and a frontend is several runs, one per repository, and each stays what it always is: one
repository, its own worktree, audit chain and change certificate. A change set is the id that ties them together.

  staircase changeset create checkout-v2
  staircase claude --change-set checkout-v2 "add the order endpoint"     (in each repository)
  staircase changeset add checkout-v2 12 13 14
  staircase changeset contract checkout-v2 backend:api/openapi.yaml
  staircase changeset ack checkout-v2 backend:api/openapi.yaml --by frontend
  staircase changeset verify checkout-v2

A contract is a file that more than one repository depends on. When a member's run changed one, every other member must have acknowledged
that exact content: a person looked at the change and the other repository accepts it. Verifying the set checks that every member run
was delivered, that its certificate verifies, reaches the level asked and names this set, and that no contract change is unacknowledged.

The set is a file in the workspace; nothing of it is in any repository. An acknowledgement is a record in that file, not a signature:
whoever can write the workspace can write it, as for a project's profile. Make a contract's path sensitive in the profile
(staircase project profile set ... --sensitive) so that a change to it always needs a person.`,
}

var changesetCreateCmd = &cobra.Command{
	Use:   "create <id>",
	Short: "Make an empty change set",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, err := changeset.Create(viper.GetString("STAIRCASE_DIR"), args[0], time.Now()); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Change set %s is made. Start each run with --change-set %s, then staircase changeset add %s <run-id>...\n", args[0], args[0], args[0])
		return nil
	},
}

var changesetAddCmd = &cobra.Command{
	Use:   "add <id> <run-id>...",
	Short: "Make runs members of a change set (one run per project; a newer one replaces it)",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		var runs []int64
		for _, a := range args[1:] {
			id, err := parseID("run-id", a)
			if err != nil {
				return err
			}
			runs = append(runs, id)
		}
		return changesetAdd(cmd.OutOrStdout(), args[0], runs)
	},
}

var changesetContractCmd = &cobra.Command{
	Use:   "contract <id> <project>:<path>",
	Short: "Declare a file that several repositories depend on",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return changesetContract(cmd.OutOrStdout(), args[0], args[1])
	},
}

var changesetAckBy string

var changesetAckCmd = &cobra.Command{
	Use:   "ack <id> <project>:<path> --by <project>",
	Short: "Record that a repository accepts a member's change to a contract",
	Long: `Records that a person looked at the member run's change to the contract and that the project named by --by accepts it. The record is
of the content the change produced: a later change to the file needs a new acknowledgement. Repeat the command for each other member.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if changesetAckBy == "" {
			return fmt.Errorf("say which project accepts the change: --by <project>")
		}
		return changesetAck(cmd.OutOrStdout(), args[0], args[1], changesetAckBy)
	},
}

var changesetMinCAL int

var changesetVerifyCmd = &cobra.Command{
	Use:   "verify <id>",
	Short: "Check a whole change set: every run delivered, every certificate valid, every contract change acknowledged",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if changesetMinCAL < 0 || changesetMinCAL > 4 {
			return fmt.Errorf("--min-cal is a change assurance level from 1 to 4")
		}
		return changesetVerify(cmd.OutOrStdout(), args[0], changesetMinCAL)
	},
}

var changesetShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show a change set: its runs, contracts and acknowledgements",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return changesetShow(cmd.OutOrStdout(), args[0])
	},
}

var changesetListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the workspace's change sets",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ids, err := changeset.List(viper.GetString("STAIRCASE_DIR"))
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No change sets. Make one with staircase changeset create <id>.")
		}
		for _, id := range ids {
			fmt.Fprintln(cmd.OutOrStdout(), id)
		}
		return nil
	},
}

func init() {
	changesetAckCmd.Flags().StringVar(&changesetAckBy, "by", "", "The project (id or name) that accepts the change")
	changesetVerifyCmd.Flags().IntVar(&changesetMinCAL, "min-cal", 0, "Fail a member whose certificate is below this change assurance level (1-4)")
	changesetCmd.AddCommand(changesetCreateCmd, changesetAddCmd, changesetContractCmd, changesetAckCmd, changesetVerifyCmd, changesetShowCmd, changesetListCmd)
	rootCmd.AddCommand(changesetCmd)
}

// projectRef finds a project by id or by name; a name that two vendors share is refused, never guessed.
func projectRef(store *persistence.Store, ref string) (*domain.Project, error) {
	projects, err := store.ListAllProjects()
	if err != nil {
		return nil, err
	}
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		for i := range projects {
			if projects[i].ID == id {
				return &projects[i], nil
			}
		}
		return nil, fmt.Errorf("no project #%d", id)
	}
	var found []*domain.Project
	for i := range projects {
		if projects[i].Name == ref {
			found = append(found, &projects[i])
		}
	}
	switch len(found) {
	case 0:
		return nil, fmt.Errorf("no project %q", ref)
	case 1:
		return found[0], nil
	}
	return nil, fmt.Errorf("%d projects are named %q: use the id", len(found), ref)
}

// contractRef is "<project>:<path>", the project by id or name.
func contractRef(store *persistence.Store, ref string) (changeset.Contract, *domain.Project, error) {
	name, path, ok := strings.Cut(ref, ":")
	if !ok || name == "" || path == "" {
		return changeset.Contract{}, nil, fmt.Errorf("a contract is <project>:<path>, for example backend:api/openapi.yaml; %q is not", ref)
	}
	p, err := projectRef(store, name)
	if err != nil {
		return changeset.Contract{}, nil, err
	}
	return changeset.Contract{Project: p.ID, Path: path}, p, nil
}

// withSet loads the set, runs f on it and, when f succeeds, keeps the result.
func withSet(id string, f func(store *persistence.Store, s *changeset.Set) error) error {
	wsDir := viper.GetString("STAIRCASE_DIR")
	s, err := changeset.Load(wsDir, id)
	if err != nil {
		return err
	}
	store, db, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if err := f(store, &s); err != nil {
		return err
	}
	return s.Save(wsDir)
}

func changesetAdd(w io.Writer, id string, runs []int64) error {
	return withSet(id, func(store *persistence.Store, s *changeset.Set) error {
		for _, runID := range runs {
			run, err := store.GetRun(runID)
			if err != nil || run == nil {
				return fmt.Errorf("run #%d does not exist", runID)
			}
			c, err := store.GetCase(run.CaseID)
			if err != nil || c == nil {
				return fmt.Errorf("run #%d has no case", runID)
			}
			replaced, err := s.AddRun(c.ProjectID, runID)
			if err != nil {
				return err
			}
			if replaced != 0 {
				fmt.Fprintf(w, "Run #%d of project #%d replaces run #%d in %s.\n", runID, c.ProjectID, replaced, id)
			} else {
				fmt.Fprintf(w, "Run #%d of project #%d is in %s.\n", runID, c.ProjectID, id)
			}
		}
		return nil
	})
}

func changesetContract(w io.Writer, id, ref string) error {
	return withSet(id, func(store *persistence.Store, s *changeset.Set) error {
		c, p, err := contractRef(store, ref)
		if err != nil {
			return err
		}
		if err := s.Declare(c); err != nil {
			return err
		}
		fmt.Fprintf(w, "%s of %s is a contract of %s.\n", c.Path, p.Name, id)
		pr, _, err := profile.Load(viper.GetString("STAIRCASE_DIR"), p.ID)
		if err != nil {
			return err
		}
		if !engine.MatchAny(pr.Sensitive, c.Path) {
			fmt.Fprintf(w, "Note: a change to it does not always need a person yet. Make it so with: staircase project profile set %d --sensitive %s\n", p.ID, c.Path)
		}
		return nil
	})
}

func changesetAck(w io.Writer, id, ref, by string) error {
	return withSet(id, func(store *persistence.Store, s *changeset.Set) error {
		c, p, err := contractRef(store, ref)
		if err != nil {
			return err
		}
		if !s.Declared(c) {
			return fmt.Errorf("%s of %s is not a contract of %s: staircase changeset contract %s %s", c.Path, p.Name, id, id, ref)
		}
		other, err := projectRef(store, by)
		if err != nil {
			return err
		}
		m, ok := s.Member(p.ID)
		if !ok {
			return fmt.Errorf("%s has no run in %s, so it changed nothing there", p.Name, id)
		}
		run, err := store.GetRun(m.Run)
		if err != nil || run == nil {
			return fmt.Errorf("run #%d does not exist", m.Run)
		}
		h, err := history.Read(store, run.ID)
		if err != nil {
			return fmt.Errorf("run #%d: its history cannot be read: %w", run.ID, err)
		}
		before, err := changeset.BlobAt(p.SourcePath, h.Bound.BaseSHA, c.Path)
		if err != nil {
			return err
		}
		after, err := changeset.BlobAt(p.SourcePath, run.GitCommitHash, c.Path)
		if err != nil {
			return err
		}
		if before == after {
			return fmt.Errorf("run #%d did not change %s: there is nothing to acknowledge", run.ID, c.Path)
		}
		who, _ := exec.Command("git", "-C", other.SourcePath, "config", "user.email").Output()
		if err := s.Acknowledge(c, other.ID, run.ID, after, strings.TrimSpace(string(who)), time.Now()); err != nil {
			return err
		}
		fmt.Fprintf(w, "%s accepts run #%d's change to %s.\n", other.Name, run.ID, c.Path)
		return nil
	})
}

func changesetVerify(w io.Writer, id string, minCAL int) error {
	wsDir := viper.GetString("STAIRCASE_DIR")
	s, err := changeset.Load(wsDir, id)
	if err != nil {
		return err
	}
	store, db, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	r, err := changeset.Verify(store, wsDir, s, minCAL)
	if err != nil {
		return err
	}
	for _, m := range r.Members {
		state := "ok"
		if len(m.Problems) > 0 {
			state = "FAILED"
		}
		fmt.Fprintf(w, "%s run #%d: %s", tui.Safe(m.Name), m.Run, state)
		if m.CAL > 0 {
			fmt.Fprintf(w, " (CAL %d)", m.CAL)
		}
		if len(m.Changed) > 0 {
			fmt.Fprintf(w, ", changed the contract %s", tui.Safe(strings.Join(m.Changed, ", ")))
		}
		fmt.Fprintln(w)
		for _, p := range m.Problems {
			fmt.Fprintf(w, "   problem: %s\n", tui.Safe(p))
		}
	}
	for _, p := range r.Problems {
		fmt.Fprintf(w, "problem: %s\n", tui.Safe(p))
	}
	if !r.OK() {
		return fmt.Errorf("change set %s does not hold", id)
	}
	fmt.Fprintf(w, "Change set %s holds: %d runs delivered, certificates valid, contract changes acknowledged.\n", id, len(r.Members))
	return nil
}

func changesetShow(w io.Writer, id string) error {
	s, err := changeset.Load(viper.GetString("STAIRCASE_DIR"), id)
	if err != nil {
		return err
	}
	store, db, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	name := func(id int64) string {
		if p, err := store.GetProject(id); err == nil && p != nil {
			return tui.Safe(p.Name)
		}
		return fmt.Sprintf("#%d", id)
	}
	fmt.Fprintf(w, "Change set %s, made %s\n", s.ID, s.Created.Format(time.DateOnly))
	fmt.Fprintln(w, "Runs:")
	for _, m := range s.Members {
		fmt.Fprintf(w, "  %s: run #%d\n", name(m.Project), m.Run)
	}
	if len(s.Members) == 0 {
		fmt.Fprintln(w, "  none")
	}
	fmt.Fprintln(w, "Contracts:")
	for _, c := range s.Contracts {
		fmt.Fprintf(w, "  %s:%s\n", name(c.Project), tui.Safe(c.Path))
	}
	if len(s.Contracts) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, a := range s.Acks {
		fmt.Fprintf(w, "Acknowledged: %s accepts %s:%s as run #%d made it (%.12s), by %s, %s\n",
			name(a.By), name(a.Contract.Project), tui.Safe(a.Contract.Path), a.Run, a.Blob, tui.Safe(a.Who), a.At.Format(time.DateOnly))
	}
	return nil
}
