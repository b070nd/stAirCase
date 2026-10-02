// export_test.go - compiled only during `go test`.
// Exposes unexported symbols to the black-box test package (package tui_test).
package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/b070nd/stAirCase/src/internal/domain"
)

// NewYieldModel exposes the unexported constructor for white-box testing.
var NewYieldModel = newYieldModel

// YieldModel is a type alias so tests can perform type assertions.
type YieldModel = yieldModel

// RespOf extracts the IpcYieldResponse from a yieldModel returned by Update.
func RespOf(m interface{}) domain.YieldResponse {
	return m.(yieldModel).resp
}

// StateOf extracts the yieldState integer so tests can assert on state
// transitions without importing the unexported type directly.
func StateOf(m interface{}) int {
	return int(m.(yieldModel).state)
}

// RunYieldFor runs the dialog with the given program options (a fake input and
// output instead of a terminal) and reports whether the operator interrupted.
func RunYieldFor(ctx context.Context, req domain.YieldRequest, opts ...tea.ProgramOption) domain.YieldResponse {
	return runYield(ctx, req, opts...)
}

// OnInterrupt replaces what an operator's Ctrl-C does to the process, for the test's length.
func OnInterrupt(f func()) (restore func()) {
	old := interrupt
	interrupt = f
	return func() { interrupt = old }
}
