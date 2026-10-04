package tui

import "strings"

// Safe makes text an agent wrote fit to print on a terminal a person decides on:
// control characters (escape sequences, carriage returns, backspaces, the bell)
// become visible symbols (ESC is ␛) instead of acting on the terminal. Line
// feeds and tabs stay.
func Safe(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r < 0x20:
			return 0x2400 + r // the Unicode "control pictures": ␛ ␍ ␈ ...
		case r == 0x7f:
			return '␡'
		case r >= 0x80 && r <= 0x9f: // C1 controls, such as the 8-bit CSI
			return '\uFFFD'
		}
		return r
	}, s)
}
