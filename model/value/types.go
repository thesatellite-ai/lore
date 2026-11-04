package value

import (
	"encoding/json"
	"math"
	"net/mail"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// Text is free text (valid UTF-8).
type Text struct{ s string }

// TextOf validates s as text.
func TextOf(s string) (Text, error) {
	if !utf8.ValidString(s) {
		return Text{}, errs.New(errs.InvalidUTF8, "value: text is not valid UTF-8")
	}
	return Text{s: s}, nil
}

// Kind implements Value.
func (Text) Kind() Kind { return KindText }

// String returns the text.
func (v Text) String() string { return v.s }

// MarshalJSON implements Value.
func (v Text) MarshalJSON() ([]byte, error) { return canon.String(v.s), nil }
func (Text) sealed()                        {}

// Number is a JSON number kept exactly as written ("1.50" stays "1.50"), so
// an integer never turns into a float on a round trip.
type Number struct{ lit string }

// NumberOf validates lit as a JSON number literal.
func NumberOf(lit string) (Number, error) {
	if canon.KindOf([]byte(lit)) != canon.KindNumber {
		return Number{}, invalid(KindNumber, "not a JSON number")
	}
	c, err := canon.Compact([]byte(lit))
	if err != nil || string(c) != lit {
		return Number{}, invalid(KindNumber, "not a plain number literal")
	}
	return Number{lit: lit}, nil
}

// Int builds a Number from an integer.
func Int(n int64) Number { return Number{lit: strconv.FormatInt(n, 10)} }

// Float builds a Number from a float; NaN and infinities have no JSON form.
func Float(f float64) (Number, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return Number{}, invalid(KindNumber, "not finite")
	}
	return Number{lit: strconv.FormatFloat(f, 'g', -1, 64)}, nil
}

// Float64 converts to float64 (may round very long literals).
func (v Number) Float64() (float64, error) { return strconv.ParseFloat(v.lit, 64) }

// Int64 converts to int64; it fails for fractions and out-of-range values.
func (v Number) Int64() (int64, error) { return strconv.ParseInt(v.lit, 10, 64) }

// Kind implements Value.
func (Number) Kind() Kind { return KindNumber }

// String returns the literal.
func (v Number) String() string { return v.lit }

// MarshalJSON implements Value.
func (v Number) MarshalJSON() ([]byte, error) { return []byte(v.lit), nil }
func (Number) sealed()                        {}

// Bool is true or false.
type Bool struct{ b bool }

// BoolOf wraps b.
func BoolOf(b bool) Bool { return Bool{b: b} }

// Bool returns the wrapped value.
func (v Bool) Bool() bool { return v.b }

// Kind implements Value.
func (Bool) Kind() Kind { return KindBool }

// String returns "true" or "false".
func (v Bool) String() string { return strconv.FormatBool(v.b) }

// MarshalJSON implements Value.
func (v Bool) MarshalJSON() ([]byte, error) { return []byte(v.String()), nil }
func (Bool) sealed()                        {}

// DateLayout is the stored form of a date.
const DateLayout = "2006-01-02"

// DateTimeLayout is the stored form of a datetime: UTC with nine fixed
// fractional digits, the row-file form of v0.1.17 (lsync/codec.go), so
// string order equals time order.
const DateTimeLayout = "2006-01-02T15:04:05.000000000Z"

// Date is a calendar day with no time zone.
type Date struct{ t time.Time }

// DateOf parses YYYY-MM-DD.
func DateOf(s string) (Date, error) {
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		return Date{}, invalid(KindDate, "want YYYY-MM-DD")
	}
	return Date{t: t}, nil
}

// Time returns midnight UTC of the day.
func (v Date) Time() time.Time { return v.t }

// Kind implements Value.
func (Date) Kind() Kind { return KindDate }

// String returns YYYY-MM-DD.
func (v Date) String() string { return v.t.Format(DateLayout) }

// MarshalJSON implements Value.
func (v Date) MarshalJSON() ([]byte, error) { return canon.String(v.String()), nil }
func (Date) sealed()                        {}

// DateTime is an instant, stored in UTC with nanoseconds.
type DateTime struct{ t time.Time }

// DateTimeOf parses an RFC 3339 time (any zone; stored as UTC).
func DateTimeOf(s string) (DateTime, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return DateTime{}, invalid(KindDateTime, "want RFC 3339")
	}
	return DateTimeFrom(t), nil
}

// DateTimeFrom wraps t (converted to UTC; the monotonic reading dropped).
func DateTimeFrom(t time.Time) DateTime { return DateTime{t: t.UTC().Round(0)} }

// Time returns the instant in UTC.
func (v DateTime) Time() time.Time { return v.t }

// Kind implements Value.
func (DateTime) Kind() Kind { return KindDateTime }

// String returns the DateTimeLayout form.
func (v DateTime) String() string { return v.t.Format(DateTimeLayout) }

