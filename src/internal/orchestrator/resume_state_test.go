package orchestrator

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stAirCase/src/internal/history"
)

// TestTerms_read_as_runs_saved_them (ADR 0010, Q22): a run's options.json, with every key runs write, is read into the terms it
// was saved from, and refused when the chain bound other bytes, when it is of another version, or when no run could decide under it.
func TestTerms_read_as_runs_saved_them(t *testing.T) {
	saved := `{"version":1,"agreed":"dev@example.com","approve_in_scope":true,"approve_on_evidence":true,"allow_shell_exec":true,
"sandbox":"required","require_signed_approvals":false,"sign_key":"/k","sign_as":"dev@example.com","require_evidence":true,
"checks":["go test ./..."],"check_timeout_ns":180000000000,"signal_model":"m","signal_url":"http://127.0.0.1:1",
"validator_models":["x","y"],"away":true,"away_commands":["make"],"away_tools":["mcp__a__b"],
"protected_paths":["tests/**"]}`
	ws := t.TempDir()
	write := func(b string) {
		t.Helper()
		if err := saveRunFile(savedOptionsPath(ws, 1), []byte(b)); err != nil {
			t.Fatal(err)
		}
	}
	bound := func(digest string) history.History { return history.History{Bound: history.Bound{TermsSHA256: digest}} }
	t.Cleanup(SetNoNetworkModeForTest(func(mode string) bool { return mode == "required" }))

	write(saved)
	got, err := loadTerms(ws, 1, bound(sha256Hex([]byte(saved))), "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	want := Terms{Agreed: "dev@example.com", ApproveInScope: true, ApproveOnEvidence: true, AllowShellExec: true, Sandbox: "required",
		SignKey: "/k", SignAs: "dev@example.com", RequireEvidence: true, Checks: []string{"go test ./..."}, CheckTimeout: 3 * time.Minute,
		SignalModel: "m", SignalURL: "http://127.0.0.1:1", ValidatorModels: []string{"x", "y"}, Away: true, AwayCommands: []string{"make"},
		AwayTools: []string{"mcp__a__b"}, ProtectedPaths: []string{"tests/**"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("read %+v\nwant %+v", got, want)
	}
	if _, err := loadTerms(ws, 1, bound(""), "claude-code"); err != nil {
		t.Errorf("a run bound before the digest was recorded is read as before: %v", err)
	}
	for name, tc := range map[string]struct {
		file, digest, why string
	}{
		"other bytes than the chain bound": {saved, "0000", "not the ones the audit chain names"},
		"another version":                  {`{"version":2,"agreed":"x"}`, "", "unreadable"},
		"not JSON":                         {`{`, "", "unreadable"},
		"terms no run decides under":       {`{"version":1,"approve_in_scope":true}`, "", "nobody agreed"},
	} {
		write(tc.file)
		if _, err := loadTerms(ws, 1, bound(tc.digest), ""); err == nil || !strings.Contains(err.Error(), tc.why) {
			t.Errorf("%s: %v, want %q", name, err, tc.why)
		}
	}
	if _, err := loadTerms(t.TempDir(), 1, bound(""), ""); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("no saved terms: %v", err)
	}
}

// TestTerms_delivery_mode_in_the_saved_form (ADR 0012): the saved form of terms that deliver by review is byte for byte what it was
// before the field existed (no new key), a run saved before the field existed reads as review, and a policy delivery is saved,
// bound and read back whole.
func TestTerms_delivery_mode_in_the_saved_form(t *testing.T) {
	ws := t.TempDir()
	write := func(b string) history.History {
		t.Helper()
		if err := saveRunFile(savedOptionsPath(ws, 1), []byte(b)); err != nil {
			t.Fatal(err)
		}
		return history.History{Bound: history.Bound{TermsSHA256: sha256Hex([]byte(b))}}
	}

	// the golden form: every key a review run writes, in the order it writes them, and no delivery key
	review := Terms{Agreed: "dev@example.com", Away: true, ApproveOnEvidence: true, ApproveInScope: true, Checks: []string{"go test ./..."},
		ProtectedPaths: []string{"tests/**"}}
	got, err := json.Marshal(savedTerms{termsVersion, review})
	if err != nil {
		t.Fatal(err)
	}
	const golden = `{"version":1,"agreed":"dev@example.com","approve_in_scope":true,"approve_on_evidence":true,"checks":["go test ./..."],` +
		`"away":true,"protected_paths":["tests/**"]}`
	if string(got) != golden {
		t.Errorf("saved form of a review run changed:\n got %s\nwant %s", got, golden)
	}

	// a run saved before the field existed
	old, err := loadTerms(ws, 1, write(golden), "")
	if err != nil {
		t.Fatal(err)
	}
	if old.Deliver != "" || old.DeliverPaths != nil {
		t.Errorf("a run saved before the field existed must read as review: %+v", old)
	}

	// a policy delivery round trip
	policy := review
	policy.Deliver, policy.DeliverPaths, policy.SignKey = DeliverPolicy, []string{"docs/**"}, "/k"
	b, err := json.Marshal(savedTerms{termsVersion, policy})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"deliver":"policy"`, `"deliver_paths":["docs/**"]`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("saved form lacks %s: %s", key, b)
		}
	}
	back, err := loadTerms(ws, 1, write(string(b)), "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, policy) {
		t.Errorf("read %+v\nwant %+v", back, policy)
	}
}
