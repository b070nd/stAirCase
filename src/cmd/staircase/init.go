package main

import (
	"fmt"
	"log"

	"github.com/b070nd/staircase-core/src/internal/crypto"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Kept only so existing scripts do not break: the agent runtime is built in.
var offlineWheels string
var initSkipVenv bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize the stAirCase workspace: database and keys",
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

		fmt.Println("\n🎉 stAirCase Workspace initialized. Ready to orchestrate.")
	},
}

func init() {
	initCmd.Flags().StringVar(&offlineWheels, "offline-wheels", "", "No effect: there is no Python environment to install")
	initCmd.Flags().BoolVar(&initSkipVenv, "skip-venv", false, "No effect: there is no Python environment to install")
	for _, f := range []string{"offline-wheels", "skip-venv"} {
		_ = initCmd.Flags().MarkDeprecated(f, "the agent runtime is built into staircase; there is nothing to install")
	}
	rootCmd.AddCommand(initCmd)
}
