package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/governance"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/sshsig"
	"github.com/b070nd/stAirCase/src/internal/vsa"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	verifyMinCAL      int
	verifyKey         string
	verifyFile        string
	verifyCheckAnchor bool
	verifyAll         bool
	verifySigners     string
	verifyRebuild     bool
	verifyLedger      string

	verifyRequireInitiator bool

	// a SLSA verification summary for every commit that passes
	verifyVSAOut, verifyResourceURI, verifyVerifierID, verifyPolicyURI, verifyVSAKey string
)

// vsaResource is the repository URI of the summaries being written.
var vsaResource string

var verifyCmd = &cobra.Command{
	Use:   "verify <commit | range>",
	Short: "Check that a commit, or every agent commit in a range, carries a valid change certificate",
	Long: `Checks the change certificate of a commit in the git repository you are in:
it must be signed by the trusted key, be about exactly this commit, and reach
the required change assurance level (--min-cal, see docs/adr/0001).

The certificate is read from the commit's git note (refs/notes/staircase),
which a run writes; fetch notes from a remote with
  git fetch origin refs/notes/staircase:refs/notes/staircase
or pass the certificate file with --certificate.

The trusted key is the workspace's public signing key (.signing.pub), or the
file given with --key: that file is all a reviewer needs.

A range (main..HEAD) checks every commit in it that names an agent in an
Assisted-by: trailer; --all checks every commit. This is what a CI check on a
pull request runs.`,
	Args: cobra.ExactArgs(1),
	RunE: verifyHandler,
}

func init() {
	verifyCmd.Flags().IntVar(&verifyMinCAL, "min-cal", 0, "Fail below this change assurance level (1-4)")
	verifyCmd.Flags().StringVar(&verifyKey, "key", "", "Public signing key to trust (default: the workspace's .signing.pub and its team's keys, see staircase governance)")
	verifyCmd.Flags().StringVar(&verifyFile, "certificate", "", "Read the certificate from this file instead of the git note")
	verifyCmd.Flags().StringVar(&verifySigners, "allowed-signers", "",
		"git allowed_signers file of trusted reviewers: a CAL 3 change they signed (staircase sign) and did not request reaches CAL 4 "+
			"(default: the team's, from staircase governance)")
	verifyCmd.Flags().BoolVar(&verifyAll, "all", false, "In a range, require a certificate on every commit, not only on those that name an agent (Assisted-by:)")
	verifyCmd.Flags().BoolVar(&verifyRequireInitiator, "require-initiator", false,
		"CAL 4 counts only when the person who started the run signed the request (--sign-approvals) and is listed in the trusted signers; "+
			"without it the requester is the git email, which anyone can set")
	verifyCmd.Flags().StringVar(&verifyVSAOut, "vsa-out", "",
		"Write a SLSA Verification Summary Attestation (VSA v1, signed DSSE) for every commit that passes, to <dir>/<commit>.vsa.json; "+
			"its levels are stAirCase's own (STAIRCASE_CAL_n, STAIRCASE_REBUILT, ...), never a SLSA source level")
	verifyCmd.Flags().StringVar(&verifyResourceURI, "resource-uri", "", "With --vsa-out, the repository URI, such as git+https://github.com/org/repo (default: from the origin remote)")
	verifyCmd.Flags().StringVar(&verifyVerifierID, "verifier-id", "", "With --vsa-out, the verifier's identity URI (default: https://github.com/b070nd/stAirCase); consumers accept a VSA only from verifiers they know")
	verifyCmd.Flags().StringVar(&verifyPolicyURI, "policy-uri", "", "With --vsa-out, the URI that says what the policy digest covers (default: the VSA specification's policy section)")
	verifyCmd.Flags().StringVar(&verifyVSAKey, "vsa-key", "", "With --vsa-out, the raw Ed25519 private key file that signs it (default: the workspace's .signing.key)")
	verifyCmd.Flags().BoolVar(&verifyRebuild, "rebuild", false,
		"Also rebuild each commit from its ledger (what 'staircase rebuild' does): the ledger the certificate names is read from the commit's git note "+
			"(refs/notes/staircase-ledger), and a commit without one, or holding other bytes than it produces, fails")
	verifyCmd.Flags().StringVar(&verifyLedger, "ledger", "", "With --rebuild, read the ledger from this file instead of the git note (one commit only)")
	verifyCmd.Flags().BoolVar(&verifyCheckAnchor, "check-anchor", false,
		"Also check that the certificate is in a Rekor log (see 'staircase audit anchor'); reads <certificate>.anchor, by default from the workspace")
	rootCmd.AddCommand(verifyCmd)
}

