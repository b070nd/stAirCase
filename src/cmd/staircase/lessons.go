package main

import (
	"encoding/json"
	"slices"
	"strings"

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
			events, err := store.ListEventLogs(r.ID)
			if err != nil {
				return nil, err
			}
			var files []string // of the last proposal
			for _, e := range events {
				switch e.EventType {
				case "yield_request":
					var req struct {
						Edits []struct {
							File string `json:"file"`
						} `json:"proposed_edits"`
					}
					files = nil
					if json.Unmarshal([]byte(e.Payload), &req) == nil {
						for _, ed := range req.Edits {
							if !slices.Contains(files, ed.File) {
								files = append(files, ed.File)
							}
						}
					}
				case "yield_decided":
					var d struct {
						Source   string `json:"source"`
						Approved bool   `json:"approved"`
						Feedback string `json:"feedback"`
					}
					if json.Unmarshal([]byte(e.Payload), &d) != nil || d.Approved || d.Source != "operator" {
						continue
					}
					if fb := strings.TrimSpace(d.Feedback); fb != "" {
						l := strings.Join(files, ", ") + ": " + fb
						if !slices.Contains(lessons, l) {
							lessons = append(lessons, l)
						}
					}
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
