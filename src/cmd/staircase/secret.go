package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/b070nd/staircase-core/src/internal/crypto"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

var secretCmd = &cobra.Command{
	Use:   "secret",
	Short: "Manage encrypted secrets (never stored in env vars)",
}

var secretProjectID int64

var secretSetCmd = &cobra.Command{
	Use:   "set <key-name>",
	Short: "Store an AES-256-GCM encrypted secret in the workspace (reads value from stdin)",
	Long: `Reads the secret value from stdin to prevent it appearing in shell history.

Pipe the value in non-interactively:
  printf 'my-secret' | staircase secret set MY_KEY

Or enter it interactively (input will not be echoed):
  staircase secret set MY_KEY`,
	Args: cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		// Read the secret value from stdin (CHECK 4.5.1: never accept as argv
		// so it doesn't leak into shell history / process listings).
		var value string
		stat, _ := os.Stdin.Stat()
		if stat != nil && (stat.Mode()&os.ModeCharDevice) != 0 {
			// Interactive terminal — suppress echo so the value isn't visible.
			fmt.Fprint(os.Stderr, "Enter secret value: ")
			raw, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr) // restore newline after hidden input
			if err != nil {
				return fmt.Errorf("read secret: %w", err)
			}
			value = string(raw)
		} else {
			// Piped / redirected — read until EOF and strip trailing newline.
			raw, err := io.ReadAll(os.Stdin)
			if err != nil {
				return fmt.Errorf("read secret: %w", err)
			}
			value = strings.TrimRight(string(raw), "\r\n")
		}
		if value == "" {
			return fmt.Errorf("secret value must not be empty")
		}

		wsDir := viper.GetString("STAIRCASE_DIR")
		keyPath := filepath.Join(wsDir, crypto.KeyFile)

		// Hold a shared advisory lock on the key file from LoadKey through
		// CreateSecret.  Without this, a concurrent 'staircase secret rotate'
		// could commit under the new key between our Encrypt call and the DB
		// insert, stranding the new ciphertext (it would be encrypted under the
		// now-superseded key).  The exclusive rotate lock (LOCK_NB) fails fast if
		// we hold the shared lock — it does not wait, it refuses
		// (CHECK 4.3.3 extension to write path).
		lockF, err := os.OpenFile(keyPath, os.O_RDONLY, 0)
		if err != nil {
			return fmt.Errorf("open key for lock: %w", err)
		}
		defer func() { _ = lockF.Close() }()
		if err := flockShared(lockF.Fd()); err != nil {
			return fmt.Errorf("workspace is locked by another process — is a rotate active?: %w", err)
		}

		key, err := crypto.LoadKey(wsDir)
		if err != nil {
			_ = flockUnlock(lockF.Fd())
			return err
		}

		encrypted, err := crypto.Encrypt(key, value)
		if err != nil {
			_ = flockUnlock(lockF.Fd())
			return fmt.Errorf("encrypt: %w", err)
		}

		store, db, err := openStore()
		if err != nil {
			_ = flockUnlock(lockF.Fd())
			return err
		}
		defer func() { _ = db.Close() }()

		var pid *int64
		if secretProjectID != 0 {
			pid = &secretProjectID
		}

		sec, err := store.CreateSecret(args[0], encrypted, pid)
		_ = flockUnlock(lockF.Fd()) // release before any further I/O
		if err != nil {
			return fmt.Errorf("store secret: %w", err)
		}

		scope := "global"
		if pid != nil {
			scope = fmt.Sprintf("project #%d", *pid)
		}
		fmt.Printf("✅ Secret #%d %q stored (%s).\n", sec.ID, sec.KeyName, scope)
		return nil
	},
}

var secretListCmd = &cobra.Command{
	Use:   "list",
	Short: "List stored secret key names (values are never shown)",
	RunE: func(_ *cobra.Command, _ []string) error {
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		// Query all secrets directly — the store GetSecret is for lookup by name.
		// Use the DB directly via a raw list query.
		db2 := db
		rows, err := db2.Query(
			`SELECT id, key_name, scoped_to_project_id FROM secrets ORDER BY key_name`,
		)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()

		type row struct {
			id    int64
			key   string
			scope string
		}
		var secretRows []row
		for rows.Next() {
			var id int64
			var keyName string
			var pid *int64
			if err := rows.Scan(&id, &keyName, &pid); err != nil {
				return err
			}
			scope := "global"
			if pid != nil {
				scope = fmt.Sprintf("project #%d", *pid)
			}
			secretRows = append(secretRows, row{id, keyName, scope})
		}

		_ = store // silence unused warning

		if len(secretRows) == 0 {
			fmt.Println("No secrets stored. Use 'staircase secret set <key> <value>'.")
			return nil
		}

		tblRows := make([][]string, len(secretRows))
		for i, r := range secretRows {
			tblRows[i] = []string{fmt.Sprint(r.id), r.key, r.scope, "***encrypted***"}
		}
		table([]string{"ID", "Key", "Scope", "Value"}, tblRows)
		return nil
	},
}

