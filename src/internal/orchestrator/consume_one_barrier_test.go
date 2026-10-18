//go:build barriers

package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/barrier"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/persistence"
)

// TestConsumption_one_decision: what consume leaves on the journal and the chain, and what it tells the caller, at every point the run can end
// and every write that can fail (ADR 0005, the consumption point). The barrier tests show the same through whole runs.
func TestConsumption_one_decision(t *testing.T) {
	req := domain.YieldRequest{Type: "yield_request", AgentName: "coder", ActionType: domain.ActionFileEdit,
		ProposedEdits: []domain.ProposedEdit{{File: "a.txt", SearchBlock: MarkerNewFile, ReplaceBlock: "a\n"}}}
	reqJSON, _ := json.Marshal(req)
	line := func(seq int, request string) string {
		return `{"seq":` + string(rune('0'+seq)) + `,"source":"policy","request":` + request + "}\n"
	}
	for name, tc := range map[string]struct {
		approved    bool
		endAt       string // "before", or the barrier point at which the run ends
		journal     string // what the journal holds before
		breakChain  bool
		breakJourn  bool
		wantJournal int  // lines afterwards
		wantOK      bool // approved on the chain
		unconsumed  bool
		wantErr     string // "journal", "chain" or ""
	}{
		"consumed":                         {approved: true, wantJournal: 1, wantOK: true},
		"a rejection is not journaled":     {approved: false, wantJournal: 0},
		"ended before the journal":         {approved: true, endAt: "before", wantJournal: 0},
		"ended between journal and chain":  {approved: true, endAt: barrier.JournalSynced, wantJournal: 1, unconsumed: true},
		"ended under the chain's lock":     {approved: true, endAt: barrier.AuditAppend, wantJournal: 1, unconsumed: true},
		"kept by an interrupted attempt":   {approved: true, journal: line(1, string(reqJSON)), wantJournal: 1, wantOK: true},
		"another proposal, same number":    {approved: true, journal: line(1, `{"type":"other"}`), wantJournal: 1, wantErr: "journal"},
		"a torn line is dropped first":     {approved: true, journal: `{"seq":1,"sou`, wantJournal: 1, wantOK: true},
		"the journal cannot be written":    {approved: true, breakJourn: true, wantErr: "journal"},
		"the chain cannot be written":      {approved: true, breakChain: true, wantJournal: 1, wantErr: "chain"},
		"a rejection that cannot be added": {approved: false, breakChain: true, wantErr: "chain"},
	} {
		t.Run(name, func(t *testing.T) {
			ws := t.TempDir()
			db, err := persistence.InitDB(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			s := persistence.NewStore(db)
			v, _ := s.CreateVendor("V")
			p, _ := s.CreateProject(v.ID, "P", t.TempDir())
			if _, err := s.CreateSwarmTopology(p.ID, "sup", "memory", "langgraph"); err != nil {
				t.Fatal(err)
			}
			c, _ := s.CreateCase(p.ID)
			run, err := s.CreateRun(c.ID, 1, "main")
			if err != nil {
				t.Fatal(err)
			}
			jpath := journalPath(ws, run.ID)
			switch {
			case tc.breakJourn:
				if err := os.WriteFile(filepath.Join(ws, "journal"), nil, 0o600); err != nil { // a file where the directory goes
					t.Fatal(err)
				}
			case tc.journal != "":
				if err := saveRunFile(jpath, []byte(tc.journal)); err != nil {
					t.Fatal(err)
				}
			}
			if tc.breakChain {
				if _, err := db.Exec(`CREATE TRIGGER full BEFORE INSERT ON run_event_logs BEGIN SELECT RAISE(ABORT, 'disk full'); END`); err != nil {
					t.Fatal(err)
				}
			}
			live, end := context.WithCancel(context.Background())
			defer end()
			if tc.endAt == "before" {
				end()
			} else if tc.endAt != "" {
				t.Cleanup(barrier.SetHook(func(name string) {
					if name == tc.endAt {
						end()
					}
				}))
			}
			rl := ruling{resp: domain.Decide(tc.approved, "decided"), source: "policy", next: map[string]*approvedFile{"a.txt": {content: []byte("a\n"), mode: 0o644}}}
			var recorded []history.Decided
			err = NewRunner(s, ws).consume(live, run.ID, 1, req, &rl, func(journaled bool) history.Decided {
				d := yieldDecided(1, rl.source, req, rl.resp, "base", rl.next, "")
				d.Unconsumed = journaled && !rl.resp.Approved
				recorded = append(recorded, d)
				return d
			})

			var ce *consumeError
			switch tc.wantErr {
			case "":
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			default:
				if !errors.As(err, &ce) || ce.journal != (tc.wantErr == "journal") {
					t.Fatalf("want a %s error, got %v", tc.wantErr, err)
				}
				return
			}
			j, err := readJournalFile(ws, run.ID)
			if errors.Is(err, os.ErrNotExist) && tc.wantJournal == 0 {
				j, err = journalRead{}, nil
			}
			if err != nil || len(j.Entries) != tc.wantJournal || j.TornTail {
				b, _ := os.ReadFile(jpath)
				t.Errorf("the journal reads as %d entries (torn %v, %v): %q; want %d", len(j.Entries), j.TornTail, err, b, tc.wantJournal)
			}
			events, err := s.ListVerifiedEventLogs(run.ID)
			if err != nil || len(events) != 1 || events[0].EventType != "yield_decided" {
				t.Fatalf("the chain holds %d events (%v): want the one decision", len(events), err)
			}
			last := recorded[len(recorded)-1]
			if last.Approved != tc.wantOK || rl.resp.Approved != tc.wantOK || last.Unconsumed != tc.unconsumed {
				t.Errorf("recorded approved=%v unconsumed=%v, told the caller approved=%v; want %v, %v", last.Approved, last.Unconsumed, rl.resp.Approved, tc.wantOK, tc.unconsumed)
			}
			if !tc.wantOK && tc.approved && (rl.source != "orchestrator" || rl.next != nil) {
				t.Errorf("an approval that was not consumed is the orchestrator's rejection, with nothing to apply: %+v", rl)
			}
		})
	}
}
