package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/signal"

	"github.com/b070nd/stAirCase/src/internal/approvalhttp"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	servePort  int
	serveToken string
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "One review page in your browser for every running session",
	Long: `Serves a local page that lists the proposals waiting in every session that is
running with --approval-port (staircase claude, codex, review, seal, run), with
the exact change of each, and lets you approve or reject them in one place.

Sessions announce themselves in the workspace; the page needs only its own key,
which is in the link printed here (the sessions' keys never reach the browser).
The page is served only to this machine.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		token := serveToken
		if token == "" {
			raw := make([]byte, 16)
			if _, err := rand.Read(raw); err != nil {
				return err
			}
			token = hex.EncodeToString(raw)
		}
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
		defer stop()
		hub := approvalhttp.NewHub(viper.GetString("STAIRCASE_DIR"), token)
		if err := hub.Start(ctx, fmt.Sprintf("127.0.0.1:%d", servePort)); err != nil {
			return err
		}
		fmt.Printf("🌐 Review every session in your browser: %s\n   Stop with Ctrl-C. Start sessions with --approval-port <port>.\n", hub.ReviewURL())
		<-ctx.Done()
		return nil
	},
}

func init() {
	serveCmd.Flags().IntVar(&servePort, "port", 8765, "Port to listen on (127.0.0.1 only)")
	serveCmd.Flags().StringVar(&serveToken, "token", "", "The page's key (default: a new one, in the printed link)")
	rootCmd.AddCommand(serveCmd)
}
