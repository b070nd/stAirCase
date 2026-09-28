package main

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	verifyMinCAL int
	verifyKey    string
	verifyFile   string
)

var verifyCmd = &cobra.Command{
	Use:   "verify <commit>",
	Short: "Check that a commit carries a valid change certificate",
	Long: `Checks the change certificate of a commit in the git repository you are in:
it must be signed by the trusted key, be about exactly this commit, and reach
the required change assurance level (--min-cal, see docs/adr/0001).

The certificate is read from the commit's git note (refs/notes/staircase),
which a run writes; fetch notes from a remote with
  git fetch origin refs/notes/staircase:refs/notes/staircase
or pass the certificate file with --certificate.

The trusted key is the workspace's public signing key (.signing.pub), or the
file given with --key: that file is all a reviewer needs.`,
	Args: cobra.ExactArgs(1),
	RunE: verifyHandler,
}

func init() {
	verifyCmd.Flags().IntVar(&verifyMinCAL, "min-cal", 0, "Fail below this change assurance level (1-4)")
	verifyCmd.Flags().StringVar(&verifyKey, "key", "", "Public signing key to trust (default: the workspace's .signing.pub)")
	verifyCmd.Flags().StringVar(&verifyFile, "certificate", "", "Read the certificate from this file instead of the git note")
	rootCmd.AddCommand(verifyCmd)
}

func verifyHandler(_ *cobra.Command, args []string) error {
	if verifyMinCAL < 0 || verifyMinCAL > 4 {
		return errors.New("--min-cal is a level from 1 to 4")
	}
	out, err := exec.Command("git", "rev-parse", "--verify", "-q", args[0]+"^{commit}").Output()
	if err != nil {
		return fmt.Errorf("%q is not a commit in this repository", args[0])
	}
	commit := strings.TrimSpace(string(out))

	var raw []byte
	if verifyFile != "" {
		raw, err = os.ReadFile(verifyFile)
	} else {
		raw, err = exec.Command("git", "notes", "--ref=staircase", "show", commit).Output()
		if err != nil {
			return fmt.Errorf("commit %.12s has no change certificate (no git note in refs/notes/staircase)", commit)
		}
	}
	if err != nil {
		return err
	}
	var env certificate.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("the change certificate is not valid JSON: %w", err)
	}

	var pub ed25519.PublicKey
	if verifyKey != "" {
		b, err := os.ReadFile(verifyKey)
		if err != nil {
			return err
		}
		if len(b) != ed25519.PublicKeySize {
			return fmt.Errorf("%s is not an Ed25519 public key (%d bytes)", verifyKey, len(b))
		}
		pub = b
	} else if pub, err = crypto.LoadSigningPublicKey(viper.GetString("STAIRCASE_DIR")); err != nil {
		return err
	}

	s, err := certificate.Open(env, pub)
	if err != nil {
		return fmt.Errorf("commit %.12s: %w", commit, err)
	}
	if s.Commit() != commit {
		return fmt.Errorf("the certificate is about commit %.12s, not %.12s", s.Commit(), commit)
	}
	p := s.Predicate
	if p.CAL < verifyMinCAL {
		return fmt.Errorf("commit %.12s reached CAL %d, below the required %d: %s", commit, p.CAL, verifyMinCAL,
			strings.Join(p.Notes, "; "))
	}
	fmt.Printf("✅ Commit %.12s: valid change certificate, CAL %d\n", commit, p.CAL)
	fmt.Printf("   run #%d from %.12s, assisted by %s\n", p.Run, p.BaseCommit, strings.Join(p.Agents, ", "))
	for source, n := range p.Decisions {
		fmt.Printf("   %d decision(s) by %s\n", n, source)
	}
	for _, n := range p.Notes {
		fmt.Printf("   note: %s\n", n)
	}
	return nil
}
