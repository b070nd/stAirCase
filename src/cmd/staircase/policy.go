package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/b070nd/staircase-core/src/internal/crypto"
	"github.com/b070nd/staircase-core/src/internal/policy"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var policyCmd = &cobra.Command{
	Use:   "policy",
	Short: "Manage the auto-approval policy (sign, verify)",
}

// policySignCmd signs the current policy.json with the workspace Ed25519
// signing key, writing policy.json.sig. Re-run after any policy edit.
var policySignCmd = &cobra.Command{
	Use:   "sign",
	Short: "Sign policy.json with the workspace signing key",
	Long: `Sign $STAIRCASE_DIR/policy.json with the workspace Ed25519 signing key
(.signing.key) and write $STAIRCASE_DIR/policy.json.sig.

Re-run this command after every policy edit. staircase run warns when the
signature file is absent and refuses to run when the signature is invalid.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		wsDir := viper.GetString("STAIRCASE_DIR")

		policyPath := filepath.Join(wsDir, "policy.json")
		data, err := os.ReadFile(policyPath)
		if err != nil {
			return fmt.Errorf("read policy.json: %w", err)
		}

		privKey, err := crypto.LoadSigningKey(wsDir)
		if err != nil {
			return fmt.Errorf("load signing key: %w", err)
		}

		sigHex := crypto.Sign(privKey, data)
		sigPath := filepath.Join(wsDir, policy.PolicySigFile)
		if err := os.WriteFile(sigPath, []byte(sigHex), 0o644); err != nil {
			return fmt.Errorf("write policy signature: %w", err)
		}

		fmt.Printf("✅ policy.json signed → %s\n", sigPath)
		fmt.Printf("   sig: %s…\n", sigHex[:16])
		return nil
	},
}

// policyVerifyCmd verifies policy.json.sig against the current policy.json.
var policyVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Verify the policy.json signature",
	Long: `Verify $STAIRCASE_DIR/policy.json.sig against the current policy.json
content and the workspace public key (.signing.pub).

Exits 0 when the signature is valid, non-zero otherwise.  Useful in CI to
confirm that policy.json has not been modified since it was last signed.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		wsDir := viper.GetString("STAIRCASE_DIR")

		sigPresent, err := policy.VerifyPolicySignature(wsDir)
		if err != nil {
			return fmt.Errorf("policy signature invalid: %w", err)
		}
		if !sigPresent {
			return fmt.Errorf("no policy.json.sig found — run 'staircase policy sign' first")
		}

		fmt.Println("✅ policy.json signature verified.")
		return nil
	},
}

func init() {
	policyCmd.AddCommand(policySignCmd, policyVerifyCmd)
	rootCmd.AddCommand(policyCmd)
}
