package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/staircase-core/src/internal/gate"
	"github.com/stretchr/testify/assert"
)

// TestPrintGateReport_human_readable verifies that printGateReport writes a
// structured, human-readable report containing the expected header fields,
// column headers, and per-gate result rows.
//
// This test is only possible because printGateReport accepts io.Writer
// (not *os.File). The io.Writer change was made explicitly to enable this.
func TestPrintGateReport_human_readable(t *testing.T) {
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	report := gate.Report{
		CaseID:  42,
		RunAt:   now,
		Overall: gate.StatusPass,
		Summary: gate.Summary{Pass: 2, Warn: 0, Fail: 0, Skip: 0},
		Gates: []gate.Result{
			{
				Name:     "source-path-set",
				Category: "structural",
				Severity: gate.SeverityBlock,
				Status:   gate.StatusPass,
				Message:  "source path configured",
			},
			{
				Name:     "key-file-exists",
				Category: "security",
				Severity: gate.SeverityBlock,
				Status:   gate.StatusPass,
				Message:  "key file found",
			},
		},
	}

	var buf bytes.Buffer
	printGateReport(&buf, report)
	out := buf.String()

	assert.Contains(t, out, "case #42", "report must include case ID")
	assert.Contains(t, out, "2024-01-15", "report must include run date")
	assert.Contains(t, out, "CATEGORY", "report must include column header")
	assert.Contains(t, out, "source-path-set", "report must include gate names")
	assert.Contains(t, out, "key-file-exists")
	assert.Contains(t, out, "structural", "report must include categories")
	assert.Contains(t, out, "security")
	assert.Contains(t, out, "2 pass", "footer must include summary counts")
	assert.Contains(t, out, "All BLOCK gates passed", "PASS overall must include success message")
}

func TestPrintGateReport_blocking_shows_failure_message(t *testing.T) {
	report := gate.Report{
		CaseID:  7,
		RunAt:   time.Now(),
		Overall: gate.StatusFail,
		Summary: gate.Summary{Fail: 1},
		Gates: []gate.Result{
			{
				Name:     "topology-valid",
				Category: "structural",
				Severity: gate.SeverityBlock,
				Status:   gate.StatusFail,
				Message:  "no topology registered",
			},
		},
	}

	var buf bytes.Buffer
	printGateReport(&buf, report)
	out := buf.String()

	assert.Contains(t, out, "topology-valid")
	assert.True(t, strings.Contains(out, "BLOCK failures must be resolved"),
		"blocking report must show failure action message")
}

func TestPrintGateReport_warn_overall(t *testing.T) {
	report := gate.Report{
		CaseID:  3,
		RunAt:   time.Now(),
		Overall: gate.StatusWarn,
		Summary: gate.Summary{Pass: 1, Warn: 1},
		Gates: []gate.Result{
			{Name: "a", Category: "cat", Severity: gate.SeverityBlock, Status: gate.StatusPass, Message: "ok"},
			{Name: "b", Category: "cat", Severity: gate.SeverityWarn, Status: gate.StatusWarn, Message: "advisory"},
		},
	}

	var buf bytes.Buffer
	printGateReport(&buf, report)
	out := buf.String()

	assert.Contains(t, out, "Warnings detected", "WARN overall must show warning message")
}

func TestStatusIcon_known_statuses(t *testing.T) {
	// statusIcon is unexported but its output is visible in printGateReport output.
	// Verify indirectly that icons appear in the rendered report.
	report := gate.Report{
		CaseID:  1,
		RunAt:   time.Now(),
		Overall: gate.StatusPass,
		Summary: gate.Summary{Pass: 1},
		Gates: []gate.Result{
			{Name: "g", Category: "c", Severity: gate.SeverityBlock, Status: gate.StatusPass, Message: "ok"},
		},
	}
	var buf bytes.Buffer
	printGateReport(&buf, report)
	// The ✅ icon must appear at least once (in the PASS row and/or footer).
	assert.Contains(t, buf.String(), "✅")
}
