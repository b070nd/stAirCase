package main

// audit — tamper-proof checkpoint export and verification.
//
// Commands:
//
//	staircase audit export <run-id>
//	    Reads all event-log entries for the run, computes a canonical
//	    JSON payload, signs it with the workspace Ed25519 key, and writes
//	    $STAIRCASE_DIR/audit/run-{id}.checkpoint.json.
//
//	staircase audit verify <checkpoint-file>
//	    Reads a checkpoint file, re-derives the hash chain from the stored
//	    entries, and verifies the Ed25519 signature against the public key in
//	    $STAIRCASE_DIR/.signing.pub.  Exits non-zero on any discrepancy.
//
// Checkpoint format (run-{id}.checkpoint.json):
//
//	{
//	  "run_id":    42,
//	  "exported":  "2026-05-05T12:00:00Z",
//	  "entries":   [ …RunEventLog objects… ],
//	  "signature": "<128-char hex Ed25519 signature over canonical entries JSON>"
//	}
//
// The signature covers the UTF-8 encoding of the JSON array in "entries" with
// no trailing newline.  The array uses compact JSON (no pretty-printing) to
// make cross-platform re-verification deterministic.

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/b070nd/staircase-core/src/internal/crypto"
	"github.com/b070nd/staircase-core/src/internal/domain"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// ─── command wiring ───────────────────────────────────────────────────────────

var auditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Audit checkpoint management (export and verify tamper-proof logs)",
}

var auditExportCmd = &cobra.Command{
	Use:   "export <run-id>",
	Short: "Export a signed audit checkpoint for a completed run",
	Args:  cobra.ExactArgs(1),
	RunE:  auditExportHandler,
}

var auditVerifyCmd = &cobra.Command{
	Use:   "verify <checkpoint-file>",
	Short: "Verify the integrity and signature of an audit checkpoint file",
	Args:  cobra.ExactArgs(1),
	RunE:  auditVerifyHandler,
}

func init() {
	auditCmd.AddCommand(auditExportCmd)
	auditCmd.AddCommand(auditVerifyCmd)
	rootCmd.AddCommand(auditCmd)
}

// ─── checkpoint type ──────────────────────────────────────────────────────────

// AuditCheckpoint is the on-disk representation of a signed audit checkpoint.
type AuditCheckpoint struct {
	RunID     int64                `json:"run_id"`
	Exported  time.Time            `json:"exported"`
	Entries   []domain.RunEventLog `json:"entries"`
	Signature string               `json:"signature"`
}

// ─── export ───────────────────────────────────────────────────────────────────

func auditExportHandler(_ *cobra.Command, args []string) error {
	runID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("invalid run-id %q: %w", args[0], err)
	}

	wsDir := viper.GetString("STAIRCASE_DIR")

	// ── 1. Open DB ────────────────────────────────────────────────────────────
	db, err := persistence.InitDB(wsDir)
	if err != nil {
		return fmt.Errorf("db init: %w", err)
	}
	defer func() { _ = db.Close() }()
	store := persistence.NewStore(db)

	// ── 2. Verify run exists ──────────────────────────────────────────────────
	run, err := store.GetRun(runID)
	if err != nil {
		return fmt.Errorf("load run: %w", err)
	}
	if run == nil {
		return fmt.Errorf("run %d not found", runID)
	}

	// ── 3. Load event logs ────────────────────────────────────────────────────
	entries, err := store.ListEventLogs(runID)
	if err != nil {
		return fmt.Errorf("list event logs: %w", err)
	}

	// ── 4. Sign ───────────────────────────────────────────────────────────────
	privKey, err := crypto.LoadSigningKey(wsDir)
	if err != nil {
		return fmt.Errorf("load signing key: %w", err)
	}

	canonical, err := marshalEntries(entries)
	if err != nil {
		return fmt.Errorf("marshal entries: %w", err)
	}
	sig := crypto.Sign(privKey, canonical)

	// ── 5. Write checkpoint ───────────────────────────────────────────────────
	auditDir := filepath.Join(wsDir, "audit")
	if err := os.MkdirAll(auditDir, 0700); err != nil {
		return fmt.Errorf("mkdir audit: %w", err)
	}

	cp := AuditCheckpoint{
		RunID:     runID,
		Exported:  time.Now().UTC(),
		Entries:   entries,
		Signature: sig,
	}
	out, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal checkpoint: %w", err)
	}

	cpPath := filepath.Join(auditDir, fmt.Sprintf("run-%d.checkpoint.json", runID))
	if err := os.WriteFile(cpPath, out, 0600); err != nil {
		return fmt.Errorf("write checkpoint: %w", err)
	}

	fmt.Printf("✅ Audit checkpoint written: %s\n", cpPath)
	fmt.Printf("   run_id:  %d\n", runID)
	fmt.Printf("   entries: %d\n", len(entries))
	fmt.Printf("   sig:     %s…\n", sig[:16])
	return nil
}

