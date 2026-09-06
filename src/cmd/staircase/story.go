package main

import (
	"fmt"

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
	Short: "Mark a User Story as INVALIDATED (forces AI re-evaluation on next run)",
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
		fmt.Printf("✅ Story #%d marked INVALIDATED — it will be re-evaluated on the next run.\n", storyID)
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
		if err := store.UpdateUserStoryStatus(storyID, persistence.StoryStatusImplemented); err != nil {
			return fmt.Errorf("accept story: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Story #%d accepted by operator and marked IMPLEMENTED.\n", storyID)
		return nil
	},
}

func init() {
	storyCmd.AddCommand(storyAddCmd, storyListCmd, storyInvalidateCmd, storyAcceptCmd)
	rootCmd.AddCommand(storyCmd)
}
