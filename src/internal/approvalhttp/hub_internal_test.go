package approvalhttp

import "testing"

// TestLoopback: a session's key is sent only to this machine.
func TestLoopback(t *testing.T) {
	for raw, want := range map[string]bool{
		"http://127.0.0.1:8765": true, "http://localhost:1": true, "http://[::1]:9": true,
		"http://192.0.2.1:9": false, "http://evil.example": false, "https://127.0.0.1:1": false,
		"http://127.0.0.1.evil.example": false, "ftp://127.0.0.1": false, "": false,
	} {
		if got := loopback(raw); got != want {
			t.Errorf("loopback(%q) = %v, want %v", raw, got, want)
		}
	}
}