// secretRotateCmd re-encrypts all secrets under a fresh AES-256 key.
// The old key file is replaced atomically (temp+rename) and every ciphertext
// is updated in a single DB transaction, so a crash during rotation leaves
// the workspace in a consistent state (either fully rotated or not at all).
// An exclusive non-blocking flock on the key file fails fast if any concurrent
// process (active run, secret set) already holds a shared lock (CHECK 4.3.3).
var secretRotateCmd = &cobra.Command{
	Use:   "rotate",
	Short: "Re-encrypt all secrets under a new AES-256 workspace key",
	Long: `Generates a fresh AES-256 key, re-encrypts every stored secret in a
single atomic DB transaction, then replaces the old key file.

The operation holds an exclusive non-blocking advisory lock on the workspace
key file.  It fails fast (does not wait) if any concurrent process — an active
run or a 'secret set' command — already holds a shared lock on the key.`,
	RunE: func(_ *cobra.Command, _ []string) error {
		wsDir := viper.GetString("STAIRCASE_DIR")
		keyPath := filepath.Join(wsDir, crypto.KeyFile)

		// Acquire exclusive non-blocking advisory lock.  Fails fast if any
		// concurrent process (active run, 'secret set') holds a shared lock
		// (CHECK 4.3.3).
		lockF, err := os.OpenFile(keyPath, os.O_RDONLY, 0)
		if err != nil {
			return fmt.Errorf("open key for lock: %w", err)
		}
		defer func() { _ = lockF.Close() }()
		if err := flockExclusive(lockF.Fd()); err != nil {
			return fmt.Errorf("workspace key is locked — is a run or 'secret set' active?: %w", err)
		}
		defer func() { _ = flockUnlock(lockF.Fd()) }()

		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		// reencrypt re-encrypts every secret in a single DB transaction (CHECK 4.3.2).
		// Opens a pinned *sql.Conn, sets synchronous=FULL on it, then runs the
		// rotation transaction on the same connection — guaranteeing that the PRAGMA
		// and the transaction share the underlying SQLite connection and the WAL
		// write is fsynced before COMMIT returns (power-loss durable).
		reencrypt := func(oldKey, newKey []byte) error {
			conn, err := db.Conn(context.Background())
			if err != nil {
				return fmt.Errorf("rotate: open conn: %w", err)
			}
			defer func() { _ = conn.Close() }()
			if _, err := conn.ExecContext(context.Background(), "PRAGMA synchronous=FULL"); err != nil {
				return fmt.Errorf("rotate: set synchronous=FULL: %w", err)
			}
			defer func() {
				_, _ = conn.ExecContext(context.Background(), "PRAGMA synchronous=NORMAL")
			}()
			return store.RotateSecretsOnConn(context.Background(), conn, oldKey, newKey, crypto.Decrypt, crypto.Encrypt)
		}

		// tryDecryptAny probes whether the DB was already committed to the new key
		// (used by crash-recovery in crypto.RotateKey to distinguish "pending" from
		// "pending but already committed").  Scans every secret so a partial
		// failure (e.g. first secret still under old key) is not mistaken for
		// an uncommitted DB.
		tryDecryptAny := func(key []byte) bool {
			secrets, err := store.ListAllSecrets()
			if err != nil || len(secrets) == 0 {
				return false
			}
			for _, s := range secrets {
				if _, decErr := crypto.Decrypt(key, s.EncryptedValue); decErr == nil {
					return true
				}
			}
			return false
		}

		// RotateKey handles key generation, journal-based crash recovery, and
		// atomic key-file installation (CHECK 4.3.2).
		if err := crypto.RotateKey(wsDir, reencrypt, tryDecryptAny); err != nil {
			return err
		}

		fmt.Println("✅ Workspace key rotated and all secrets re-encrypted.")
		return nil
	},
}

func init() {
	secretSetCmd.Flags().Int64Var(&secretProjectID, "project", 0, "Scope secret to a specific project ID (0 = global)")
	secretCmd.AddCommand(secretSetCmd, secretListCmd, secretRotateCmd)
	rootCmd.AddCommand(secretCmd)
}
