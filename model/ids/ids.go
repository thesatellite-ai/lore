// Package ids is lore's permanent row id: "<3 lowercase letters>_<32 hex of a
// UUIDv7>", the same text form v0.1.17 writes (saas/pkg/aicoder/ids).
//
// Why typed: an ID[Memory] can never be passed where an ID[Actor] is
// expected, so a wrong-table id is a compile error instead of an E_NOT_FOUND
// at run time. AnyID is the untyped form for envelope code that handles rows
// of every table.
//
// Invariant: a non-zero ID or AnyID always holds a valid id (format, a
// registered prefix, UUID version 7). The zero value is "no id" and encodes
// as the empty string.
package ids

import (
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Prefix is the 3-letter table tag at the front of an id.
type Prefix string

// Tag is implemented by the empty marker types below; each names the prefix
// of its table, so ID[T] knows its prefix without a value.
type Tag interface {
	Prefix() Prefix
}

// Length facts of the text form.
const (
	prefixLen = 3
	hexLen    = 32
	idLen     = prefixLen + 1 + hexLen
	sep       = '_'
	uuidV7    = 7
)

// ID is an id of the table tagged T.
type ID[T Tag] struct {
	s string
}

// New returns a fresh id for T. It fails only when the host has no working
// random source.
func New[T Tag]() (ID[T], error) {
	var t T
	s, err := newString(t.Prefix())
	if err != nil {
		return ID[T]{}, err
	}
	return ID[T]{s: s}, nil
}

// Parse validates s as an id of T's table.
func Parse[T Tag](s string) (ID[T], error) {
	var t T
	if err := validate(s, t.Prefix()); err != nil {
		return ID[T]{}, err
	}
	return ID[T]{s: s}, nil
}

// String returns the text form ("" for the zero ID).
func (id ID[T]) String() string { return id.s }

// IsZero reports the "no id" value.
func (id ID[T]) IsZero() bool { return id.s == "" }

// Any returns the untyped form.
func (id ID[T]) Any() AnyID { return AnyID(id) }

// Time returns the creation instant embedded in the UUIDv7 (millisecond
// precision). The zero ID gives the zero time.
func (id ID[T]) Time() time.Time { return embeddedTime(id.s) }

// MarshalText encodes the text form; the zero ID encodes as "".
func (id ID[T]) MarshalText() ([]byte, error) { return []byte(id.s), nil }

// UnmarshalText decodes and validates; "" decodes to the zero ID so optional
// id fields round-trip through JSON.
func (id *ID[T]) UnmarshalText(b []byte) error {
	if len(b) == 0 {
		*id = ID[T]{}
		return nil
	}
	v, err := Parse[T](string(b))
	if err != nil {
		return err
	}
	*id = v
	return nil
}

// AnyID is an id of any registered table.
type AnyID struct {
	s string
}

// ParseAny validates s as an id with any registered prefix.
func ParseAny(s string) (AnyID, error) {
	if err := validate(s, ""); err != nil {
		return AnyID{}, err
	}
	return AnyID{s: s}, nil
}

// String returns the text form ("" for the zero AnyID).
func (id AnyID) String() string { return id.s }

// IsZero reports the "no id" value.
func (id AnyID) IsZero() bool { return id.s == "" }

// Prefix returns the id's table prefix ("" for the zero AnyID).
func (id AnyID) Prefix() Prefix {
	if id.s == "" {
		return ""
	}
	return Prefix(id.s[:prefixLen])
}

// Time returns the creation instant embedded in the UUIDv7.
func (id AnyID) Time() time.Time { return embeddedTime(id.s) }

// As narrows to a typed id; it fails when the prefix is not T's.
func As[T Tag](id AnyID) (ID[T], error) { return Parse[T](id.s) }

// MarshalText encodes the text form; the zero AnyID encodes as "".
func (id AnyID) MarshalText() ([]byte, error) { return []byte(id.s), nil }

// UnmarshalText decodes and validates; "" decodes to the zero AnyID.
func (id *AnyID) UnmarshalText(b []byte) error {
	if len(b) == 0 {
		*id = AnyID{}
		return nil
	}
	v, err := ParseAny(string(b))
	if err != nil {
		return err
	}
	*id = v
	return nil
}

// Valid reports whether p is 3 lowercase ASCII letters (not whether it is
// registered; see Registered).
func (p Prefix) Valid() bool {
	if len(p) != prefixLen {
		return false
	}
	for i := range len(p) {
		if p[i] < 'a' || p[i] > 'z' {
			return false
		}
	}
	return true
}

// newString makes the text form of a fresh id with prefix p.
func newString(p Prefix) (string, error) {
	if !p.Registered() {
		return "", errs.New(errs.InvalidID, "ids: unregistered prefix "+string(p))
	}
	u, err := uuid.NewV7()
	if err != nil {
		return "", errs.Wrap(errs.Internal, "ids: no random source for a new id", err)
	}
	return string(p) + string(sep) + strings.ReplaceAll(u.String(), "-", ""), nil
}

// validate checks the text form; want "" accepts any registered prefix.
func validate(s string, want Prefix) error {
	if len(s) != idLen || s[prefixLen] != sep {
		return errs.New(errs.InvalidID, "ids: "+quote(s)+" is not <prefix>_<32 hex>")
	}
	p := Prefix(s[:prefixLen])
	if !p.Valid() || !p.Registered() {
		return errs.New(errs.InvalidID, "ids: "+quote(s)+" has no registered prefix")
	}
	if want != "" && p != want {
		return errs.New(errs.InvalidID, "ids: "+quote(s)+" is not a "+string(want)+"_ id")
	}
	if _, err := parseUUID(s); err != nil {
		return err
	}
	return nil
}

// parseUUID rebuilds the UUID from the hex tail and checks it is version 7.
func parseUUID(s string) (uuid.UUID, error) {
	h := s[prefixLen+1:]
	for i := range len(h) {
		c := h[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return uuid.Nil, errs.New(errs.InvalidID, "ids: "+quote(s)+" tail is not lowercase hex")
		}
	}
	u, err := uuid.Parse(h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32])
	if err != nil {
		return uuid.Nil, errs.Wrap(errs.InvalidID, "ids: "+quote(s)+" tail is not a uuid", err)
	}
	if u.Version() != uuidV7 {
		return uuid.Nil, errs.New(errs.InvalidID, "ids: "+quote(s)+" is not a UUIDv7")
	}
	return u, nil
}

// embeddedTime reads the UUIDv7 timestamp of a valid id ("" → zero time).
func embeddedTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	u, err := parseUUID(s)
	if err != nil {
		return time.Time{} // unreachable: s was validated when the id was built
	}
	sec, nsec := u.Time().UnixTime()
	return time.Unix(sec, nsec).UTC()
}

// maxQuoted bounds how much of a bad input an error message repeats, so a
// pasted megabyte (or a secret pasted by mistake) is not echoed back whole.
const maxQuoted = 48

// quote renders s for an error message, truncated to maxQuoted bytes.
func quote(s string) string {
	if len(s) > maxQuoted {
		return strconv.Quote(s[:maxQuoted]) + "…"
	}
	return strconv.Quote(s)
}