func verifyHandler(_ *cobra.Command, args []string) error {
	if verifyMinCAL < 0 || verifyMinCAL > 4 {
		return errors.New("--min-cal is a level from 1 to 4")
	}
	if verifyVSAOut != "" {
		var err error
		if vsaResource, err = resolveResourceURI(); err != nil {
			return err
		}
	}
	if strings.Contains(args[0], "..") {
		return verifyRange(args[0])
	}
	out, err := exec.Command("git", "rev-parse", "--verify", "-q", args[0]+"^{commit}").Output()
	if err != nil {
		return fmt.Errorf("%q is not a commit in this repository", args[0])
	}
	return verifyCommit(strings.TrimSpace(string(out)))
}

// verifyRange checks every commit of a range such as main..HEAD: a commit
// that says an agent helped (an Assisted-by: trailer) must carry a valid
// certificate; with --all every commit must. A commit made with an agent but
// not marked as such cannot be told apart from a person's.
func verifyRange(rng string) error {
	out, err := exec.Command("git", "rev-list", "--reverse", rng).Output()
	if err != nil {
		return fmt.Errorf("%q is not a commit range in this repository", rng)
	}
	commits := strings.Fields(string(out))
	var failed []string
	skipped := 0
	for _, c := range commits {
		trailer, _ := exec.Command("git", "log", "-1", "--format=%(trailers:key=Assisted-by,valueonly,separator=%x2C )", c).Output()
		assisted := strings.TrimSpace(string(trailer))
		if assisted == "" && !verifyAll {
			fmt.Printf("·  Commit %.12s: no agent declared, no certificate needed\n", c)
			skipped++
			continue
		}
		if err := verifyCommit(c); err != nil {
			if assisted != "" {
				err = fmt.Errorf("%w (the commit says an agent helped: %s)", err, assisted)
			}
			fmt.Printf("❌ %v\n", err)
			failed = append(failed, err.Error())
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d commit(s) in %s failed:\n  %s", len(failed), len(commits), rng, strings.Join(failed, "\n  "))
	}
	fmt.Printf("✅ %s: %d commit(s) checked\n", rng, len(commits)-skipped)
	if skipped > 0 {
		fmt.Printf("⚠️  %d commit(s) were not checked because they name no agent. An agent's commit that leaves out its Assisted-by trailer looks the same; use --all where every change must be certified.\n", skipped)
	}
	return nil
}

// verifyCommit checks one commit's change certificate.
func verifyCommit(commit string) error {
	var raw []byte
	var err error
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

	keys, err := trustedKeys(verifyKey)
	if err != nil {
		return err
	}
	s, err := certificate.OpenAny(env, keys)
	if err != nil {
		return fmt.Errorf("commit %.12s: %w", commit, err)
	}
	if s.Commit() != commit { // before the signers: they sign this certificate, not another commit's
		return fmt.Errorf("the certificate is about commit %.12s, not %.12s", s.Commit(), commit)
	}
	p := s.Predicate
	level, signers := p.CAL, []string(nil)
	signersFile := verifySigners
	if team := filepath.Join(viper.GetString("STAIRCASE_DIR"), governance.AllowedSigners); signersFile == "" {
		if _, err := os.Stat(team); err == nil {
			signersFile = team // the team's reviewers (staircase governance use)
		}
	}
	// The initiator's own signature: always checked when present (a false claim
	// fails the certificate), trusted only when the signers file lists their key.
	var initiator, initiatorKey string
	initiatorTrusted := false
	if in := p.Initiator; in != nil {
		sig, err := base64.StdEncoding.DecodeString(in.Signature)
		if err != nil {
			return fmt.Errorf("commit %.12s: the initiator's signature is not valid base64", commit)
		}
		text := certificate.InitiatorText(p, in.Principal)
		if initiatorKey, err = sshsig.Check(certificate.InitiatorNamespace, text, sig); err != nil {
			return fmt.Errorf("commit %.12s: the initiator's signature is not valid: %w", commit, err)
		}
		initiator = in.Principal
		initiatorTrusted = signersFile != "" && sshsig.Verify(signersFile, in.Principal, certificate.InitiatorNamespace, text, sig) == nil
	}
	if signersFile != "" && p.CAL >= 3 { // ADR 0001: CAL 4 = CAL 3 + a second, identified person
		if verifyRequireInitiator && !initiatorTrusted {
			s.Predicate.Notes = append(s.Predicate.Notes, "CAL 4 needs the run's initiator authenticated and trusted (--require-initiator)")
		} else {
			if signers, err = personSignatures(env, signersFile, p.RequestedBy, initiator, initiatorKey); err != nil {
				return err
			}
			if len(signers) > 0 {
				level = 4
			}
		}
	}
	if err := s.Accept(commit, level, verifyMinCAL); err != nil { // docs/spec/certificate-v1.md
		return err
	}
	rebuilt := ""
	var rebuiltLedger []byte
	if verifyRebuild {
		if p.Ledger == "" {
			return fmt.Errorf("commit %.12s: the certificate names no ledger (no ledger, nothing to rebuild from)", commit)
		}
		top, err := gitTopLevel()
		if err != nil {
			return err
		}
		ledger, err := ledgerFor(top, commit)
		if err != nil {
			return fmt.Errorf("commit %.12s: %w", commit, err)
		}
		if got := ledgerDigest(ledger); got != p.Ledger {
			return fmt.Errorf("commit %.12s: the ledger found is not the one the certificate names (sha256 %.12s, want %.12s)", commit, got, p.Ledger)
		}
		n, tree, err := rebuildCommit(top, commit, p, ledger)
		if err != nil {
			return err
		}
		rebuilt = fmt.Sprintf("   rebuilt from %d approved proposal(s): tree %.12s is identical\n", n, tree)
		rebuiltLedger = ledger
	}
	if verifyCheckAnchor {
		sidecar := verifyFile + ".anchor"
		if verifyFile == "" {
			sidecar = certificatePath(viper.GetString("STAIRCASE_DIR"), p.Run) + ".anchor"
		}
		payload, err := base64.StdEncoding.DecodeString(env.Payload)
		if err != nil {
			return err
		}
		if err := checkAnchor(sidecar, payload, keys); err != nil {
			return fmt.Errorf("commit %.12s: %w", commit, err)
		}
	}
	fmt.Printf("✅ Commit %.12s: valid change certificate, CAL %d\n", commit, level)
	fmt.Print(rebuilt)
	if verifyVSAOut != "" {
		if err := writeVSA(commit, raw, rebuiltLedger, p, level, initiatorTrusted, keys, signersFile); err != nil {
			return fmt.Errorf("commit %.12s: verification summary: %w", commit, err)
		}
	}
	switch {
	case initiator != "" && initiatorTrusted:
		fmt.Printf("   started by %s (signed, key %s, trusted)\n", initiator, initiatorKey)
	case initiator != "":
		fmt.Printf("   started by %s (signed, key %s, not in the trusted signers)\n", initiator, initiatorKey)
	case p.RequestedBy != "":
		fmt.Printf("   requested by %s (the git email of the checkout: unauthenticated)\n", p.RequestedBy)
	}
	if len(signers) > 0 {
		fmt.Printf("   reviewed and signed by %s\n", strings.Join(signers, ", "))
	}
	fmt.Printf("   run #%d from %.12s, assisted by %s\n", p.Run, p.BaseCommit, strings.Join(p.Agents, ", "))
	for source, n := range p.Decisions {
		fmt.Printf("   %d decision(s) by %s\n", n, source)
	}
	for _, c := range p.Checks {
		where := "in the sandbox"
		if !c.Sandboxed {
			where = "without a sandbox"
		}
		fmt.Printf("   check passed %s: %s\n", where, c.Command)
	}
	if sg := p.Signed; sg != nil {
		fmt.Printf("   %d decision(s) signed with SSH keys", sg.Decisions)
		if len(sg.Signers) > 0 {
			fmt.Printf(", trusted signers: %s", strings.Join(sg.Signers, ", "))
		}
		fmt.Println()
	}
	if a := p.Attention; a != nil {
		fmt.Printf("   %d decision(s) by people, median %.0f s", a.HumanDecisions, a.MedianSeconds)
		if a.QuickApprovals > 0 {
			fmt.Printf("; ⚠️  %d large change(s) approved within seconds", a.QuickApprovals)
		}
		fmt.Println()
	}
	for _, n := range p.Notes {
		fmt.Printf("   note: %s\n", n)
	}
	return nil
}

// personSignatures are the trusted people who signed env (staircase sign):
// each signature must verify with ssh-keygen against the allowed_signers file,
// and the requester's own signature does not count.
func personSignatures(env certificate.Envelope, allowedSigners, requester, initiator, initiatorKey string) ([]string, error) {
	payload, err := base64.StdEncoding.DecodeString(env.Payload)
	if err != nil {
		return nil, err
	}
	pae := certificate.PAE(env.PayloadType, payload)
	var who []string
	for _, s := range env.Signatures {
		principal, ok := strings.CutPrefix(s.KeyID, certificate.SSHSignature)
		if !ok || principal == requester || (initiator != "" && principal == initiator) {
			continue
		}
		sig, err := base64.StdEncoding.DecodeString(s.Sig)
		if err != nil {
			continue
		}
		if fp, err := sshsig.Check(certificate.SSHNamespace, pae, sig); initiatorKey != "" && err == nil && fp == initiatorKey {
			continue // the same key as the initiator's, under another name
		}
		if sshsig.Verify(allowedSigners, principal, certificate.SSHNamespace, pae, sig) == nil {
			who = append(who, principal)
		}
	}
	return who, nil
}

// trustedKeys are the keys certificates must be signed by: the file given
// with --key, else the workspace's own key and its team's (governance).
func trustedKeys(file string) ([]ed25519.PublicKey, error) {
	if file == "" {
		return governance.TrustedKeys(viper.GetString("STAIRCASE_DIR"))
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%s is not an Ed25519 public key (%d bytes)", file, len(b))
	}
	return []ed25519.PublicKey{b}, nil
}

// ledgerFor returns the ledger verify --rebuild checks commit against: the file
// given with --ledger, else the commit's git note.
func ledgerFor(top, commit string) ([]byte, error) {
	if verifyLedger != "" {
		return os.ReadFile(verifyLedger)
	}
	out, err := exec.Command("git", "-C", top, "notes", "--ref="+orchestrator.LedgerNotesRef, "show", commit).Output()
	if err != nil {
		return nil, errors.New("no ledger (no git note in refs/notes/staircase-ledger; fetch it like the certificates, or pass --ledger)")
	}
	return bytes.TrimSuffix(out, []byte("\n")), nil // git adds a line break to a note
}

// resolveResourceURI is the repository URI of a VSA: --resource-uri, else the
// origin remote.
func resolveResourceURI() (string, error) {
	if verifyResourceURI != "" {
		return verifyResourceURI, nil
	}
	out, _ := exec.Command("git", "remote", "get-url", "origin").Output()
	return vsa.ResourceURI(string(out))
}

// writeVSA signs and writes the verification summary of a commit that passed.
func writeVSA(commit string, certRaw, ledger []byte, p certificate.Predicate, level int, initiatorTrusted bool, keys []ed25519.PublicKey, signersFile string) error {
	tree, err := exec.Command("git", "rev-parse", commit+"^{tree}").Output()
	if err != nil {
		return err
	}
	var priv ed25519.PrivateKey
	if verifyVSAKey != "" {
		raw, err := os.ReadFile(verifyVSAKey)
		if err != nil {
			return err
		}
		if len(raw) != ed25519.PrivateKeySize {
			return fmt.Errorf("%s is not a raw Ed25519 private key (%d bytes)", verifyVSAKey, len(raw))
		}
		priv = ed25519.PrivateKey(raw)
	} else if priv, err = crypto.LoadSigningKey(viper.GetString("STAIRCASE_DIR")); err != nil {
		return fmt.Errorf("no key to sign with (pass --vsa-key): %w", err)
	}
	params := vsa.Parameters{MinCAL: verifyMinCAL, All: verifyAll, Rebuild: verifyRebuild, RequireInitiator: verifyRequireInitiator, Keys: []string{}}
	for _, k := range keys {
		params.Keys = append(params.Keys, certificate.KeyID(k))
	}
	slices.Sort(params.Keys)
	if signersFile != "" {
		b, err := os.ReadFile(signersFile)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		params.Signers = hex.EncodeToString(sum[:])
	}
	id := orDefaultStr(verifyVerifierID, "https://github.com/b070nd/stAirCase")
	v := buildVersion(version, "")
	if v != "dev" {
		v = "v" + v
	}
	st := vsa.Build(vsa.Input{
		Commit: commit, Tree: strings.TrimSpace(string(tree)), ResourceURI: vsaResource, VerifierID: id, VerifierVersion: v,
		Time: time.Now(), PolicyURI: verifyPolicyURI, Params: params, CAL: level, Rebuilt: ledger != nil,
		ChecksPassed: len(p.Checks) > 0, InitiatorAuthenticated: initiatorTrusted, Certificate: certRaw, Ledger: ledger,
	})
	env, err := vsa.Sign(st, priv)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(verifyVSAOut, 0o755); err != nil {
		return err
	}
	path := filepath.Join(verifyVSAOut, commit+".vsa.json")
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("   📄 Verification summary (SLSA VSA): %s\n", path)
	return nil
}
