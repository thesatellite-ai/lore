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

// Short is the read-back text: RFC 3339 in UTC with only the fractional
// digits the instant has ("2026-11-07T08:00:00Z", "…:07.123Z"). JSON and
// the stored form keep DateTimeLayout's nine fixed digits (string order
// equals time order there).
func (v DateTime) Short() string { return v.t.Format(time.RFC3339Nano) }

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

// Day-sized units of the duration input grammar (RES 3.1, --remind-before):
// d = 24 h, w = 7 d. Months and years are refused because their length
// varies. Only whole numbers take them (30d, 2w), never a mix (1d12h).
const (
	day  = 24 * time.Hour
	week = 7 * day
)

// dayUnits maps a day-sized suffix to its length.
var dayUnits = map[byte]time.Duration{'d': day, 'w': week}

// DurationForm names the accepted input forms of DurationInputOf, for
// refusals (INTEG-QUESTIONS-10: a refusal names the way forward).
const DurationForm = "e.g. 30d, 2w or 1h30m"

// DurationInputOf parses a duration as a person types it on the CLI: a
// whole number of days or weeks (30d, 2w) or a Go duration (45m, 1h30m,
// 1.5h). It is the one grammar of a duration field value, its --default
// and --min/--max, and --remind-before (#22b). The value is stored in Go's
// form ("720h0m0s"), so every reader of stored rows keeps working.
// DurationOf stays Go-only: task time entries refuse a day unit on purpose
// (D13, OQ18), and stored JSON is always Go form.
func DurationInputOf(s string) (Duration, error) {
	if s != "" {
		if unit, ok := dayUnits[s[len(s)-1]]; ok {
			n, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
			if err != nil || n < 0 || n > math.MaxInt64/int64(unit) {
				return Duration{}, invalid(KindDuration, "want "+DurationForm)
			}
			return Duration{d: time.Duration(n) * unit}, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return Duration{}, invalid(KindDuration, "want "+DurationForm)
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

// Short is the read-back text: Go's form without zero trailing units
// ("1h30m", "720h", "1h0m5s"), so the text shows only the precision the
// value has. JSON and the stored form keep String's full form.
func (v Duration) Short() string {
	s := v.d.String()
	if v.d == 0 {
		return s
	}
	if t, ok := strings.CutSuffix(s, "m0s"); ok {
		s = t + "m"
	}
	if t, ok := strings.CutSuffix(s, "h0m"); ok {
		s = t + "h"
	}
	return s
}

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

// URL is an absolute URL without credentials.
type URL struct {
	s string
	// stripped: URLOf removed user info from the input (D24, 14.2 rows
	// 12-14); the engine turns it into W_URL_CREDENTIALS. Not part of the
	// value: two URLs with the same text are equal values.
	stripped bool
}

// URLOf validates an absolute URL (scheme plus host or opaque part) and
// strips credentials instead of refusing them (URL CREDENTIALS FIX, D24,
// SQ13): an ssh URL keeps its login user and loses a password, every other
// scheme loses its whole user part (over http a bare user name is often a
// token). Stripped reports that something was removed, never what.
func URLOf(s string) (URL, error) {
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || (u.Host == "" && u.Opaque == "") {
		return URL{}, invalid(KindURL, "not an absolute URL")
	}
	stripped := false
	if u.User != nil {
		switch strings.ToLower(u.Scheme) {
		case "ssh", "git+ssh", "ssh+git":
			if _, has := u.User.Password(); has {
				u.User, stripped = url.User(u.User.Username()), true
			}
		default:
			u.User, stripped = nil, true
		}
	}
	if !stripped {
		return URL{s: u.String()}, nil
	}
	return URL{s: u.String(), stripped: true}, nil
}

// Stripped reports whether URLOf removed user info from its input.
func (v URL) Stripped() bool { return v.stripped }

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
