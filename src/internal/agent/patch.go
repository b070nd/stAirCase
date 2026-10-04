package agent

import (
	"errors"
	"fmt"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
)

// parsePatch turns Codex's apply_patch text into the edits staircase
// decides: a new file's whole content, one search/replace per hunk (context
// and removed lines found, context and added lines put in their place), a
// deletion. What it cannot map exactly (a move, a hunk with nothing to find)
// is refused, so the agent can say it another way.
func parsePatch(patch string) ([]domain.ProposedEdit, error) {
	lines := strings.Split(strings.TrimRight(patch, "\n"), "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "*** Begin Patch" || strings.TrimSpace(lines[len(lines)-1]) != "*** End Patch" {
		return nil, errors.New("not an apply_patch text: it must start with *** Begin Patch and end with *** End Patch")
	}
	var (
		edits          []domain.ProposedEdit
		file, kind     string // the section: "add" or "update"
		body           []string
		search, replac []string
		hunks          int
	)
	flushHunk := func() error {
		if len(search) == 0 && len(replac) == 0 {
			return nil
		}
		if len(search) == 0 {
			return fmt.Errorf("%s: a change with only added lines cannot be placed; include the lines around it", file)
		}
		edits = append(edits, domain.ProposedEdit{File: file,
			SearchBlock: strings.Join(search, "\n") + "\n", ReplaceBlock: strings.Join(replac, "\n") + "\n"})
		search, replac = nil, nil
		hunks++
		return nil
	}
	flush := func() error {
		switch kind {
		case "add":
			content := ""
			if len(body) > 0 {
				content = strings.Join(body, "\n") + "\n"
			}
			edits = append(edits, domain.ProposedEdit{File: file, SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: content})
		case "update":
			if err := flushHunk(); err != nil {
				return err
			}
			if hunks == 0 {
				return fmt.Errorf("%s: the update changes nothing", file)
			}
		}
		kind, body, hunks = "", nil, 0
		return nil
	}
	for _, l := range lines[1 : len(lines)-1] {
		switch {
		case strings.HasPrefix(l, "*** Add File: "):
			if err := flush(); err != nil {
				return nil, err
			}
			file, kind = strings.TrimPrefix(l, "*** Add File: "), "add"
		case strings.HasPrefix(l, "*** Update File: "):
			if err := flush(); err != nil {
				return nil, err
			}
			file, kind = strings.TrimPrefix(l, "*** Update File: "), "update"
		case strings.HasPrefix(l, "*** Delete File: "):
			if err := flush(); err != nil {
				return nil, err
			}
			edits = append(edits, domain.ProposedEdit{File: strings.TrimPrefix(l, "*** Delete File: "),
				SearchBlock: orchestrator.MarkerDeleteFile})
		case strings.HasPrefix(l, "*** Move to: "):
			return nil, fmt.Errorf("%s: moving a file is not supported; add the new file and delete the old one", file)
		case l == "*** End of File":
		case strings.HasPrefix(l, "***"):
			return nil, fmt.Errorf("unknown patch line %q", l)
		case kind == "add" && strings.HasPrefix(l, "+"):
			body = append(body, l[1:])
		case kind == "update" && strings.HasPrefix(l, "@@"):
			if err := flushHunk(); err != nil {
				return nil, err
			}
		case kind == "update" && (strings.HasPrefix(l, " ") || l == ""):
			ctx := strings.TrimPrefix(l, " ")
			search, replac = append(search, ctx), append(replac, ctx)
		case kind == "update" && strings.HasPrefix(l, "-"):
			search = append(search, l[1:])
		case kind == "update" && strings.HasPrefix(l, "+"):
			replac = append(replac, l[1:])
		default:
			return nil, fmt.Errorf("unexpected patch line %q", l)
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if len(edits) == 0 {
		return nil, errors.New("the patch changes no file")
	}
	return edits, nil
}
