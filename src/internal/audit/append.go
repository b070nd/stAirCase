// Package audit provides append-only checkpoint writing outside the SQLite
// database so that tamper evidence survives DB-level attacks (CHECK 9.2.2).
package audit

import (
	"fmt"
	"os"
	"slices"
)

// AppendCheckpoint opens path for append (O_APPEND|O_CREATE|O_WRONLY, mode
// 0600) and writes data.  Using O_APPEND guarantees that concurrent or
// repeated calls to this function never overwrite earlier checkpoint records —
// each call adds to the end of the file atomically at the OS level
// (CHECK 9.2.2).
func AppendCheckpoint(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) // CHECK 9.2.2
	if err != nil {
		return fmt.Errorf("open checkpoint: %w", err)
	}
	defer func() { _ = f.Close() }()
	// Append data plus a newline delimiter so multiple exports remain readable
	// as NDJSON when the same file is reused across staircase audit export calls.
	// One write keeps each record whole under O_APPEND; Clip makes append copy
	// instead of writing into the caller's buffer.
	if _, err := f.Write(append(slices.Clip(data), '\n')); err != nil {
		return fmt.Errorf("write checkpoint: %w", err)
	}
	return nil
}
