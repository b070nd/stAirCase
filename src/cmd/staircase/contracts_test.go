package main

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/policy"
	"github.com/b070nd/stAirCase/src/internal/signal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests keep the documentation of the six contracts (docs/compatibility.md) true by reading the
// code: a field, an event or a rule that exists in the code and is not in the document that describes
// the contract fails here, so a contract cannot grow in silence.

func readDoc(t *testing.T, rel string) string {
	b, err := os.ReadFile(filepath.Join(repoRoot, rel))
	require.NoError(t, err)
	return string(b)
}

// jsonFields lists the JSON names of a struct type and of the struct types it holds.
func jsonFields(t reflect.Type, seen map[reflect.Type]bool) []string {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || seen[t] {
		return nil
	}
	seen[t] = true
	var out []string
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			continue
		}
		out = append(out, name)
		out = append(out, jsonFields(t.Field(i).Type, seen)...)
	}
	return out
}

func mentions(doc, name string) bool {
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`).MatchString(doc)
}

// TestContract_every_certificate_field_is_in_the_specification: the change certificate is the one contract
// that is stable now; a predicate field that the specification does not describe is a field no independent
// verifier can know about.
func TestContract_every_certificate_field_is_in_the_specification(t *testing.T) {
	spec := readDoc(t, "docs/spec/certificate-v1.md")
	fields := jsonFields(reflect.TypeOf(certificate.Predicate{}), map[reflect.Type]bool{})
	require.NotEmpty(t, fields)
	for _, f := range fields {
		assert.True(t, mentions(spec, f), "the certificate field %q is not in docs/spec/certificate-v1.md", f)
	}
}

// TestContract_the_in_toto_draft_names_every_certificate_field: the proposal to the in-toto framework must not
// describe an older predicate than the one stAirCase writes.
func TestContract_the_in_toto_draft_names_every_certificate_field(t *testing.T) {
	draft := readDoc(t, "docs/spec/in-toto-predicate.md")
	for _, f := range jsonFields(reflect.TypeOf(certificate.Predicate{}), map[reflect.Type]bool{}) {
		assert.True(t, mentions(draft, f), "the certificate field %q is not in docs/spec/in-toto-predicate.md", f)
	}
}

// TestContract_every_policy_field_is_documented: the policy file's rules and limits.
func TestContract_every_policy_field_is_documented(t *testing.T) {
	doc := readDoc(t, "docs/approvals.md") + readDoc(t, "docs/drift.md")
	fields := jsonFields(reflect.TypeOf(policy.Engine{}), map[reflect.Type]bool{})
	require.NotEmpty(t, fields)
	for _, f := range fields {
		assert.True(t, mentions(doc, f), "the policy field %q is not in docs/approvals.md or docs/drift.md", f)
	}
}

// eventNames are the audit event types the source writes: the literal given to the audit functions, the store's
// chained append, and the kinds of end-of-run violation (which are audited under their own name).
func eventNames(t *testing.T) []string {
	var names = map[string]bool{}
	re := []*regexp.Regexp{
		regexp.MustCompile(`\baudit(?:Err|If)?\(\s*(?:[A-Za-z.]+\s*,\s*)?"([a-z_]+)"`),
		regexp.MustCompile(`AppendEventLogChained(?:If)?\(\s*[A-Za-z.]+\s*,\s*"([a-z_]+)"`),
		regexp.MustCompile(`violation\{"([a-z_]+)"`),
	}
	root := filepath.Join(repoRoot, "src")
	require.NoError(t, filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		b, _ := os.ReadFile(p)
		for _, r := range re {
			for _, m := range r.FindAllStringSubmatch(string(b), -1) {
				names[m[1]] = true
			}
		}
		return nil
	}))
	var out []string
	for n := range names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// TestContract_every_audit_event_is_documented: the proposal and decision records are a contract; an event
// the code writes that docs/audit.md does not list is a record a reader of the chain cannot interpret.
func TestContract_every_audit_event_is_documented(t *testing.T) {
	doc := readDoc(t, "docs/audit.md")
	names := eventNames(t)
	require.Greater(t, len(names), 15, "the scan found too few events: it no longer reads the code")
	for _, n := range names {
		assert.True(t, strings.Contains(doc, "`"+n+"`"), "the audit event %q is written by the code and is not in docs/audit.md", n)
	}
}

// TestContract_every_hook_event_is_documented: the hook bridge registers these events per agent.
func TestContract_every_hook_event_is_documented(t *testing.T) {
	doc := readDoc(t, "docs/adr/0003-hook-bridge.md") + readDoc(t, "docs/compatibility.md")
	for _, ev := range []string{"SessionStart", "PreToolUse", "PostToolUse", "Stop", "BeforeTool", "AfterTool"} {
		assert.True(t, mentions(doc, ev), "the hook event %q is not documented", ev)
	}
}

// TestContract_the_decision_model_interface_is_documented: the questions asked, the fields of an answer and the
// threshold are what an evaluation server must match.
func TestContract_the_decision_model_interface_is_documented(t *testing.T) {
	doc := readDoc(t, "docs/approvals.md")
	for id := range signal.ReviewQuestions {
		assert.True(t, mentions(doc, id), "the question %q is not in docs/approvals.md", id)
	}
	for _, f := range jsonFields(reflect.TypeOf(signal.Question{}), map[reflect.Type]bool{}) {
		assert.True(t, mentions(doc, f), "the question field %q is not documented", f)
	}
	for _, f := range jsonFields(reflect.TypeOf(signal.Answer{}), map[reflect.Type]bool{}) {
		if f == "noul" { // the native name of a boolean's probability, documented as such
			assert.True(t, mentions(doc, "noul"))
			continue
		}
		assert.True(t, mentions(doc, f), "the answer field %q is not documented", f)
	}
	assert.Contains(t, doc, "**0.5**", "the threshold is documented")
}
