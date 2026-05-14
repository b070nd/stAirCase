// export_test.go — exposes unexported monitor helpers for whitebox testing.
package monitor

// ExportedFormatElapsed exposes the unexported formatElapsed function.
var ExportedFormatElapsed = formatElapsed

// ExportedFormatTokens exposes the unexported formatTokens function.
var ExportedFormatTokens = formatTokens
