package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var rebuildKey, rebuildLedger, rebuildCertificate string

var rebuildCmd = &cobra.Command{
	Use:   "rebuild <commit>",
	Short: "Check that a certified commit is exactly what its approved proposals produce",
	Long: `Rebuilds the commit's files from the run's ledger and compares them with the
commit. The certificate (signed by a trusted key, about exactly this commit) names
the ledger's SHA-256; the ledger lists the base commit and, in order, every
approved proposal. stAirCase replays them on the base commit, with the same rules
a run uses, and the git tree that results must be identical to the commit's tree.

This is stronger than staircase verify: verify shows who signed what was decided;
rebuild shows that what was decided is what is in the commit, byte for byte.

The ledger holds the approved content, so it stays with the author
(<workspace>/audit/run-N.ledger.json); a reviewer needs it, the certificate and
the repository. Pass another location with --ledger.`,
	Args: cobra.ExactArgs(1),
	RunE: rebuildHandler,
}

func init() {
	rebuildCmd.Flags().StringVar(&rebuildKey, "key", "", "Public signing key to trust (default: the workspace's and its team's)")
	rebuildCmd.Flags().StringVar(&rebuildLedger, "ledger", "", "The run's ledger file (default: <workspace>/audit/run-N.ledger.json)")
	rebuildCmd.Flags().StringVar(&rebuildCertificate, "certificate", "", "Read the certificate from this file instead of the git note")
	rootCmd.AddCommand(rebuildCmd)
}

func rebuildHandler(_ *cobra.Command, args []string) error {
	out, err := exec.Command("git", "rev-parse", "--verify", "-q", args[0]+"^{commit}").Output()
	if err != nil {
		return fmt.Errorf("%q is not a commit in this repository", args[0])
	}
	commit := strings.TrimSpace(string(out))
	top, err := gitTopLevel()
	if err != nil {
		return err
	}
	var raw []byte
	if rebuildCertificate != "" {
		raw, err = os.ReadFile(rebuildCertificate)
	} else if raw, err = exec.Command("git", "-C", top, "notes", "--ref=staircase", "show", commit).Output(); err != nil {
		return fmt.Errorf("commit %.12s has no change certificate (no git note in refs/notes/staircase)", commit)
	}
	if err != nil {
		return err
	}
	var env certificate.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("the change certificate is not valid JSON: %w", err)
	}
	keys, err := trustedKeys(rebuildKey)
	if err != nil {
		return err
	}
	s, err := certificate.OpenAny(env, keys)
	if err != nil {
		return fmt.Errorf("commit %.12s: %w", commit, err)
	}
	if s.Commit() != commit {
		return fmt.Errorf("the certificate is about commit %.12s, not %.12s", s.Commit(), commit)
	}
	p := s.Predicate
	if p.Ledger == "" {
		return errors.New("this certificate names no ledger (the run predates ledgers, or it was not kept): nothing to rebuild from")
	}

	ledgerFile := rebuildLedger
	if ledgerFile == "" {
		ledgerFile = orchestrator.LedgerPath(viper.GetString("STAIRCASE_DIR"), p.Run)
	}
	ledger, err := os.ReadFile(ledgerFile)
	if err != nil {
		return fmt.Errorf("the ledger is needed to rebuild (ask the author for run-%d.ledger.json, then --ledger): %w", p.Run, err)
	}
	if got := ledgerDigest(ledger); got != p.Ledger {
		return fmt.Errorf("%s is not the ledger the certificate names (sha256 %.12s, want %.12s)", ledgerFile, got, p.Ledger)
	}
	proposals, tree, err := rebuildCommit(top, commit, p, ledger)
	if err != nil {
		return err
	}
	fmt.Printf("✅ Commit %.12s rebuilt: %d approved proposal(s) on %.12s give tree %.12s, identical to the commit's\n", commit, proposals, p.BaseCommit, tree)
	fmt.Printf("   certificate signed, ledger %.12s named in it, run #%d\n", p.Ledger, p.Run)
	return nil
}

// rebuildCommit replays ledger (whose digest the caller has checked against the
// certificate) and checks that it starts where the commit does and produces the
// commit's tree. It returns the number of proposals and the tree.
func rebuildCommit(top, commit string, p certificate.Predicate, ledger []byte) (int, string, error) {
	git := func(a ...string) (string, error) {
		o, err := exec.Command("git", append([]string{"-C", top}, a...)...).Output()
		return strings.TrimSpace(string(o)), err
	}
	tree, l, err := orchestrator.RebuildTree(top, ledger)
	if err != nil {
		return 0, "", err
	}
	parent, _ := git("rev-parse", commit+"^")
	if l.Base != parent || l.Base != p.BaseCommit {
		return 0, "", fmt.Errorf("the ledger starts from %.12s, but the commit's parent is %.12s and the certificate's base is %.12s", l.Base, parent, p.BaseCommit)
	}
	want, err := git("rev-parse", commit+"^{tree}")
	if err != nil {
		return 0, "", err
	}
	if tree != want {
		return 0, "", fmt.Errorf("commit %.12s does not have the tree the ledger produces (rebuilt %.12s, commit has %.12s): it holds bytes nobody approved", commit, tree, want)
	}
	return len(l.Proposals), tree, nil
}

// ledgerDigest is the hex SHA-256 the certificate records for a ledger.
func ledgerDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
