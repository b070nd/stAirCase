package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

var (
	decideAccept, decideReject bool
	decideShown, decideMessage string
	decideAdopt                int
)

var decideCmd = &cobra.Command{
	Use:   "decide [run-id]",
	Short: "Review what your away runs left for you: accept, reject, defer or clarify",
	Long: `Lists the runs that ended while you were away (staircase claude --away ...) and wait for your review, as a tree: each
run's change, the checks that ran on exactly that change, and the proposals it parked because they needed you. Nothing of it
is committed until you decide:

  Y or I   accept: the change is committed on the run's branch, with its certificate, without starting the agent again (an empty change, from a run
           that parked everything, delivers nothing: adopt a parked proposal first to have one)
  N or O   reject: nothing is committed
  D        defer, and show the alternatives: what was parked and how to see the whole change; the run keeps waiting
  Z        clarify: a read-only session (Claude Code's plan mode) in a separate directory that holds only this change, its
           evidence and what was parked; nothing said there decides anything, you come back to Y, N or D
  P        prototype a parked change: see what it would do on top of the change (its diff and your checks on the result,
           in a checkout that is thrown away); one that passes can be adopted into the change, as your approval, and you
           then decide on the new review

Without a terminal, decide one run with --accept or --reject. A decision is made on the review entry you were shown; if the
run's review is no longer that one (--shown), it is refused.

Each run is shown with its status, read from its verified audit chain: its outcome (running, awaiting review, blocked or
partial, failed, delivered, or delivered with incomplete evidence) and why, how many times its agent was started, and the
time, tokens and estimated cost it used against what is left. Your other away runs that are still running, or blocked, are
listed below the tree; staircase decide <run-id> on a run that does not await review shows its status.`,
	Args: cobra.MaximumNArgs(1),
	RunE: decideHandler,
}

func init() {
	decideCmd.Flags().BoolVar(&decideAccept, "accept", false, "Accept the run's change: commit it, with its certificate")
	decideCmd.Flags().BoolVar(&decideReject, "reject", false, "Reject the run's change: nothing is committed")
	decideCmd.Flags().StringVar(&decideShown, "shown", "", "The review entry (as listed) the decision is made on; refused if the run's review is another")
	decideCmd.Flags().StringVarP(&decideMessage, "message", "m", "", "Why, for the audit chain")
	decideCmd.Flags().IntVar(&decideAdopt, "adopt", 0, "Adopt the parked proposal with this number (as listed) into the change, if your checks pass with it: a new review follows")
	rootCmd.AddCommand(decideCmd)
}