// MarshalJSON implements Value.
func (v DateTime) MarshalJSON() ([]byte, error) { return canon.String(v.String()), nil }
func (DateTime) sealed()                        {}

// Duration is a length of time, stored in Go's form ("1h30m0s").
type Duration struct{ d time.Duration }

// DurationOf parses a Go duration ("45m", "1h30m", "1.5h").
func DurationOf(s string) (Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return Duration{}, invalid(KindDuration, "want e.g. 45m or 1h30m")
	}
	return Duration{d: d}, nil
}

// DurationFrom wraps d.
func DurationFrom(d time.Duration) Duration { return Duration{d: d} }

// Duration returns the wrapped value.
func (v Duration) Duration() time.Duration { return v.d }

// Kind implements Value.
func (Duration) Kind() Kind { return KindDuration }

// String returns Go's duration text.
func (v Duration) String() string { return v.d.String() }

// MarshalJSON implements Value.
func (v Duration) MarshalJSON() ([]byte, error) { return canon.String(v.String()), nil }
func (Duration) sealed()                        {}

// IP is one IPv4 or IPv6 address.
type IP struct{ a netip.Addr }

// IPOf parses an address ("203.0.113.10", "2001:db8::1"); stored in its
// canonical text form.
func IPOf(s string) (IP, error) {
	a, err := netip.ParseAddr(s)
	if err != nil {
		return IP{}, invalid(KindIP, "not an IP address")
	}
	return IP{a: a}, nil
}

// Addr returns the address.
func (v IP) Addr() netip.Addr { return v.a }

// Kind implements Value.
func (IP) Kind() Kind { return KindIP }

// String returns the canonical address text.
func (v IP) String() string { return v.a.String() }

// MarshalJSON implements Value.
func (v IP) MarshalJSON() ([]byte, error) { return canon.String(v.String()), nil }
func (IP) sealed()                        {}

// CIDR is an address prefix ("10.0.0.0/8").
type CIDR struct{ p netip.Prefix }

// CIDROf parses a prefix.
func CIDROf(s string) (CIDR, error) {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return CIDR{}, invalid(KindCIDR, "not a CIDR prefix")
	}
	return CIDR{p: p}, nil
}

// Prefix returns the prefix.
func (v CIDR) Prefix() netip.Prefix { return v.p }

// Kind implements Value.
func (CIDR) Kind() Kind { return KindCIDR }

// String returns the canonical prefix text.
func (v CIDR) String() string { return v.p.String() }

// MarshalJSON implements Value.
func (v CIDR) MarshalJSON() ([]byte, error) { return canon.String(v.String()), nil }
func (CIDR) sealed()                        {}

// DNS limits (RFC 1035).
const (
	dnsMaxName  = 253
	dnsMaxLabel = 63
)

// DNS is a host name ("db.hub.acme.example"), stored lowercase without a
// trailing dot.
type DNS struct{ s string }

// DNSOf validates a host name: dot-separated labels of letters, digits and
// inner hyphens, a non-numeric last label (so an IP is never a DNS name).
func DNSOf(s string) (DNS, error) {
	n := strings.ToLower(strings.TrimSuffix(s, "."))
	if n == "" || len(n) > dnsMaxName {
		return DNS{}, invalid(KindDNS, "empty or too long")
	}
	labels := strings.Split(n, ".")
	for _, l := range labels {
		if l == "" || len(l) > dnsMaxLabel || l[0] == '-' || l[len(l)-1] == '-' {
			return DNS{}, invalid(KindDNS, "bad label")
		}
		for i := range len(l) {
			c := l[i]
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return DNS{}, invalid(KindDNS, "bad character")
			}
		}
	}
	if _, err := strconv.Atoi(labels[len(labels)-1]); err == nil {
		return DNS{}, invalid(KindDNS, "numeric last label")
	}
	return DNS{s: n}, nil
}

// Kind implements Value.
func (DNS) Kind() Kind { return KindDNS }

// String returns the name.
func (v DNS) String() string { return v.s }

// MarshalJSON implements Value.
func (v DNS) MarshalJSON() ([]byte, error) { return canon.String(v.s), nil }
func (DNS) sealed()                        {}

// URL is an absolute URL without a password.
type URL struct{ s string }

// URLOf validates an absolute URL (scheme plus host or opaque part). A URL
// holding a password is refused with E_URL_CREDENTIALS: credentials never
// enter a stored value; stripping them (W_URL_CREDENTIALS) is the caller's
// job before it builds the value.
func URLOf(s string) (URL, error) {
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || (u.Host == "" && u.Opaque == "") {
		return URL{}, invalid(KindURL, "not an absolute URL")
	}
	if _, has := u.User.Password(); has {
		return URL{}, errs.New(errs.URLUserinfo, "value: the URL holds a password")
	}
	return URL{s: u.String()}, nil
}

// Kind implements Value.
func (URL) Kind() Kind { return KindURL }

