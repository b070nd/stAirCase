// Package diag holds the logs kept for a person to read when something went wrong: the hosted-session log (host/<session>.log) and the
// run's debug log. They are local, never evidence, and follow two rules:
//
//   - They are bounded. A log and its previous file (path.1) never exceed MaxLogBytes together: at half the bound the log moves to
//     path.1 and continues in a fresh file. The oldest output is dropped; each fresh file and the end say how often it rotated and how
//     many bytes were lost.
//   - They hold no secret the agent was handed. Every write is scrubbed line by line, so a value split across two writes is still found.
//
// The authoritative records (audit chain, certificate, ledger) are not kept here, so a full log never costs the run any evidence.
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
	// reserve is room kept back in a file for the marker at its end.
	reserve = 128
	// markerRoom is room for the line a fresh file starts with, whatever the counts in it.
	markerRoom = 128
	// minBound keeps a file's half of the bound larger than its markers.
	minBound = 8 * reserve
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

// Log is a bounded, scrubbed, append-only diagnostic file. Its Write never fails and never blocks the writer on a full log: when
// the file reaches half the bound it becomes the numbered previous file (path.1, replacing an older one) and the log continues in a
// fresh file, so the two together never exceed the bound and the newest output survives.
type Log struct {
	mu        sync.Mutex
	f         *os.File
	path      string
	max       int64
	written   int64 // bytes in the file, the earlier opens' included
	rotations int
	dropped   int64 // bytes lost: what a rotation replaced, and what could not be written at all
	secrets   func() []string
	pending   []byte // the line being written, until it has an end
}

// Open opens (or creates, mode 0600) the log at path to append, bounded at bound bytes in all (the file and its previous file
// together), scrubbing the values secrets returns (which may be nil) from everything written. A log that is already at half of its
// bound is rotated at once, so that a continuation is not blind.
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
	l := &Log{f: f, path: path, max: max(bound, minBound), written: st.Size(), secrets: secrets}
	if l.written >= l.half() {
		l.rotate()
	}
	return l, nil
}

// half is the size at which a file is rotated: two files of it are the bound.
func (l *Log) half() int64 { return l.max / 2 }

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
	if l.rotations > 0 || l.dropped > 0 {
		l.put([]byte(fmt.Sprintf("[staircase: rotated %d times, dropped %d bytes in all]\n", l.rotations, l.dropped)))
	}
	if l.f == nil {
		return nil
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

// emit scrubs b and writes it, rotating the file whenever the room it has is used up. A piece is cut at a line end when it can be,
// so that rotation keeps lines whole. Scrubbing comes first, so a secret that would have been cut by a rotation is already gone.
func (l *Log) emit(b []byte) {
	if len(b) == 0 {
		return
	}
	if l.secrets != nil {
		b = crypto.ScrubBytes(b, l.secrets())
	}
	fresh := false // rotated, and nothing written since: another rotation would gain nothing
	for len(b) > 0 {
		n := int(min(int64(len(b)), max(l.half()-reserve-l.written, 0)))
		if n < len(b) {
			if i := lastNewline(b[:n]); i >= 0 {
				n = i + 1
			} else if !fresh && int64(len(b)) <= l.half()-reserve-markerRoom {
				n = 0 // it fits a fresh file whole: rotate rather than cut it
			} else {
				for n > 0 && !utf8.RuneStart(b[n]) { // a cut inside a character would leave a broken one
					n--
				}
			}
		}
		if n == 0 {
			if fresh {
				l.dropped += int64(len(b))
				return
			}
			l.rotate()
			fresh = true
			continue
		}
		l.put(b[:n])
		b = b[n:]
		fresh = false
		if len(b) > 0 {
			l.rotate()
			fresh = true
		}
	}
}

// put appends b if the file stays within its half of the bound, and counts it dropped if it would not.
func (l *Log) put(b []byte) {
	if l.f == nil || l.written+int64(len(b)) > l.half() {
		l.dropped += int64(len(b))
		return
	}
	n, _ := l.f.Write(b)
	l.written += int64(n)
}

// rotate moves the file to its numbered previous file, replacing the one that was there, and continues in a fresh file whose first
// line says how often the log has rotated and how much it has dropped. What the replaced previous file held is dropped. A file
// longer than half of the bound (a log from before rotation) is cut to its newest half, so the two never exceed the bound.
func (l *Log) rotate() {
	prev := l.path + ".1"
	if st, err := os.Stat(prev); err == nil {
		l.dropped += st.Size()
	}
	if l.f != nil {
		_ = l.f.Close()
		l.f = nil
	}
	if err := os.Rename(l.path, prev); err != nil {
		l.dropped += l.written // the file cannot move: start it again, losing what it held
		_ = os.Remove(l.path)
	} else if cut := l.written - l.half(); cut > 0 {
		l.dropped += cut
		keepTail(prev, l.half())
	}
	l.rotations++
	l.written = 0
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND|os.O_TRUNC, 0o600)
	if err != nil {
		return // the log is lost from here on; the writer is never told
	}
	l.f = f
	l.put([]byte(fmt.Sprintf("[staircase: rotated %d times, dropped %d bytes so far; earlier output is in the .1 file]\n", l.rotations, l.dropped)))
}

// keepTail cuts the file at path to its last keep bytes, from the start of a character.
func keepTail(path string, keep int64) {
	b, err := os.ReadFile(path)
	if err != nil || int64(len(b)) <= keep {
		return
	}
	b = b[int64(len(b))-keep:]
	for len(b) > 0 && !utf8.RuneStart(b[0]) {
		b = b[1:]
	}
	_ = os.WriteFile(path, b, 0o600)
}