func decideHandler(cmd *cobra.Command, args []string) error {
	if decideAccept && decideReject {
		return errors.New("either --accept or --reject, not both")
	}
	if decideAdopt > 0 && (decideAccept || decideReject) {
		return errors.New("adopt, then decide on the new review: one at a time")
	}
	flagged := decideAccept || decideReject || decideAdopt > 0
	store, db, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	parent := context.Background()
	if cmd != nil && cmd.Context() != nil {
		parent = cmd.Context()
	}
	ctx, stop := signal.NotifyContext(parent, os.Interrupt)
	defer stop()
	runner := orchestrator.NewRunner(store, viper.GetString("STAIRCASE_DIR"))
	decide := func(runID int64) func(bool, string) error {
		return func(accept bool, entry string) error {
			res, err := runner.Decide(ctx, runID, orchestrator.DecideOptions{Accept: accept, Shown: entry, Feedback: decideMessage})
			if err != nil && res.Commit == "" {
				return err
			}
			if accept && res.Commit == "" {
				fmt.Printf("✅ Run #%d accepted: its change was empty, so nothing was delivered and its branch is unchanged. What it parked stays unapplied\n", runID)
			} else if accept {
				fmt.Printf("✅ Run #%d accepted: committed %.12s on staircase/run-%d\n", runID, res.Commit, runID)
			} else {
				fmt.Printf("✋ Run #%d rejected: nothing committed\n", runID)
			}
			return err
		}
	}

	var ids []int64
	if len(args) == 1 {
		id, err := parseID("run-id", args[0])
		if err != nil {
			return err
		}
		ids = []int64{id}
	} else {
		if flagged {
			return errors.New("name the run to --accept, --reject or --adopt: staircase decide <run-id>")
		}
		runs, err := store.ListRunsByStatus(persistence.RunStatusAwaitingReview)
		if err != nil {
			return err
		}
		for _, r := range runs {
			ids = append(ids, r.ID)
		}
	}
	if decideAdopt > 0 {
		shown := decideShown
		if shown == "" {
			rv, err := runner.ReviewOf(ids[0])
			if err != nil {
				return err
			}
			shown = rv.Entry
		}
		rv, err := runner.Adopt(ctx, ids[0], decideAdopt, shown)
		if err != nil {
			return err
		}
		fmt.Printf("✅ Adopted parked proposal %d into run #%d's change. The new review:\n", decideAdopt, ids[0])
		fmt.Print(inboxTree([]orchestrator.Review{rv}, nil))
		return nil
	}
	if flagged {
		return decide(ids[0])(decideAccept, decideShown)
	}

	var reviews []orchestrator.Review
	status := map[int64]orchestrator.Status{}
	for _, id := range ids {
		rv, err := runner.ReviewOf(id)
		if len(args) == 1 && errors.Is(err, orchestrator.ErrNotAwaiting) { // nothing to decide: how the run stands
			st, err := runner.StatusOf(id)
			if err != nil {
				return err
			}
			printStatus(st)
			return nil
		}
		if err != nil {
			return err
		}
		reviews = append(reviews, rv)
		if st, err := runner.StatusOf(id); err == nil {
			status[id] = st
		} else {
			fmt.Println(unreadable(id, err)) // never leave the run's status out as if it had none
		}
	}
	fmt.Print(inboxTree(reviews, status))
	if len(args) == 0 {
		if also := alsoAway(store, runner); len(also) > 0 {
			fmt.Printf("\nYour other away runs\n")
			for _, l := range also {
				fmt.Printf("   %s\n", l)
			}
			fmt.Printf("   (staircase decide <run-id> shows one's status)\n")
		}
	}
	if len(reviews) == 0 {
		return nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("no terminal to decide in: decide a run with staircase decide <run-id> --accept or --reject")
	}
	in := bufio.NewReader(os.Stdin)
	for _, rv := range reviews {
		repo, task := runPlace(store, rv.RunID)
		id := rv.RunID
		box := inbox{
			decide:  decide(id),
			clarify: func(rv orchestrator.Review) error { return clarify(rv, repo, task) },
			prototype: func(seq int) (orchestrator.PrototypeResult, error) {
				return runner.Prototype(ctx, id, seq)
			},
			adopt: func(seq int, entry string) (orchestrator.Review, error) {
				return runner.Adopt(ctx, id, seq, entry)
			},
		}
		if err := ask(in, os.Stdout, rv, box); err != nil {
			fmt.Printf("   ⚠️  run #%d: %v\n", rv.RunID, err)
		}
	}
	return nil
}

// runPlace is the repository a run worked in and its task, for the clarifying session.
func runPlace(store *persistence.Store, runID int64) (repo, task string) {
	run, err := store.GetRun(runID)
	if err != nil || run == nil {
		return "", ""
	}
	c, err := store.GetCase(run.CaseID)
	if err != nil || c == nil {
		return "", ""
	}
	if p, err := store.GetProject(c.ProjectID); err == nil && p != nil {
		repo = p.SourcePath
	}
	return repo, c.PrdJSON
}

