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
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/b070nd/staircase-core/src/internal/audit"
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

var (
	auditAnchor      bool
	auditCheckAnchor bool
	auditRekorURL    string
)

func init() {
	auditExportCmd.Flags().BoolVar(&auditAnchor, "anchor", false,
		"Also anchor the signed checkpoint in a Rekor transparency log (external witness)")
	auditExportCmd.Flags().StringVar(&auditRekorURL, "rekor-url", audit.DefaultRekorURL,
		"Rekor server URL used by --anchor / --check-anchor")
	auditVerifyCmd.Flags().BoolVar(&auditCheckAnchor, "check-anchor", false,
		"Also verify each record against its Rekor anchor sidecar (<file>.anchor)")
	auditVerifyCmd.Flags().StringVar(&auditRekorURL, "rekor-url", audit.DefaultRekorURL,
		"Rekor server URL used by --anchor / --check-anchor")
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
	if err := os.MkdirAll(auditDir, 0o700); err != nil {
		return fmt.Errorf("mkdir audit: %w", err)
	}

	cp := AuditCheckpoint{
		RunID:     runID,
		Exported:  time.Now().UTC(),
		Entries:   entries,
		Signature: sig,
	}
	// Compact JSON is required for NDJSON: one object per line so verify can
	// use a line scanner even when multiple exports are appended to the same file.
	out, err := json.Marshal(cp)
	if err != nil {
		return fmt.Errorf("marshal checkpoint: %w", err)
	}

	cpPath := filepath.Join(auditDir, fmt.Sprintf("run-%d.checkpoint.json", runID))
	if err := audit.AppendCheckpoint(cpPath, out); err != nil {
		return fmt.Errorf("write checkpoint: %w", err)
	}

	fmt.Printf("✅ Audit checkpoint written: %s\n", cpPath)
	fmt.Printf("   run_id:  %d\n", runID)
	fmt.Printf("   entries: %d\n", len(entries))
	fmt.Printf("   sig:     %s…\n", sig[:16])

	// External witness: anchor this record in a Rekor transparency log.
	if auditAnchor {
		recordHash := sha256.Sum256(out)
		anchor, err := audit.AnchorRecord(auditRekorURL, out, hex.EncodeToString(recordHash[:]), privKey)
		if err != nil {
			return fmt.Errorf("anchor checkpoint: %w", err)
		}
		if err := audit.AppendAnchor(cpPath+".anchor", anchor); err != nil {
			return fmt.Errorf("write anchor sidecar: %w", err)
		}
		fmt.Printf("🪨 Anchored in Rekor: %s\n", anchor.RekorURL)
		fmt.Printf("   uuid:      %s\n", anchor.UUID)
		fmt.Printf("   log_index: %d\n", anchor.LogIndex)
	}
	return nil
}

// ─── verify ───────────────────────────────────────────────────────────────────

// auditVerifyHandler reads a checkpoint file that may contain one or more
// NDJSON records (one JSON object per line) and verifies each independently.
// AppendCheckpoint writes one object per export call; older files may have a
// single object without a trailing newline — both formats are handled.
func auditVerifyHandler(_ *cobra.Command, args []string) error {
	cpPath := args[0]
	wsDir := viper.GetString("STAIRCASE_DIR")

	pubKey, err := crypto.LoadSigningPublicKey(wsDir)
	if err != nil {
		return fmt.Errorf("load public key: %w", err)
	}

	f, err := os.Open(cpPath)
	if err != nil {
		return fmt.Errorf("open checkpoint: %w", err)
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20) // 1 MiB max line

	lineNum := 0
	anyFailed := false
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		lineNum++

		var cp AuditCheckpoint
		if err := json.Unmarshal(line, &cp); err != nil {
			return fmt.Errorf("parse checkpoint line %d: %w", lineNum, err)
		}

		if err := verifyOne(cpPath, lineNum, cp, pubKey); err != nil {
			fmt.Printf("❌ %v\n", err)
			anyFailed = true
		} else {
			fmt.Printf("✅ Checkpoint %s (record %d) OK — run_id=%d entries=%d exported=%s\n",
				cpPath, lineNum, cp.RunID, len(cp.Entries), cp.Exported.Format(time.RFC3339))
		}

		// Optional external-witness check against the Rekor anchor sidecar.
		// Scope: confirms the entry exists in the log and the logged artifact
		// matches this record byte-for-byte (no Merkle inclusion proof yet).
		if auditCheckAnchor {
			anchors, aErr := audit.LoadAnchors(cpPath + ".anchor")
			if aErr != nil {
				fmt.Printf("❌ record %d: load anchors: %v\n", lineNum, aErr)
				anyFailed = true
			} else {
				recordHash := sha256.Sum256(line)
				if anchor, vErr := audit.VerifyAnchor(line, hex.EncodeToString(recordHash[:]), anchors); vErr != nil {
					fmt.Printf("❌ record %d: %v\n", lineNum, vErr)
					anyFailed = true
				} else {
					fmt.Printf("🪨 record %d anchored OK — uuid=%s log_index=%d\n", lineNum, anchor.UUID, anchor.LogIndex)
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read checkpoint: %w", err)
	}
	if lineNum == 0 {
		return fmt.Errorf("checkpoint file is empty: %s", cpPath)
	}
	if anyFailed {
		return fmt.Errorf("checkpoint verification failed")
	}
	return nil
}

// verifyOne verifies a single AuditCheckpoint record's hash chain and signature.
func verifyOne(cpPath string, lineNum int, cp AuditCheckpoint, pubKey []byte) error {
	// ── 1. Hash chain ─────────────────────────────────────────────────────────
	if chainErr := verifyHashChain(cp.Entries); chainErr != nil {
		log.Printf("❌ Hash chain invalid: %v", chainErr)
		return fmt.Errorf("checkpoint %s record %d: hash chain invalid: %w", cpPath, lineNum, chainErr)
	}

	// ── 2. Ed25519 signature ──────────────────────────────────────────────────
	canonical, err := marshalEntries(cp.Entries)
	if err != nil {
		return fmt.Errorf("checkpoint %s record %d: marshal entries: %w", cpPath, lineNum, err)
	}
	if sigErr := crypto.Verify(pubKey, canonical, cp.Signature); sigErr != nil {
		log.Printf("❌ Signature invalid: %v", sigErr)
		return fmt.Errorf("checkpoint %s record %d: signature invalid: %w", cpPath, lineNum, sigErr)
	}
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
