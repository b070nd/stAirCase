package gate

// gates.json integrity (MCP signed tool ecosystem — P3 precursor).
//
// Plugin gate definitions in $wsDir/gates.json are loaded and executed as
// subprocesses.  Without a signature the operator cannot detect whether an
// adversary has modified the gate manifest (e.g. pointing a gate script at a
// malicious binary).  Signing the manifest provides the same tamper-detection
// guarantee as policy.json.sig and the audit checkpoint Ed25519 signatures.
//
// Workflow:
//
//	staircase gate sign   — signs gates.json → gates.json.sig
//	staircase gate verify — verifies the signature, exits non-zero on failure
//
// RunAll warns when the signature is absent and returns a gate failure when
// the signature is present but invalid.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/b070nd/staircase-core/src/internal/crypto"
	"github.com/b070nd/staircase-core/src/internal/obs"
)

// GatesSigFile is the sidecar written by 'staircase gate sign'.
const GatesSigFile = "gates.json.sig"

// VerifyGatesSignature checks the Ed25519 signature sidecar (gates.json.sig)
// against the current gates.json content using the workspace signing public key.
//
// Returns (false, nil) when either gates.json or the signature sidecar are
// absent — caller should warn but continue.  Returns (true, nil) when valid.
// Returns (true, err) when the sidecar is present but the signature is invalid.
func VerifyGatesSignature(wsDir string) (sigPresent bool, err error) {
	gatesPath := filepath.Join(wsDir, "gates.json")
	data, err := os.ReadFile(gatesPath)
	if os.IsNotExist(err) {
		return false, nil // no gates file — nothing to verify
	}
	if err != nil {
		return false, fmt.Errorf("read gates.json for verification: %w", err)
	}

	sigPath := filepath.Join(wsDir, GatesSigFile)
	sigHex, err := os.ReadFile(sigPath)
	if os.IsNotExist(err) {
		return false, nil // unsigned — caller warns
	}
	if err != nil {
		return true, fmt.Errorf("read gates signature: %w", err)
	}

	pub, err := crypto.LoadSigningPublicKey(wsDir)
	if err != nil {
		return true, fmt.Errorf("load signing public key: %w", err)
	}
	if err := crypto.Verify(pub, data, strings.TrimSpace(string(sigHex))); err != nil {
		return true, fmt.Errorf("gates.json signature invalid (file may have been tampered): %w", err)
	}
	return true, nil
}

// checkGatesSignature is called by RunAll before executing plugin gates.
// It adds a synthetic gate result to the report for the signature check.
func checkGatesSignature(wsDir string, report *Report) {
	sigPresent, err := VerifyGatesSignature(wsDir)
	if err != nil {
		report.Gates = append(report.Gates, fail(
			"gates.json-integrity", "security", SeverityBlock,
			fmt.Sprintf("gates.json tampered — re-sign with 'staircase gate sign': %v", err),
		))
		report.Summary.Fail++
		report.Overall = StatusFail // must be set here; RunAll checks this before executing plugins
		obs.Log.Error("gates.json integrity check failed", "err", err)
		return
	}
	if !sigPresent {
		// When the workspace has signing infrastructure, an unsigned gates.json
		// is treated as a tamper event: an adversary could otherwise bypass the
		// signature check entirely by deleting the sidecar. Workspaces that never
		// initialized signing keep the advisory warning (non-breaking).
		if _, keyErr := os.Stat(filepath.Join(wsDir, crypto.SigningPubFile)); keyErr == nil {
			report.Gates = append(report.Gates, fail(
				"gates.json-integrity", "security", SeverityBlock,
				"gates.json is unsigned but this workspace has a signing key — run 'staircase gate sign'",
			))
			report.Summary.Fail++
			report.Overall = StatusFail
			obs.Log.Error("gates.json unsigned in a signing-enabled workspace — plugin gates blocked")
			return
		}
		obs.Log.Warn("gates.json is unsigned — run 'staircase gate sign' to enable tamper detection")
	}
}
