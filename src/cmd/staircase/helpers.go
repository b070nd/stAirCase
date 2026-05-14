package main

import (
	"database/sql"
	"fmt"
	"os/exec"
	"strings"

	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/spf13/viper"
)

// openStore initialises the SQLite DB and returns a Store and the underlying
// connection. Callers must defer db.Close().
func openStore() (*persistence.Store, *sql.DB, error) {
	wsDir := viper.GetString("STAIRCASE_DIR")
	db, err := persistence.InitDB(wsDir)
	if err != nil {
		return nil, nil, fmt.Errorf("db init: %w", err)
	}
	return persistence.NewStore(db), db, nil
}

// parseID parses a decimal int64, returning a clear error on failure.
func parseID(label, s string) (int64, error) {
	var id int64
	if _, err := fmt.Sscan(s, &id); err != nil {
		return 0, fmt.Errorf("invalid %s %q: expected a numeric ID", label, s)
	}
	return id, nil
}

// gitOutput runs a git command in repoPath and returns trimmed stdout.
func gitOutput(repoPath string, args ...string) (string, error) {
	if repoPath == "" {
		return "", fmt.Errorf("no source path configured")
	}
	cmdArgs := append([]string{"-C", repoPath}, args...)
	out, err := exec.Command("git", cmdArgs...).Output()
	return strings.TrimSpace(string(out)), err
}

// table renders a simple fixed-width table to stdout.
// cols is the header row; rows is the data.
func table(cols []string, rows [][]string) {
	// Compute column widths.
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = len(c)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	pad := func(s string, w int) string {
		if len(s) >= w {
			return s
		}
		return s + strings.Repeat(" ", w-len(s))
	}

	printRow := func(cells []string) {
		for i, cell := range cells {
			if i > 0 {
				fmt.Print("  ")
			}
			w := 0
			if i < len(widths) {
				w = widths[i]
			}
			fmt.Print(pad(cell, w))
		}
		fmt.Println()
	}

	printRow(cols)
	sep := make([]string, len(cols))
	for i, w := range widths {
		sep[i] = strings.Repeat("─", w)
	}
	printRow(sep)
	for _, row := range rows {
		printRow(row)
	}
}
