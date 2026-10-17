package main

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/persistence"
)

// maxLessons bounds how many earlier rejections a plan carries.
const maxLessons = 10

// projectLessons are what a person rejected in the project's earlier runs,
// with their reasons, newest first: "<files>: <reason>". They come from the
// audit chains, so nothing new is stored; refusals by the orchestrator or a
// policy and rejections without a reason are left out.
func projectLessons(store *persistence.Store, projectID int64) ([]string, error) {
	cases, err := store.ListCasesByProject(projectID)
	if err != nil {
		return nil, err
	}
	var lessons []string
	for _, c := range cases {
		runs, err := store.ListRunsByCase(c.ID)
		if err != nil {
			return nil, err
		}
		for _, r := range runs {
			h, err := history.Read(store, r.ID)
			if err != nil { // a run whose history cannot be trusted teaches nothing: said, and skipped
				fmt.Fprintf(os.Stderr, "⚠️  lessons: run #%d skipped: %v\n", r.ID, err)
				continue
			}
			for _, d := range h.Decisions {
				fb := strings.TrimSpace(d.Feedback)
				if d.Approved || d.Source != "operator" || fb == "" {
					continue
				}
				var files []string // of the proposal the person rejected
				if d.Request != nil {
					for _, ed := range d.Request.ProposedEdits {
						if !slices.Contains(files, ed.File) {
							files = append(files, ed.File)
						}
					}
				}
				l := fb
				if len(files) > 0 {
					l = strings.Join(files, ", ") + ": " + fb
				}
				if !slices.Contains(lessons, l) {
					lessons = append(lessons, l)
				}
			}
		}
	}
	slices.Reverse(lessons) // newest first
	if len(lessons) > maxLessons {
		lessons = lessons[:maxLessons]
	}
	return lessons, nil
}
