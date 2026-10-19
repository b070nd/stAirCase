// Package diag holds the logs stAirCase keeps for a person to read when something went wrong: the hosted-session log
// (host/<session>.log) and the run's debug log. They are local, never evidence, and they are held to two rules.
//
//   - They are bounded. A log stops growing at its bound (MaxLogBytes), says so where it stopped, and at its end says how much
//     it dropped. The bound covers the markers too: the file never exceeds it.
//   - They hold no secret the agent was handed. Every write is scrubbed of the delivered values, line by line, so a value cut in
//     two by two writes is still found.
//
// What a log drops is lost on purpose; the run's authoritative records (the audit chain, the certificate, the ledger) are not
// kept here and are never short of anything because a log is full.
package diag

import (
	"fmt"
	"io"
	"os"
	"sync"
	"unicode/utf8"

	"github.com/b070nd/stAirCase/src/internal/crypto"
)

// MaxLogBytes is the bound of every log stAirCase keeps: 8 MiB, which holds many hours of a hosted session's output and a long
// run's audited messages (each at most 64 KiB), and is small enough that a runaway agent cannot fill a disk through it. A variable
// only so that a test can lower it.
var MaxLogBytes int64 = 8 << 20

const (
	// reserve is room kept back for the two markers, so that a full log can still say it is full.
	reserve = 256
	// maxLine is how much of a line without an end is held back before it is written.
	maxLine = 64 << 10
)

var (
	deliveredMu sync.Mutex
	delivered   []string
)

// Deliver records secret values handed to an agent, for every log of this process to scrub. A hosted session is one process, so
// its host log, which no run owns, learns from here what its run delivered.
func Deliver(values ...string) {
	deliveredMu.Lock()
	defer deliveredMu.Unlock()
	delivered = append(delivered, values...)
}

// Delivered returns the values recorded by Deliver.
func Delivered() []string {
	deliveredMu.Lock()
	defer deliveredMu.Unlock()
	return append([]string(nil), delivered...)
}

// Log is a bounded, scrubbed, append-only diagnostic file. Its Write never fails and never blocks the writer on a full log: what
// does not fit is counted and dropped.
type Log struct {
	mu      sync.Mutex
	f       *os.File
	max     int64
	written int64 // bytes in the file, the earlier opens' included
	dropped int64
	marked  bool
	secrets func() []string
	pending []byte // the line being written, until it has an end
}

// Open opens (or creates, mode 0600) the log at path to append, bounded at bound bytes in all, scrubbing the values secrets returns
// (which may be nil) from everything written.
func Open(path string, bound int64, secrets func() []string) (*Log, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Log{f: f, max: max(bound, 2*reserve), written: st.Size(), secrets: secrets}, nil
}

// Write takes p into the log. It reports all of p as written, whatever became of it.
func (l *Log) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pending = append(l.pending, p...)
	l.drain(false)
	return len(p), nil
}

// Close writes what is left of an unfinished line, says how much was dropped, and closes the file.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.drain(true)
	if l.dropped > 0 {
		l.put([]byte(fmt.Sprintf("[staircase: dropped %d bytes in all]\n", l.dropped)))
	}
	return l.f.Close()
}

// Redirect makes os.Stdout write into the log, and returns the function that gives os.Stdout back after the last of it has been
// written. A program whose own output must not reach a stream it shares with another sends it here.
func (l *Log) Redirect() (restore func()) {
	r, w, err := os.Pipe()
	if err != nil {
		return func() {}
	}
	prev := os.Stdout
	os.Stdout = w
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(l, r)
	}()
	return func() {
		os.Stdout = prev
		_ = w.Close()
		<-done
		_ = r.Close()
	}
}

// drain writes the complete lines held (and, at the end, the rest). A line that has no end after maxLine bytes is written but for
// its last bytes, as many as the longest secret less one: those may be the start of a secret the next write finishes.
func (l *Log) drain(final bool) {
	if i := lastNewline(l.pending); i >= 0 {
		l.emit(l.pending[:i+1])
		l.pending = append([]byte(nil), l.pending[i+1:]...)
	}
	switch {
	case final:
		l.emit(l.pending)
		l.pending = nil
	case len(l.pending) > maxLine:
		hold := 0
		if l.secrets != nil {
			for _, s := range l.secrets() {
				hold = max(hold, len(s)-1)
			}
		}
		hold = min(hold, len(l.pending))
		l.emit(l.pending[:len(l.pending)-hold])
		l.pending = append([]byte(nil), l.pending[len(l.pending)-hold:]...)
	}
}

func lastNewline(b []byte) int {
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] == '\n' {
			return i
		}
	}
	return -1
}

// emit scrubs b and writes as much of it as the bound leaves room for, counting the rest as dropped.
func (l *Log) emit(b []byte) {
	if len(b) == 0 {
		return
	}
	if l.secrets != nil {
		b = crypto.ScrubBytes(b, l.secrets())
	}
	room := max(l.max-reserve-l.written, 0)
	if int64(len(b)) <= room {
		l.put(b)
		return
	}
	keep := int(room)
	for keep > 0 && !utf8.RuneStart(b[keep]) { // a cut inside a character would leave a broken one
		keep--
	}
	l.put(b[:keep])
	l.dropped += int64(len(b) - keep)
	if !l.marked {
		l.marked = true
		l.put([]byte(fmt.Sprintf("\n[staircase: this log reached its bound of %d bytes; output after this point is dropped]\n", l.max)))
	}
}

// put appends b if the file stays within its bound, and counts it dropped if it would not.
func (l *Log) put(b []byte) {
	if l.written+int64(len(b)) > l.max {
		l.dropped += int64(len(b))
		return
	}
	n, _ := l.f.Write(b)
	l.written += int64(n)
}
