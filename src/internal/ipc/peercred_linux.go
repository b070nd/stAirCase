//go:build linux

package ipc

import (
	"fmt"
	"net"
	"os"
	"syscall"
)

// checkPeerUID verifies that the process on the other end of a Unix-domain
// socket connection has the same UID as the current process.
//
// On Linux this uses SO_PEERCRED (a kernel-level credential attached to the
// socket by the OS at connect time, not spoofable from userspace).  If the
// peer UID does not match we close the connection immediately — this catches
// a different local user who somehow discovered the socket path despite the
// 0600 file-permission guard.
//
// Non-UDS connections (e.g. Windows TCP fallback) return nil unchanged.
func checkPeerUID(conn net.Conn) error {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return nil
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return fmt.Errorf("peer cred: SyscallConn: %w", err)
	}
	var ucred *syscall.Ucred
	var sysErr error
	_ = raw.Control(func(fd uintptr) {
		ucred, sysErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	})
	if sysErr != nil {
		return fmt.Errorf("peer cred: SO_PEERCRED: %w", sysErr)
	}
	if ucred.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("peer cred: peer UID %d != server UID %d — connection rejected", ucred.Uid, os.Getuid())
	}
	return nil
}
