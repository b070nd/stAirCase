package main

import (
	"fmt"
	"log"

	"github.com/b070nd/staircase-core/src/internal/crypto"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/b070nd/staircase-core/src/internal/runtime"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var offlineWheels string
var initSkipVenv bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize the stAirCase workspace, SQLite DB, and Python venv",
	Run: func(cmd *cobra.Command, args []string) {
		wsDir := viper.GetString("STAIRCASE_DIR")
		fmt.Printf("Initializing stAirCase in: %s\n", wsDir)

		// 1. Init SQLite Schema
		db, err := persistence.InitDB(wsDir)
		if err != nil {
			log.Fatalf("Database initialization failed: %v", err)
		}
		defer func() { _ = db.Close() }()
		fmt.Println("✅ SQLite Database initialized and schema verified.")

		// 2. Generate workspace encryption key (idempotent)
		if err := crypto.GenerateKey(wsDir); err != nil {
			log.Fatalf("Key generation failed: %v", err)
		}
		fmt.Println("🔑 Workspace encryption key ready.")

		// 2b. Generate Ed25519 signing key for audit checkpoints (idempotent)
		if err := crypto.GenerateSigningKey(wsDir); err != nil {
			log.Fatalf("Signing key generation failed: %v", err)
		}
		fmt.Println("🔏 Audit signing key ready.")

		// 3. Bootstrap Python Venv (skippable for air-gapped/CI setups that
		//    provision the runtime separately).
		if initSkipVenv {
			fmt.Println("⏭  Skipped Python venv bootstrap (--skip-venv); provision $STAIRCASE_DIR/venv yourself before 'staircase run'.")
		} else if err := runtime.BootstrapVenv(wsDir, offlineWheels); err != nil {
			log.Fatalf("Environment bootstrap failed: %v", err)
		}

		fmt.Println("\n🎉 stAirCase Workspace initialized. Ready to orchestrate.")
	},
}

func init() {
	initCmd.Flags().StringVar(&offlineWheels, "offline-wheels", "", "Path to local pip wheels for air-gapped installation")
	initCmd.Flags().BoolVar(&initSkipVenv, "skip-venv", false, "Skip Python venv bootstrap (provision $STAIRCASE_DIR/venv yourself)")
	rootCmd.AddCommand(initCmd)
}