// inboxTree is what waits for the owner, as a tree: run, its status (when status has it), change, checks, parked proposals.
func inboxTree(reviews []orchestrator.Review, status map[int64]orchestrator.Status) string {
	if len(reviews) == 0 {
		return "Nothing waits for your review.\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Awaiting your review\n")
	for i, rv := range reviews {
		branch, indent := "├─", "│  "
		if i == len(reviews)-1 {
			branch, indent = "└─", "   "
		}
		fmt.Fprintf(&b, "%s run #%d   review %.12s   from %.12s\n", branch, rv.RunID, rv.Entry, rv.Base)
		if st, ok := status[rv.RunID]; ok {
			for _, l := range statusLines(st) {
				fmt.Fprintf(&b, "%s├─ %s\n", indent, l)
			}
		}
		files := slices.Sorted(maps.Keys(rv.Files))
		if len(files) == 0 {
			fmt.Fprintf(&b, "%s├─ change   empty: nothing was approved (accepting it delivers nothing; prototype and adopt a parked proposal to have one)\n", indent)
		} else {
			fmt.Fprintf(&b, "%s├─ change   %d file(s): %s\n", indent, len(files), strings.Join(files, ", "))
		}
		var checks []string
		for _, c := range rv.Checks {
			checks = append(checks, fmt.Sprintf("%s (exit %d)", c.Command, c.ExitCode))
		}
		if rv.ChecksFailed != "" {
			fmt.Fprintf(&b, "%s├─ checks   FAILED on this unchanged tree (%.12s): %s (a parked proposal may be what fixes them: prototype it)\n", indent, rv.Tree, rv.ChecksFailed)
		} else {
			fmt.Fprintf(&b, "%s├─ checks   passed on exactly this change (tree %.12s): %s\n", indent, rv.Tree, strings.Join(checks, "; "))
		}
		if len(rv.ParkedProposals) == 0 {
			fmt.Fprintf(&b, "%s└─ parked   nothing\n", indent)
			continue
		}
		fmt.Fprintf(&b, "%s└─ parked   %d proposal(s) that needed you:\n", indent, len(rv.ParkedProposals))
		for j, p := range rv.ParkedProposals {
			leaf := "├─"
			if j == len(rv.ParkedProposals)-1 {
				leaf = "└─"
			}
			num := ""
			if p.Seq > 0 {
				num = fmt.Sprintf("#%d ", p.Seq)
			}
			fmt.Fprintf(&b, "%s   %s %s%s: %s\n", indent, leaf, num, parkedWhat(p), p.Why)
		}
	}
	fmt.Fprintf(&b, "\nKeys: Y or I accept, N or O reject, D defer and show the alternatives, Z clarify, P prototype a parked change\n")
	return b.String()
}

func parkedWhat(p orchestrator.ParkedProposal) string {
	if len(p.Files) > 0 {
		return strings.Join(p.Files, ", ")
	}
	return p.Action
}

// inbox is what the owner can do about one run from the decision inbox.
type inbox struct {
	decide    func(accept bool, entry string) error
	clarify   func(orchestrator.Review) error
	prototype func(seq int) (orchestrator.PrototypeResult, error)
	adopt     func(seq int, entry string) (orchestrator.Review, error)
}

// ask asks the owner about one run until they decide or defer; Z opens the clarifying session and P a prototype of a parked change
// (which may be adopted, giving a new review), and then it asks again. An end of input defers. A decision names the review entry
// that was shown.
func ask(in io.Reader, out io.Writer, rv orchestrator.Review, box inbox) error {
	r, ok := in.(*bufio.Reader)
	if !ok {
		r = bufio.NewReader(in)
	}
	for {
		fmt.Fprintf(out, "run #%d: Y accept, N reject, D defer, Z clarify, P prototype a parked change > ", rv.RunID)
		line, err := r.ReadString('\n')
		key := strings.ToUpper(strings.TrimSpace(line))
		if key == "" && err != nil {
			fmt.Fprintf(out, "\n   deferred: run #%d keeps waiting\n", rv.RunID)
			return nil
		}
		switch key {
		case "Y", "I":
			return box.decide(true, rv.Entry)
		case "N", "O":
			return box.decide(false, rv.Entry)
		case "P":
			if next, ok := prototypeStep(r, out, rv, box); ok {
				rv = next
				fmt.Fprint(out, inboxTree([]orchestrator.Review{rv}, nil))
			}
		case "D":
			fmt.Fprint(out, alternatives(rv))
			fmt.Fprintf(out, "   deferred: run #%d keeps waiting\n", rv.RunID)
			return nil
		case "Z":
			if err := box.clarify(rv); err != nil {
				fmt.Fprintf(out, "   the clarifying session did not start: %v\n", err)
			}
		default:
			fmt.Fprintf(out, "   answer Y (or I), N (or O), D, Z or P\n")
		}
	}
}

