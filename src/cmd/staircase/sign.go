package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	signKey       string
	signPrincipal string
)

var signCmd = &cobra.Command{
	Use:   "sign <commit>",
	Short: "Sign a commit's change certificate as the person who reviewed it (two-party review)",
	Long: `After reviewing a run's change, a second person signs its change certificate
with their SSH key (ssh-keygen -Y sign, as git does for SSH-signed commits).
The signature is added to the certificate in the commit's git note.

staircase verify --allowed-signers <file> then counts it: a CAL 3 change signed
by a trusted person who did not request the run reaches CAL 4. The file has
git's allowed_signers format: "<email> <public key>" per line.`,
	Args: cobra.ExactArgs(1),
	RunE: signHandler,
}

func init() {
	signCmd.Flags().StringVar(&signKey, "key", "", "SSH public key to sign with, its private key in ssh-agent or next to it (default: git user.signingkey, else ~/.ssh/id_ed25519.pub)")
	signCmd.Flags().StringVar(&signPrincipal, "as", "", "Who is signing, as in allowed_signers (default: git user.email)")
	rootCmd.AddCommand(signCmd)
}

func signHandler(_ *cobra.Command, args []string) error {
	out, err := exec.Command("git", "rev-parse", "--verify", "-q", args[0]+"^{commit}").Output()
	if err != nil {
		return fmt.Errorf("%q is not a commit in this repository", args[0])
	}
	commit := strings.TrimSpace(string(out))
	raw, err := exec.Command("git", "notes", "--ref=staircase", "show", commit).Output()
	if err != nil {
		return fmt.Errorf("commit %.12s has no change certificate to sign", commit)
	}
	var env certificate.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("the change certificate is not valid JSON: %w", err)
	}
	payload, err := base64.StdEncoding.DecodeString(env.Payload)
	if err != nil {
		return err
	}
	key, who := signKey, signPrincipal
	if key == "" {
		key = gitConfig("user.signingkey")
		if key == "" || !strings.HasSuffix(key, ".pub") {
			home, _ := os.UserHomeDir()
			key = filepath.Join(home, ".ssh", "id_ed25519.pub")
		}
	}
	if who == "" {
		who = gitConfig("user.email")
	}
	if who == "" {
		return fmt.Errorf("who is signing? set --as or git user.email")
	}

	cmd := exec.Command("ssh-keygen", "-Y", "sign", "-q", "-f", key, "-n", certificate.SSHNamespace)
	cmd.Stdin = bytes.NewReader(certificate.PAE(env.PayloadType, payload))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	sig, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("ssh-keygen -Y sign with %s: %w: %s", key, err, bytes.TrimSpace(stderr.Bytes()))
	}
	id := certificate.SSHSignature + who
	kept := env.Signatures[:0]
	for _, s := range env.Signatures {
		if s.KeyID != id { // signing again replaces the same person's signature
			kept = append(kept, s)
		}
	}
	env.Signatures = append(kept, certificate.Signature{KeyID: id, Sig: base64.StdEncoding.EncodeToString(sig)})
	b, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "staircase-certificate-*.json")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	_ = f.Close()
	if out, err := exec.Command("git", "notes", "--ref=staircase", "add", "-f", "-F", f.Name(), commit).CombinedOutput(); err != nil {
		return fmt.Errorf("write the signed certificate: %w: %s", err, bytes.TrimSpace(out))
	}
	var st certificate.Statement
	if json.Unmarshal(payload, &st) == nil { // keep the workspace copy in step, when it is here
		path := certificatePath(viper.GetString("STAIRCASE_DIR"), st.Predicate.Run)
		if _, err := os.Stat(path); err == nil {
			_ = os.WriteFile(path, append(b, '\n'), 0o600)
		}
	}
	fmt.Printf("✍️  Commit %.12s: change certificate signed by %s\n", commit, who)
	fmt.Println("   Push it with: git push origin refs/notes/staircase")
	return nil
}

// gitConfig is a git setting as the current repository sees it.
func gitConfig(key string) string {
	out, err := exec.Command("git", "config", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
