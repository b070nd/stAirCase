//go:build windows

package runtime_test

import (
	"context"
	"strings"
	"testing"

	"github.com/b070nd/staircase-core/src/internal/runtime"
)

// TestLaunchPython_windows_returns_unsupported verifies that the Windows stub
// returns a clear "not supported" error rather than panicking or returning nil.
// This is the contract the orchestrator relies on to avoid attempting a run on Windows.
func TestLaunchPython_windows_returns_unsupported(t *testing.T) {
	_, err := runtime.LaunchPython(context.Background(), t.TempDir(), nil, "", "")
	if err == nil {
		t.Fatal("LaunchPython must return an error on Windows")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "windows") {
		t.Errorf("error should mention Windows, got: %v", err)
	}
}

// TestBootstrapToken_windows_returns_error verifies the Windows stub returns an error.
func TestBootstrapToken_windows_returns_error(t *testing.T) {
	tok, err := runtime.BootstrapToken()
	if err == nil {
		t.Fatal("BootstrapToken must return an error on Windows")
	}
	if tok != "" {
		t.Errorf("expected empty token on Windows, got %q", tok)
	}
}
