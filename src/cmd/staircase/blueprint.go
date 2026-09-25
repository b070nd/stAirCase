package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/b070nd/staircase-core/src/internal/blueprint"
	"github.com/b070nd/staircase-core/src/internal/domain"
	"github.com/spf13/cobra"
)

var blueprintCmd = &cobra.Command{
	Use:   "blueprint",
	Short: "Import and list blueprints: a project's automation, versioned in its own repository",
}

var blueprintImportCmd = &cobra.Command{
	Use:   "import <dir>",
	Short: "Import <dir>/blueprint.yaml as an immutable snapshot identified by its content hash",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		dir, err := filepath.Abs(args[0])
		if err != nil {
			return err
		}
		b, err := blueprint.Load(dir)
		if err != nil {
			return fmt.Errorf("blueprint %s: %w", dir, err)
		}
		// The source commit makes the snapshot reproducible — only if the
		// blueprint's files are exactly that commit.
		gitSHA := ""
		if dirty, err := gitOutput(dir, "status", "--porcelain", "--", "."); err == nil && dirty == "" {
			gitSHA, _ = gitOutput(dir, "rev-parse", "HEAD")
		}
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()
		hash := b.Hash()
		created, err := store.ImportBlueprint(domain.Blueprint{Hash: hash, Name: b.Name, Content: string(b.JSON()), SourceDir: dir, GitSHA: gitSHA})
		if err != nil {
			return err
		}
		if !created {
			fmt.Printf("✅ Blueprint %s %.12s is already imported (unchanged).\n", b.Name, hash)
			return nil
		}
		fmt.Printf("✅ Blueprint %s imported: %s\n", b.Name, hash)
		if gitSHA == "" {
			fmt.Fprintln(os.Stderr, "   ⚠️  not reproducible: the blueprint is not a clean git checkout, so no source commit was recorded")
		} else {
			fmt.Printf("   source commit %s\n", gitSHA)
		}
		fmt.Printf("   Bind a project with: staircase project bind <project-id> %.12s\n", hash)
		return nil
	},
}

var blueprintListCmd = &cobra.Command{
	Use:   "list",
	Short: "List imported blueprints",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()
		list, err := store.ListBlueprints()
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Println("No blueprints. Import one with: staircase blueprint import <dir>")
			return nil
		}
		rows := make([][]string, len(list))
		for i, b := range list {
			rows[i] = []string{b.Hash[:12], b.Name, fmt.Sprintf("%.12s", b.GitSHA), b.ImportedAt.Format("2006-01-02 15:04"), b.SourceDir}
		}
		table([]string{"HASH", "NAME", "COMMIT", "IMPORTED", "SOURCE"}, rows)
		return nil
	},
}

var projectBindCmd = &cobra.Command{
	Use:   "bind <project-id> <blueprint-hash>",
	Short: "Bind a project to a blueprint: creates a new topology version and the blueprint's cases",
	Long: `Bind a project to an imported blueprint (hash or unique prefix).

Binding creates a new topology version and new cases and stories from the
blueprint; existing topologies, cases and stories are not changed. Bound cases
run only as the blueprint defines them: the runtime.plan_pinned gate blocks a
run whose compiled plan differs from the blueprint. To change a bound case,
change the blueprint, import it and bind again.`,
	Args: cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		id, err := parseID("project-id", args[0])
		if err != nil {
			return err
		}
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()
		p, err := store.GetProject(id)
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("project #%d not found", id)
		}
		stored, err := store.FindBlueprint(args[1])
		if err != nil {
			return err
		}
		b, err := blueprint.Parse([]byte(stored.Content))
		if err != nil {
			return fmt.Errorf("blueprint %.12s: %w", stored.Hash, err)
		}
		if b.Hash() != stored.Hash {
			return fmt.Errorf("blueprint %.12s: stored content does not match its hash", stored.Hash)
		}
		version, cases, err := store.BindBlueprint(id, b.Binding())
		if err != nil {
			return fmt.Errorf("bind: %w", err)
		}
		fmt.Printf("✅ Project #%d bound to blueprint %s %.12s: topology v%d\n", id, b.Name, stored.Hash, version)
		for i, c := range cases {
			fmt.Printf("   case #%d  %s  (%d stories) → staircase compile %d\n", c, b.Cases[i].Slug, len(b.Cases[i].Stories), c)
		}
		return nil
	},
}

func init() {
	blueprintCmd.AddCommand(blueprintImportCmd, blueprintListCmd)
	projectCmd.AddCommand(projectBindCmd)
	rootCmd.AddCommand(blueprintCmd)
}
