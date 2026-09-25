package main

import (
	"encoding/json"
	"fmt"
	"os/user"
	"path"
	"strings"

	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/spf13/cobra"
)

var storyCmd = &cobra.Command{
	Use:   "story",
	Short: "Manage User Stories within a Case",
}

var storyAddCmd = &cobra.Command{
	Use:   "add <case-id> <description>",
	Short: "Add a User Story to a Case",
	Args:  cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		caseID, err := parseID("case-id", args[0])
		if err != nil {
			return err
		}
		if c, _ := store.GetCase(caseID); c == nil {
			return fmt.Errorf("case #%d not found", caseID)
		}

		us, err := store.CreateUserStory(caseID, args[1])
		if err != nil {
			return fmt.Errorf("create story: %w", err)
		}
		fmt.Printf("✅ Story #%d added to Case #%d — status: %s\n", us.ID, caseID, us.Status)
		return nil
	},
}

var storyListCmd = &cobra.Command{
	Use:   "list <case-id>",
	Short: "List all User Stories for a Case",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		caseID, err := parseID("case-id", args[0])
		if err != nil {
			return err
		}

		stories, err := store.ListUserStoriesByCase(caseID)
		if err != nil {
			return err
		}
		if len(stories) == 0 {
			fmt.Printf("No stories for case #%d.\n", caseID)
			return nil
		}

		rows := make([][]string, len(stories))
		for i, s := range stories {
			desc := s.Description
			if len(desc) > 60 {
				desc = desc[:57] + "..."
			}
			rows[i] = []string{fmt.Sprint(s.ID), s.Status, desc}
		}
		table([]string{"ID", "Status", "Description"}, rows)
		return nil
	},
}

var storyInvalidateCmd = &cobra.Command{
	Use:   "invalidate <story-id>",
	Short: "Mark a User Story as INVALIDATED (it must be re-verified and accepted again)",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		storyID, err := parseID("story-id", args[0])
		if err != nil {
			return err
		}
		if err := store.UpdateUserStoryStatus(storyID, persistence.StoryStatusInvalidated); err != nil {
			return fmt.Errorf("invalidate story: %w", err)
		}
		fmt.Printf("✅ Story #%d marked INVALIDATED — verify the work again, then run 'staircase story accept %d'.\n", storyID, storyID)
		return nil
	},
}

var storyAcceptCmd = &cobra.Command{
	Use:   "accept <story-id>",
	Short: "Record operator acceptance after independently verifying a story",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		storyID, err := parseID("story-id", args[0])
		if err != nil {
			return err
		}
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()
		actor := "unknown"
		if u, err := user.Current(); err == nil {
			actor = u.Username
		}
		runID, caseStatus, err := store.AcceptUserStory(storyID, actor)
		if err != nil {
			return fmt.Errorf("accept story: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Story #%d accepted by %s (audit: run #%d). Case status: %s.\n", storyID, actor, runID, caseStatus)
		return nil
	},
}

var (
	storyScopeAllow    []string
	storyScopeMaxFiles int
)

var storyScopeCmd = &cobra.Command{
	Use:   "scope <story-id>",
	Short: "Set the paths a story's runs may change (drift supervision)",
	Long: `Set the paths a story's runs may change. A proposal touching any other path
goes to a human instead of the policy, and more than the policy's
max_scope_violations such proposals halt the run. --allow takes globs relative
to the repository root (** spans directories) and may repeat; --max-files caps
the distinct files the story's runs change before a human must look. With no
flags the scope is cleared. Stories of a case bound to a blueprint take their
scope from the blueprint: change it there.`,
	Args: cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		id, err := parseID("story-id", args[0])
		if err != nil {
			return err
		}
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()
		st, err := store.GetUserStory(id)
		if err != nil {
			return err
		}
		if st == nil {
			return fmt.Errorf("story #%d not found", id)
		}
		if hash, _, err := store.CaseBlueprint(st.CaseID); err != nil {
			return err
		} else if hash != "" {
			return fmt.Errorf("story #%d belongs to case #%d, bound to blueprint %.12s — change the scope in the blueprint and bind again", id, st.CaseID, hash)
		}
		for _, g := range storyScopeAllow {
			if _, err := path.Match(g, ""); err != nil || g == "" || path.IsAbs(g) || strings.HasPrefix(g, "../") {
				return fmt.Errorf("--allow %q: a glob relative to the repository root", g)
			}
		}
		scope := ""
		if len(storyScopeAllow) > 0 || storyScopeMaxFiles > 0 {
			b, _ := json.Marshal(struct {
				Allow    []string `json:"allow,omitempty"`
				MaxFiles int      `json:"max_files,omitempty"`
			}{storyScopeAllow, storyScopeMaxFiles})
			scope = string(b)
		}
		if err := store.SetUserStoryScope(id, scope); err != nil {
			return err
		}
		if scope == "" {
			fmt.Printf("✅ Story #%d has no scope.\n", id)
		} else {
			fmt.Printf("✅ Story #%d scope: %s — recompile case #%d\n", id, scope, st.CaseID)
		}
		return nil
	},
}

func init() {
	storyScopeCmd.Flags().StringArrayVar(&storyScopeAllow, "allow", nil, "Glob of paths the story may change (repeatable)")
	storyScopeCmd.Flags().IntVar(&storyScopeMaxFiles, "max-files", 0, "Most distinct files the story's runs change before a human must look (0 = no cap)")
	storyCmd.AddCommand(storyAddCmd, storyListCmd, storyInvalidateCmd, storyAcceptCmd, storyScopeCmd)
	rootCmd.AddCommand(storyCmd)
}
