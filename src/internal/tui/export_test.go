// export_test.go — compiled only during `go test`.
// Exposes unexported symbols to the black-box test package (package tui_test).
package tui

import "github.com/b070nd/staircase-core/src/internal/ipc"

// NewYieldModel exposes the unexported constructor for white-box testing.
var NewYieldModel = newYieldModel

// YieldModel is a type alias so tests can perform type assertions.
type YieldModel = yieldModel

// RespOf extracts the IpcYieldResponse from a yieldModel returned by Update.
func RespOf(m interface{}) ipc.IpcYieldResponse {
	return m.(yieldModel).resp
}

// StateOf extracts the yieldState integer so tests can assert on state
// transitions without importing the unexported type directly.
func StateOf(m interface{}) int {
	return int(m.(yieldModel).state)
}
