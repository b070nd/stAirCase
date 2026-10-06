package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/b070nd/stAirCase/src/internal/approvalhttp"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/persistence"
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
The page is served only to this machine.

There is one coordinator for a workspace: a second "staircase serve" is refused and
names the first. A session whose process was killed leaves a registration behind;
this removes it, shows nothing of it, and lists the runs that were interrupted
before they committed (recover them with "staircase recover"). It touches only the
workspace, never your checkout.`,
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
		wsDir := viper.GetString("STAIRCASE_DIR")
		hub := approvalhttp.NewHub(wsDir, token)
		if err := hub.Start(ctx, fmt.Sprintf("127.0.0.1:%d", servePort)); err != nil {
			var running *approvalhttp.HubRunningError
			if errors.As(err, &running) {
				return fmt.Errorf("%w: use that one (there is one coordinator for a workspace)", running)
			}
			return err
		}
		for _, s := range hub.Removed() { // sessions whose process was killed: nothing of theirs is shown or decided
			fmt.Printf("🧹 Removed the registration of %q: its process is gone\n", s.Name)
		}
		interruptedRunsOwned(wsDir)
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

// interruptedRunsOwned lists the runs whose record says RUNNING while no process owns them any more: a run
// holds its owner lock for its whole life, so a free lock means the process was killed. It reads the database
// and changes nothing.
func interruptedRunsOwned(wsDir string) {
	db, err := persistence.InitDB(wsDir)
	if err != nil {
		return
	}
	defer func() { _ = db.Close() }()
	runs, err := persistence.NewStore(db).ListRunsByStatus(persistence.RunStatusRunning)
	if err != nil {
		return
	}
	for _, r := range runs {
		held, known := orchestrator.RunOwner(wsDir, r.ID)
		switch {
		case known && !held:
			fmt.Printf("⚠️  Run #%d was killed before it finished (its process is gone). Continue it: staircase resume %d, or recover what it approved: staircase recover %d\n", r.ID, r.ID, r.ID)
		case !known:
			fmt.Printf("ℹ️  Run #%d says it is running; whether its process is alive cannot be told (it began before runs held a lock)\n", r.ID)
		}
	}
}
