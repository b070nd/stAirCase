package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Landlock needs nothing installed: staircase runs itself as a small helper
// (landlockArg) that restricts its own thread and then executes the command,
// which inherits the restriction. Landlock can only grant access, so hiding
// a folder means granting reads on everything around it. Network is cut with
// a seccomp filter: no sockets at all, as with sandbox-exec's deny network*.

const landlockArg = "staircase-sandbox-landlock"

type landlockConfig struct {
	Write []string `json:"write"`
	Hide  []string `json:"hide"`
}

// init runs the helper before anything else in the binary, including tests.
func init() {
	if len(os.Args) > 3 && os.Args[1] == landlockArg {
		runtime.LockOSThread() // Landlock, no_new_privs and seccomp apply to this thread; exec keeps them
		err := landlockExec(os.Args[2], os.Args[3:])
		fmt.Fprintln(os.Stderr, "staircase sandbox:", err)
		os.Exit(126)
	}
}

func landlockABI() int {
	v, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return 0
	}
	return int(v)
}

func landlockWrapper(root, tmp string, hide []string) ([]string, error) {
	if landlockABI() < 1 {
		return nil, errors.New("this kernel has no Landlock")
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cfg, err := json.Marshal(landlockConfig{Write: []string{root, tmp}, Hide: hide})
	if err != nil {
		return nil, err
	}
	return []string{self, landlockArg, string(cfg)}, nil
}

// fsRights are the file-system rights this Landlock version knows.
func fsRights(abi int) uint64 {
	r := uint64(unix.LANDLOCK_ACCESS_FS_MAKE_SYM<<1) - 1 // version 1: execute … make_sym
	if abi >= 2 {
		r |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		r |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	if abi >= 5 {
		r |= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	return r
}

func landlockExec(cfgJSON string, argv []string) error {
	var c landlockConfig
	if err := json.Unmarshal([]byte(cfgJSON), &c); err != nil {
		return err
	}
	all := fsRights(landlockABI())
	attr := unix.LandlockRulesetAttr{Access_fs: all}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return fmt.Errorf("landlock ruleset: %w", errno)
	}
	read := uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR)
	for _, p := range readable(c.Hide) {
		if err := allow(int(fd), p, read); err != nil {
			return err
		}
	}
	for _, p := range append(c.Write, "/dev/null", "/dev/zero", "/dev/full", "/dev/tty", "/dev/ptmx", "/dev/pts") {
		if err := allow(int(fd), p, all); err != nil {
			return err
		}
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("no_new_privs: %w", err)
	}
	if _, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, fd, 0, 0); errno != 0 {
		return fmt.Errorf("landlock restrict: %w", errno)
	}
	if err := noSockets(); err != nil {
		return err
	}
	return unix.Exec(argv[0], argv, os.Environ())
}

// allow grants access beneath path; a path that does not exist is skipped.
func allow(ruleset int, path string, access uint64) error {
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR { // only file rights apply to a file
		access &= unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_READ_FILE |
			unix.LANDLOCK_ACCESS_FS_TRUNCATE | unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	rule := unix.LandlockPathBeneathAttr{Allowed_access: access, Parent_fd: int32(fd)}
	if _, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(ruleset), unix.LANDLOCK_RULE_PATH_BENEATH,
		uintptr(unsafe.Pointer(&rule)), 0, 0, 0); errno != 0 {
		return fmt.Errorf("landlock rule %s: %w", path, errno)
	}
	return nil
}

// noSockets refuses socket() and io_uring (which can open sockets without
// socket()) with EACCES, and every call from another architecture.
func noSockets() error {
	var arch uint32
	switch runtime.GOARCH {
	case "amd64":
		arch = unix.AUDIT_ARCH_X86_64
	case "arm64":
		arch = unix.AUDIT_ARCH_AARCH64
	default:
		return fmt.Errorf("no network filter for %s", runtime.GOARCH)
	}
	deny := uint32(unix.SECCOMP_RET_ERRNO | uint32(unix.EACCES))
	prog := []unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 4}, // seccomp_data.arch
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: 1, K: arch},
		{Code: unix.BPF_RET | unix.BPF_K, K: deny},
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0}, // seccomp_data.nr
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: 3, K: unix.SYS_SOCKET},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: 2, K: unix.SYS_IO_URING_SETUP},
		{Code: unix.BPF_JMP | unix.BPF_JGE | unix.BPF_K, Jt: 1, K: 0x40000000}, // x32 calls on amd64
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW},
		{Code: unix.BPF_RET | unix.BPF_K, K: deny},
	}
	fprog := unix.SockFprog{Len: uint16(len(prog)), Filter: &prog[0]}
	if err := unix.Prctl(unix.PR_SET_SECCOMP, unix.SECCOMP_MODE_FILTER, uintptr(unsafe.Pointer(&fprog)), 0, 0); err != nil {
		return fmt.Errorf("seccomp: %w", err)
	}
	return nil
}
