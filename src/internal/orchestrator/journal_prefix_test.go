package orchestrator

import (
	"encoding/json"
	"testing"
)

// TestIsCutJournalLine: every proper prefix of a line appendJournal writes is a cut write; text that no
// completion could turn into such a line is not. (Internal test: it exercises the classifier on its own,
// the real-recovery cases are in recover_history_test.go.)
func TestIsCutJournalLine(t *testing.T) {
	req := json.RawMessage(`{"agent_name":"coder","proposed_edits":[{"file":"a.txt","search_block":"(new file)","replace_block":"x \"y\"\n"}]}`)
	line, _ := json.Marshal(journalEntry{Seq: 12, Source: "operator", Request: req})
	for i := 0; i < len(line); i++ {
		if !isCutJournalLine(line[:i]) {
			t.Fatalf("a cut of a real line at byte %d was called corruption: %q", i, line[:i])
		}
	}
	for _, c := range []string{
		`not json at all`,
		`{"seq":3,]`,
		`{"seq":3,"source":"operator","request":]`,
		`{"seq":x`,
		`{"source":"operator"`,
		`{"seq":3,"request":{}}`,
		`{"seq":3,"source":"operator","request":{} extra`,
		`{"seq":3,"source":"operator","request":{}}}`,
		`{"seq":3,"source":"operator","request":{"a":}}`,
		`{"seq":3,"source":"operator","request":{"a":1,]}`,
		`null`,
		`[`,
		`{"seq":3,"source":"op` + "\x01",
	} {
		if isCutJournalLine([]byte(c)) {
			t.Errorf("%q is not a prefix of any journal line but was taken for a cut write", c)
		}
	}
}
