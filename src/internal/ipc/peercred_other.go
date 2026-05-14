//go:build !linux

package ipc

import "net"

// checkPeerUID is a no-op on non-Linux platforms.
//
// On macOS the combination of the 0600 socket permission and the session
// bearer-token handshake provides equivalent protection without requiring
// platform-specific syscalls.  On Windows the IPC falls back to a TCP
// loopback socket (no UDS), so peer credentials are not applicable.
//
// A future improvement for macOS would be getpeereid(3) via
// golang.org/x/sys/unix, but that requires promoting the dependency from
// indirect to direct.
func checkPeerUID(_ net.Conn) error {
	return nil
}