// String returns the canonical URL text.
func (v URL) String() string { return v.s }

// MarshalJSON implements Value.
func (v URL) MarshalJSON() ([]byte, error) { return canon.String(v.s), nil }
func (URL) sealed()                        {}

// Email is one bare address ("dev@acme.example"), no display name.
type Email struct{ s string }

// EmailOf validates a bare address.
func EmailOf(s string) (Email, error) {
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || a.Name != "" {
		return Email{}, invalid(KindEmail, "not a bare email address")
	}
	return Email{s: s}, nil
}

// Kind implements Value.
func (Email) Kind() Kind { return KindEmail }

// String returns the address.
func (v Email) String() string { return v.s }

// MarshalJSON implements Value.
func (v Email) MarshalJSON() ([]byte, error) { return canon.String(v.s), nil }
func (Email) sealed()                        {}

// JSON is any JSON value, held as validated canonical compact bytes, never
// as a decoded `any`.
type JSON struct{ raw []byte }

// JSONOf validates raw and stores its canonical compact form.
func JSONOf(raw []byte) (JSON, error) {
	c, err := canon.Compact(raw)
	if err != nil {
		return JSON{}, errs.Wrap(errs.InvalidInput, "value: invalid json", err)
	}
	return JSON{raw: c}, nil
}

// Raw returns a copy of the canonical compact bytes (nil for the zero JSON).
func (v JSON) Raw() []byte { return append([]byte(nil), v.raw...) }

// IsZero reports the zero JSON (no bytes; not the JSON null).
func (v JSON) IsZero() bool { return len(v.raw) == 0 }

// IsObject reports whether the value is a JSON object.
func (v JSON) IsObject() bool { return canon.KindOf(v.raw) == canon.KindObject }

// Kind implements Value.
func (JSON) Kind() Kind { return KindJSON }

// String returns the compact JSON text.
func (v JSON) String() string { return string(v.raw) }

// MarshalJSON implements Value; the zero JSON marshals as null.
func (v JSON) MarshalJSON() ([]byte, error) {
	if v.IsZero() {
		return []byte("null"), nil
	}
	return v.Raw(), nil
}
func (JSON) sealed() {}

// List is an ordered list whose items all have one kind.
type List struct {
	elem  Kind
	items []Value
}

// ListOf builds a list of elem items; an item of another kind (or nil) is
// refused.
func ListOf(elem Kind, items ...Value) (List, error) {
	if _, err := ParseKind(string(elem)); err != nil {
		return List{}, err
	}
	out := make([]Value, len(items))
	for i, it := range items {
		if it == nil || it.Kind() != elem {
			return List{}, invalid(KindList, "item "+strconv.Itoa(i)+" is not "+string(elem))
		}
		out[i] = it
	}
	return List{elem: elem, items: out}, nil
}

// Elem is the kind of every item.
func (v List) Elem() Kind { return v.elem }

// Items returns a copy of the items.
func (v List) Items() []Value { return append([]Value(nil), v.items...) }

// Kind implements Value.
func (List) Kind() Kind { return KindList }

// String joins the items' text with ", ".
func (v List) String() string {
	parts := make([]string, len(v.items))
	for i, it := range v.items {
		parts[i] = it.String()
	}
	return strings.Join(parts, ", ")
}

// MarshalJSON implements Value.
func (v List) MarshalJSON() ([]byte, error) {
	out := []byte{'['}
	for i, it := range v.items {
		if i > 0 {
			out = append(out, ',')
		}
		b, err := it.MarshalJSON()
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
	}
	return append(out, ']'), nil
}
func (List) sealed() {}

// Ref points at another row by its permanent id.
type Ref struct{ id ids.AnyID }

// RefOf wraps a non-zero id.
func RefOf(id ids.AnyID) (Ref, error) {
	if id.IsZero() {
		return Ref{}, invalid(KindRef, "empty id")
	}
	return Ref{id: id}, nil
}

// ID returns the referenced row's id.
func (v Ref) ID() ids.AnyID { return v.id }

// Kind implements Value.
func (Ref) Kind() Kind { return KindRef }

// String returns the id text.
func (v Ref) String() string { return v.id.String() }

// MarshalJSON implements Value.
func (v Ref) MarshalJSON() ([]byte, error) { return canon.String(v.id.String()), nil }
func (Ref) sealed()                        {}

// Null is the absent value of a nullable field.
type Null struct{}

// Kind implements Value.
func (Null) Kind() Kind { return KindNull }

// String returns "".
func (Null) String() string { return "" }

// MarshalJSON implements Value.
func (Null) MarshalJSON() ([]byte, error) { return []byte("null"), nil }
func (Null) sealed()                      {}

// jsonString decodes raw as a JSON string into a typed target.
func jsonString(k Kind, raw []byte) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", invalid(k, "want a JSON string")
	}
	return s, nil
}
