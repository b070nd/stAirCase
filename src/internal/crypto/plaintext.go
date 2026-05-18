package crypto

// Plaintext is a byte-slice wrapper that:
//   - never leaks its value through fmt.Sprintf / log / json (String returns
//     "<redacted>" — satisfying CHECK 4.2.2),
//   - provides Zero() to overwrite the backing bytes when the value is no
//     longer needed, reducing the window in which plaintext lives in memory.
//
// Use NewPlaintext to construct, Value() to read, Zero() to erase.
type Plaintext struct {
	val []byte
}

// NewPlaintext wraps val in a Plaintext.
// The value is copied into a freshly-allocated byte slice so it can be zeroed.
func NewPlaintext(val string) Plaintext {
	b := make([]byte, len(val))
	copy(b, val)
	return Plaintext{val: b}
}

// Value returns the plaintext as a string.
// Callers must ensure the result is not stored in log-able data structures.
func (p Plaintext) Value() string {
	return string(p.val)
}

// String satisfies fmt.Stringer and always returns "<redacted>" so that
// fmt.Sprintf("%v", pt) or any logger that calls .String() never emits the
// secret value.
func (p Plaintext) String() string {
	return "<redacted>"
}

// Zero overwrites every byte of the backing slice with zeros, minimising the
// window in which the plaintext lives in memory.  After Zero(), Value()
// returns an empty string.  Calling Zero() twice is safe.
func (p *Plaintext) Zero() {
	for i := range p.val {
		p.val[i] = 0
	}
	p.val = nil
}