// ─── verify ───────────────────────────────────────────────────────────────────

func auditVerifyHandler(_ *cobra.Command, args []string) error {
	cpPath := args[0]

	wsDir := viper.GetString("STAIRCASE_DIR")

	// ── 1. Read checkpoint ────────────────────────────────────────────────────
	raw, err := os.ReadFile(cpPath)
	if err != nil {
		return fmt.Errorf("read checkpoint: %w", err)
	}
	var cp AuditCheckpoint
	if err := json.Unmarshal(raw, &cp); err != nil {
		return fmt.Errorf("parse checkpoint: %w", err)
	}

	// ── 2. Verify hash chain ──────────────────────────────────────────────────
	chainErr := verifyHashChain(cp.Entries)
	if chainErr != nil {
		log.Printf("❌ Hash chain invalid: %v", chainErr)
	}

	// ── 3. Verify Ed25519 signature ───────────────────────────────────────────
	pubKey, err := crypto.LoadSigningPublicKey(wsDir)
	if err != nil {
		return fmt.Errorf("load public key: %w", err)
	}

	canonical, err := marshalEntries(cp.Entries)
	if err != nil {
		return fmt.Errorf("marshal entries for verification: %w", err)
	}

	sigErr := crypto.Verify(pubKey, canonical, cp.Signature)
	if sigErr != nil {
		log.Printf("❌ Signature invalid: %v", sigErr)
	}

	// ── 4. Report ─────────────────────────────────────────────────────────────
	if chainErr != nil || sigErr != nil {
		fmt.Printf("❌ Checkpoint %s FAILED verification\n", cpPath)
		if chainErr != nil {
			fmt.Printf("   hash chain: %v\n", chainErr)
		}
		if sigErr != nil {
			fmt.Printf("   signature:  %v\n", sigErr)
		}
		return fmt.Errorf("checkpoint verification failed")
	}

	fmt.Printf("✅ Checkpoint %s OK\n", cpPath)
	fmt.Printf("   run_id:  %d\n", cp.RunID)
	fmt.Printf("   entries: %d\n", len(cp.Entries))
	fmt.Printf("   exported: %s\n", cp.Exported.Format(time.RFC3339))
	return nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// marshalEntries serialises the entries slice as compact JSON (no trailing
// newline) for use as the signed payload.  Compact JSON is used so that the
// byte sequence is identical regardless of platform or JSON library.
func marshalEntries(entries []domain.RunEventLog) ([]byte, error) {
	return json.Marshal(entries)
}

// verifyHashChain re-derives each entry's event_hash using
// persistence.ComputeEventHash — the single source of truth for the algorithm
// — and confirms it matches the stored value.
func verifyHashChain(entries []domain.RunEventLog) error {
	prevHash := ""
	for i, e := range entries {
		got := persistence.ComputeEventHash(e.Payload, prevHash, e.GitCommitHash)
		if got != e.EventHash {
			return fmt.Errorf("entry #%d (id=%d): hash mismatch: stored=%s computed=%s",
				i, e.ID, e.EventHash, got)
		}
		prevHash = e.EventHash
	}
	return nil
}
