//go:build windows

package tui

// raiseInterrupt does nothing on Windows: the change is rejected and the run
// goes on to its next step; stop it from the console as usual.
func raiseInterrupt() {}
