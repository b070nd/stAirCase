package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/b070nd/stAirCase/src/internal/audit"
	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var auditAnchorCmd = &cobra.Command{
	Use:   "anchor <run-id>",
	Short: "Anchor a run's change certificate in a Rekor transparency log (digests only)",
	Long: `Puts the run's change certificate in a Rekor transparency log, an outside
witness that it existed at this time. Only the certificate is sent: commit
hashes, digests, counts and the level, never code, prompts or reasoning.
The log keeps its hash, the signature and your public key.

Check it later with: staircase verify <commit> --check-anchor`,
	Args: cobra.ExactArgs(1),
	RunE: auditAnchorHandler,
}

func init() {
	auditAnchorCmd.Flags().StringVar(&auditRekorURL, "rekor-url", audit.DefaultRekorURL, "Rekor server URL")
	auditCmd.AddCommand(auditAnchorCmd)
}

// certificatePath is where a run's change certificate is kept in the workspace.
func certificatePath(wsDir string, runID int64) string {
	return filepath.Join(wsDir, "audit", fmt.Sprintf("run-%d.certificate.json", runID))
}

func auditAnchorHandler(_ *cobra.Command, args []string) error {
	runID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("invalid run-id %q", args[0])
	}
	wsDir := viper.GetString("STAIRCASE_DIR")
	path := certificatePath(wsDir, runID)
	env, payload, err := readCertificate(path)
	if err != nil {
		return err
	}
	priv, err := crypto.LoadSigningKey(wsDir)
	if err != nil {
		return err
	}
	pub, err := crypto.LoadSigningPublicKey(wsDir)
	if err != nil {
		return err
	}
	if _, err := certificate.Open(env, pub); err != nil { // anchor only what this workspace signed
		return fmt.Errorf("%s: %w", path, err)
	}
	sum := sha256.Sum256(payload)
	a, err := audit.AnchorRecord(auditRekorURL, payload, hex.EncodeToString(sum[:]), priv)
	if err != nil {
		return err
	}
	if err := audit.AppendAnchor(path+".anchor", a); err != nil {
		return err
	}
	fmt.Printf("🪨 Run #%d's change certificate is in the log: uuid=%s log_index=%d\n", runID, a.UUID, a.LogIndex)
	return nil
}

// readCertificate reads a certificate file and its signed payload.
func readCertificate(path string) (certificate.Envelope, []byte, error) {
	var env certificate.Envelope
	b, err := os.ReadFile(path)
	if err != nil {
		return env, nil, fmt.Errorf("no change certificate: %w", err)
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return env, nil, fmt.Errorf("%s is not a change certificate: %w", path, err)
	}
	payload, err := base64.StdEncoding.DecodeString(env.Payload)
	return env, payload, err
}

// checkAnchor confirms the certificate's payload is in a Rekor log, using the
// anchor sidecar next to the certificate file.
func checkAnchor(sidecar string, payload []byte) error {
	anchors, err := audit.LoadAnchors(sidecar)
	if err != nil {
		return err
	}
	if len(anchors) == 0 {
		return fmt.Errorf("the certificate was never anchored (no %s) - run 'staircase audit anchor'", sidecar)
	}
	sum := sha256.Sum256(payload)
	a, err := audit.VerifyAnchor(payload, hex.EncodeToString(sum[:]), anchors)
	if err != nil {
		return err
	}
	fmt.Printf("   🪨 in the transparency log: uuid=%s log_index=%d\n", a.UUID, a.LogIndex)
	return nil
}
