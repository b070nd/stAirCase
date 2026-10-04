package main

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/tui"
	"github.com/spf13/cobra"
)

var (
	reportSince string
	reportKey   string
	reportJSON  bool
)

var reportCmd = &cobra.Command{
	Use:   "report [repository...]",
	Short: "Report how agent-written commits were governed, across one or more repositories",
	Long: `Looks at the commits on the current branch of each repository (default: the one
you are in) and reports: how many were written with an agent (an Assisted-by:
trailer or a change certificate), how many of those carry a valid certificate at
each change assurance level, which agents wrote them, and which ones have a
missing or invalid certificate or a failed check.

Certificates are read from git notes (refs/notes/staircase); fetch them first:
  git fetch origin refs/notes/staircase:refs/notes/staircase

The report decides each certificate as staircase verify does, but never fails:
use verify in CI to enforce.`,
	RunE: reportHandler,
}

func init() {
	reportCmd.Flags().StringVar(&reportSince, "since", "90.days", "Only commits after this (anything git log --since takes); empty for all")
	reportCmd.Flags().StringVar(&reportKey, "key", "", "Public signing key to trust (default: the workspace's .signing.pub)")
	reportCmd.Flags().BoolVar(&reportJSON, "json", false, "Print the report as JSON")
	rootCmd.AddCommand(reportCmd)
}

// repoReport is one repository's report.
type repoReport struct {
	Repository string         `json:"repository"`
	Commits    int            `json:"commits"`
	Human      int            `json:"human"`     // no agent declared and no certificate
	Certified  map[int]int    `json:"certified"` // valid certificates by CAL
	Agents     map[string]int `json:"agents"`    // commits per agent, from valid certificates
	Problems   []problem      `json:"problems"`
	Hurried    []problem      `json:"hurried"` // certified, but large changes were approved within seconds
}

type problem struct {
	Commit  string `json:"commit"`
	Subject string `json:"subject"`
	Reason  string `json:"reason"`
}

func reportHandler(_ *cobra.Command, args []string) error {
	repos := args
	if len(repos) == 0 {
		repos = []string{"."}
	}
	keys, err := trustedKeys(reportKey)
	if err != nil {
		return err
	}
	var all []repoReport
	for _, repo := range repos {
		r, err := report(repo, reportSince, keys)
		if err != nil {
			return fmt.Errorf("%s: %w", repo, err)
		}
		all = append(all, r)
	}
	if reportJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(all)
	}
	for _, r := range all {
		printReport(r)
	}
	return nil
}

// report looks at the commits of repo's current branch since since.
func report(repo, since string, keys []ed25519.PublicKey) (repoReport, error) {
	r := repoReport{Repository: repo, Certified: map[int]int{}, Agents: map[string]int{}}
	git := func(args ...string) ([]byte, error) {
		return exec.Command("git", append([]string{"-C", repo}, args...)...).Output()
	}
	notes := map[string]string{} // commit → note object
	if out, err := git("notes", "--ref=staircase", "list"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if note, commit, ok := strings.Cut(line, " "); ok {
				notes[commit] = note
			}
		}
	}
	args := []string{"log", "--format=%H%x00%(trailers:key=Assisted-by,valueonly,separator=%x2C )%x00%s%x1e"}
	if since != "" {
		args = append(args, "--since="+since)
	}
	out, err := git(args...)
	if err != nil {
		return r, fmt.Errorf("not a git repository with commits")
	}
	for _, rec := range strings.Split(string(out), "\x1e") {
		f := strings.Split(strings.TrimSpace(rec), "\x00")
		if len(f) != 3 {
			continue
		}
		commit, assisted, subject := f[0], strings.TrimSpace(f[1]), f[2]
		r.Commits++
		note, hasNote := notes[commit]
		if assisted == "" && !hasNote {
			r.Human++
			continue
		}
		fail := func(why string) {
			r.Problems = append(r.Problems, problem{Commit: commit, Subject: subject, Reason: why})
		}
		if !hasNote {
			fail("no change certificate (the commit says an agent helped: " + assisted + ")")
			continue
		}
		raw, err := git("cat-file", "-p", note)
		if err != nil {
			fail("unreadable note: " + err.Error())
			continue
		}
		var env certificate.Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			fail("the change certificate is not valid JSON")
			continue
		}
		s, err := certificate.OpenAny(env, keys)
		if err == nil {
			err = s.Accept(commit, s.Predicate.CAL, 0)
		}
		if err != nil {
			fail(err.Error())
			continue
		}
		r.Certified[s.Predicate.CAL]++
		if a := s.Predicate.Attention; a != nil && a.QuickApprovals > 0 {
			r.Hurried = append(r.Hurried, problem{Commit: commit, Subject: subject,
				Reason: fmt.Sprintf("%d large change(s) approved within seconds", a.QuickApprovals)})
		}
		for _, a := range s.Predicate.Agents {
			r.Agents[a]++
		}
	}
	return r, nil
}

func printReport(r repoReport) {
	// commit subjects, trailers and certificates come from repositories other people wrote
	r.Repository = tui.Safe(r.Repository)
	for _, list := range [][]problem{r.Problems, r.Hurried} {
		for i := range list {
			list[i].Subject, list[i].Reason = tui.Safe(list[i].Subject), tui.Safe(list[i].Reason)
		}
	}
	agents := map[string]int{}
	for a, n := range r.Agents {
		agents[tui.Safe(a)] += n
	}
	r.Agents = agents
	agentCommits := r.Commits - r.Human
	fmt.Printf("📊 %s: %d commit(s), %d by people, %d with an agent\n", r.Repository, r.Commits, r.Human, agentCommits)
	for _, cal := range slices.Sorted(maps.Keys(r.Certified)) {
		fmt.Printf("   CAL %d: %d certified\n", cal, r.Certified[cal])
	}
	for _, a := range slices.Sorted(maps.Keys(r.Agents)) {
		fmt.Printf("   %s: %d commit(s)\n", a, r.Agents[a])
	}
	if len(r.Hurried) > 0 {
		fmt.Printf("   ⚠️  %d certified commit(s) where large changes were approved within seconds:\n", len(r.Hurried))
		for _, p := range r.Hurried {
			fmt.Printf("      %.12s %s: %s\n", p.Commit, p.Subject, p.Reason)
		}
	}
	if len(r.Problems) == 0 {
		if agentCommits > 0 {
			fmt.Println("   ✅ every agent commit carries a valid certificate")
		}
		return
	}
	fmt.Printf("   ❌ %d agent commit(s) without a valid certificate:\n", len(r.Problems))
	for _, p := range r.Problems {
		fmt.Printf("      %.12s %s: %s\n", p.Commit, p.Subject, p.Reason)
	}
}