// prototypeStep is P (ADR 0007): the owner picks a parked proposal, sees what it would do on top of the change, and may adopt a
// passing one. It returns the new review when one was adopted.
func prototypeStep(r *bufio.Reader, out io.Writer, rv orchestrator.Review, box inbox) (orchestrator.Review, bool) {
	if len(rv.ParkedProposals) == 0 {
		fmt.Fprintf(out, "   nothing was parked\n")
		return rv, false
	}
	for i, p := range rv.ParkedProposals {
		fmt.Fprintf(out, "   %d) %s: %s\n", i+1, parkedWhat(p), p.Why)
	}
	fmt.Fprintf(out, "   which one? > ")
	line, _ := r.ReadString('\n')
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(line), "%d", &n); err != nil || n < 1 || n > len(rv.ParkedProposals) {
		fmt.Fprintf(out, "   no such parked proposal\n")
		return rv, false
	}
	p := rv.ParkedProposals[n-1]
	res, err := box.prototype(p.Seq)
	if err != nil {
		fmt.Fprintf(out, "   %v\n", err)
		return rv, false
	}
	fmt.Fprintf(out, "   prototype of %s on top of the change:\n", parkedWhat(p))
	for _, c := range res.Checks {
		fmt.Fprintf(out, "   check %s: exit %d\n", c.Command, c.ExitCode)
	}
	if res.Diff != "" {
		fmt.Fprint(out, indent(res.Diff, "   | "))
	}
	if !res.Passed {
		fmt.Fprintf(out, "   it cannot be adopted: %s\n", res.Failed)
		return rv, false
	}
	fmt.Fprintf(out, "   adopt it into the change? [y/N] > ")
	line, _ = r.ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
		fmt.Fprintf(out, "   not adopted: it stays parked\n")
		return rv, false
	}
	next, err := box.adopt(p.Seq, rv.Entry)
	if err != nil {
		fmt.Fprintf(out, "   not adopted: %v\n", err)
		return rv, false
	}
	fmt.Fprintf(out, "   adopted: the change now includes %s; this is a new review\n", parkedWhat(p))
	return next, true
}

func indent(s, prefix string) string {
	lines := strings.SplitAfter(strings.TrimRight(s, "\n")+"\n", "\n")
	var b strings.Builder
	for _, l := range lines {
		if l != "" {
			b.WriteString(prefix + l)
		}
	}
	return b.String()
}

// alternatives is what D shows: what was parked instead of decided, and how to see the whole change.
func alternatives(rv orchestrator.Review) string {
	var b strings.Builder
	if len(rv.ParkedProposals) == 0 {
		fmt.Fprintf(&b, "   nothing was parked: the change on offer is all the run did\n")
	}
	for _, p := range rv.ParkedProposals {
		fmt.Fprintf(&b, "   parked: %s - %s\n", parkedWhat(p), p.Why)
	}
	fmt.Fprintf(&b, "   the whole change: git diff %.12s %.12s\n", rv.Base, rv.Tree)
	return b.String()
}

// clarifyCommand is the read-only assistant Z starts.
var clarifyCommand = "claude"

// clarify starts a session whose whole context is this run's review: a separate directory holding REVIEW.md (the task, the
// change, the checks, what was parked) and CHANGE.diff, never the repository; the assistant runs in Claude Code's plan mode,
// which changes nothing. The directory is removed afterwards.
func clarify(rv orchestrator.Review, repo, task string) error {
	dir, err := os.MkdirTemp("", "staircase-clarify-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	var b strings.Builder
	fmt.Fprintf(&b, "# Review of run #%d\n\nTask: %s\n\nFrom commit %s to tree %s (review entry %s).\n\n%s", rv.RunID, task, rv.Base, rv.Tree, rv.Entry, inboxTree([]orchestrator.Review{rv}, nil))
	if err := os.WriteFile(filepath.Join(dir, "REVIEW.md"), []byte(b.String()), 0o600); err != nil {
		return err
	}
	diff := []byte("the change could not be read from the repository\n")
	if repo != "" {
		if out, err := exec.Command("git", "-C", repo, "diff", rv.Base, rv.Tree).Output(); err == nil {
			diff = out
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "CHANGE.diff"), diff, 0o600); err != nil {
		return err
	}
	path, err := exec.LookPath(clarifyCommand)
	if err != nil {
		return fmt.Errorf("no %s to clarify with (the material was in %s)", clarifyCommand, dir)
	}
	c := exec.Command(path, "--permission-mode", "plan",
		"The owner is deciding whether to accept an AI agent's change. Read REVIEW.md and CHANGE.diff here and answer their questions. "+
			"You cannot change anything, and nothing you say decides the review.")
	c.Dir, c.Stdin, c.Stdout, c.Stderr = dir, os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}
