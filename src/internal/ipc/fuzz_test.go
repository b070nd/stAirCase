package ipc_test

// FuzzEnvelope fuzzes the IPC message framing layer — the code path that reads
// a raw JSON-Lines byte slice, extracts the "type" field, and routes to a
// message handler. This is the primary attack surface for a malicious Python
// process attempting to crash or corrupt the Go server.
//
// Run with:
//
//	go test -fuzz=FuzzEnvelope -fuzztime=60s ./internal/ipc/...
//
// Any panic or data race found here is a HIGH finding (CHECK 3.6.2).

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/b070nd/staircase-core/src/internal/ipc"
	"github.com/b070nd/staircase-core/src/internal/persistence"
)

// newFuzzEnv is a lightweight server setup for the fuzz target.
// It avoids t.Cleanup (not available in fuzz targets) and instead returns a
// cancel function the caller must invoke.
func newFuzzEnv(f *testing.F) (srv *ipc.Server, cancel context.CancelFunc, addr string) {
	f.Helper()
	wsDir := f.TempDir()

	db, err := persistence.InitDB(wsDir)
	if err != nil {
		f.Fatal(err)
	}
	store := persistence.NewStore(db)

	v, _ := store.CreateVendor("V")
	p, _ := store.CreateProject(v.ID, "P", "")
	c, _ := store.CreateCase(p.ID)
	topo, _ := store.CreateSwarmTopology(p.ID, "sup", "memory", "langgraph")
	run, _ := store.CreateRun(c.ID, topo.Version, "main")

	sockDir, err := os.MkdirTemp("", "ipc-fuzz-*")
	if err != nil {
		f.Fatal(err)
	}

	token := "fuzz-token-32-bytes-padded-here!" // fixed token for fuzz stability
	aesKey := make([]byte, 32)
	socketPath := filepath.Join(sockDir, "f.sock")

	srv = ipc.NewServer(socketPath, run.ID, 0, token, store, aesKey, true /* allowShellExec */)
	ctx, cancelFn := context.WithCancel(context.Background())
	if err := srv.Start(ctx); err != nil {
		cancelFn()
		f.Fatal(err)
	}
	return srv, cancelFn, srv.ListenAddr()
}

func FuzzEnvelope(f *testing.F) {
	// Seed corpus — representative message types that the server handles.
	seeds := []string{
		`{"type":"heartbeat"}`,
		`{"type":"state_emit","active_agent":"coder","state":{}}`,
		`{"type":"yield_request","agent_name":"p","action_type":"read","reasoning_trace":"t","confidence_score":0.9}`,
		`{"type":"secret_request","key_name":"KEY"}`,
		`{"type":"unknown_kind"}`,
		`not-json`,
		`{}`,
		`{"type":""}`,
		`{"type":"heartbeat","extra_field":"should_be_ignored"}`,
		`{"type":"state_emit","active_agent":"` + string(make([]byte, 512)) + `"}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	srv, cancel, addr := newFuzzEnv(f)
	defer cancel()

	// Resolve the network once (unix vs tcp).
	network := "unix"
	if len(addr) > 0 && addr[0] != '/' && addr[0] != '.' {
		network = "tcp"
	}

	// Perform the auth handshake once and obtain a live connection.
	// Each fuzz iteration reuses a fresh connection to avoid state bleed.
	const token = "fuzz-token-32-bytes-padded-here!"

	f.Fuzz(func(t *testing.T, msg []byte) {
		// Ignore inputs that exceed the scanner buffer to avoid ErrTooLong noise.
		if len(msg) > 4*64*1024 {
			return
		}

		var conn net.Conn
		var err error
		for i := 0; i < 10; i++ {
			conn, err = net.Dial(network, addr)
			if err == nil {
				break
			}
			time.Sleep(2 * time.Millisecond)
		}
		if err != nil {
			return // server may be under load; skip iteration
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(500 * time.Millisecond))

		// Auth handshake.
		fmt.Fprintf(conn, `{"type":"auth","token":%q}`+"\n", token)
		sc := bufio.NewScanner(conn)
		if !sc.Scan() {
			return // auth timeout or error; not a crash
		}

		// Send the fuzz payload as a single JSON-Lines message.
		conn.Write(append(msg, '\n'))

		// Attempt to read one more response (server may or may not reply).
		sc.Scan() //nolint:errcheck

		_ = srv // keep reference alive
	})
}
