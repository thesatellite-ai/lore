package secretmgr

import (
	"fmt"
	"strconv"
)

// Mask is what every printing path of a Secret shows (10.2).
const Mask = "••••••"

// Secret holds a revealed value. String, GoString, Format and MarshalJSON
// all print Mask, so a Secret logged or wrapped into an error by accident
// leaks nothing. Only Expose returns the value.
type Secret struct{ v []byte }

// NewSecret wraps b (the slice is copied).
func NewSecret(b []byte) Secret { return Secret{v: append([]byte(nil), b...)} }

// Expose returns the value. Only the --reveal printer calls it (a source
// test in ops/settings pins that).
func (s Secret) Expose() string { return string(s.v) }

// Empty reports whether the manager answered with nothing.
func (s Secret) Empty() bool { return len(s.v) == 0 }

// String implements fmt.Stringer with the mask.
func (s Secret) String() string { return Mask }

// GoString implements fmt.GoStringer with the mask.
func (s Secret) GoString() string { return "secretmgr.Secret(" + Mask + ")" }

// Format implements fmt.Formatter: every verb prints the mask.
func (s Secret) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(Mask)) }

// MarshalJSON prints the mask as a JSON string.
func (s Secret) MarshalJSON() ([]byte, error) { return []byte(strconv.Quote(Mask)), nil }
