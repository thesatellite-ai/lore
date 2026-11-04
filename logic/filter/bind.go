package filter

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
)

// Field is what the caller knows about one key: its value kind and, for
// an enum, its allowed values in their declared order.
type Field struct {
	// Kind is the field's value kind; "" reads as text.
	Kind value.Kind
	// Elem is the element kind of a list (Kind == value.KindList).
	Elem value.Kind
	// Values are the allowed values in declared order. Set, = and != values
	// must be one of them (FLAG2: an unknown value is an invalid argument,
	// not an empty result) and > >= < <= compare by position, so
	// `priority>=high` keeps high and urgent.
	Values []string
}

// Resolver maps a term's key to its field, or fails (an unknown key is the
// caller's error, worded with the caller's field list).
type Resolver func(Key) (Field, error)

// Getter returns a row's values for a key: one element for a scalar, the
// elements of a list, none when the row has no value.
type Getter func(Key) []string

// Matcher is a Filter checked against its fields; the zero Matcher matches
// every row.
type Matcher struct {
	terms []bound
}

// bound is a term with its field's comparison kind.
type bound struct {
	Term
	field Field
	kind  value.Kind
}

// Bind checks every term against resolve: the key must exist, an ordering
// operator needs an ordered kind, and every value must parse as the kind.
func (f Filter) Bind(resolve Resolver) (Matcher, error) {
	var m Matcher
	for _, t := range f.Terms {
		fd, err := resolve(t.Key)
		if err != nil {
			return Matcher{}, err
		}
		b := bound{Term: t, field: fd, kind: scalarKind(fd)}
		if err := b.check(); err != nil {
			return Matcher{}, err
		}
		m.terms = append(m.terms, b)
	}
	return m, nil
}

// Keys lists the keys the matcher tests, in term order.
func (m Matcher) Keys() []Key {
	out := make([]Key, 0, len(m.terms))
	for _, t := range m.terms {
		out = append(out, t.Key)
	}
	return out
}

// Match reports whether a row passes every term (AND).
func (m Matcher) Match(get Getter) bool {
	for _, t := range m.terms {
		if !t.match(get(t.Key)) {
			return false
		}
	}
	return true
}

// scalarKind is the kind one value of the field compares as.
func scalarKind(f Field) value.Kind {
	k := f.Kind
	if k == value.KindList {
		k = f.Elem
	}
	if k == "" {
		k = value.KindText
	}
	return k
}

// ordered reports a kind the ordering operators accept.
func (b bound) ordered() bool {
	switch b.kind {
	case value.KindNumber, value.KindDate, value.KindDateTime, value.KindDuration:
		return true
	default:
		return len(b.field.Values) > 0
	}
}

func (b bound) check() error {
	switch b.Op {
	case OpHas, OpMissing, OpFold:
		return nil
	case OpGt, OpGe, OpLt, OpLe:
		if !b.ordered() {
			// The E_META_OP wording of CLI_REFERENCE 2.2; the code is
			// E_INVALID_INPUT until E_META_OP is registered in model/errs.
			return invalid(quoted(string(b.Op)) + " compares numbers, dates and datetimes; " + b.Key.String() + " is " + string(b.kind) + ". Use = , != or ~=").
				WithHint("nothing written")
		}
		if len(b.Values) != 1 {
			return invalid("--where " + quoted(b.Raw) + ": " + string(b.Op) + " takes one value")
		}
	default:
		// = and != check every value below.
	}
	for _, v := range b.Values {
		if err := b.checkValue(v); err != nil {
			return err
		}
	}
	return nil
}

// checkValue refuses a value the field can never hold.
func (b bound) checkValue(v string) error {
	if vs := b.field.Values; len(vs) > 0 {
		if !slices.Contains(vs, v) {
			return invalid(b.Key.String() + " " + quoted(v) + " is not one of " + strings.Join(vs, ", "))
		}
		return nil
	}
	if _, ok := parse(b.kind, v); !ok {
		return invalid("--where " + quoted(b.Raw) + ": " + quoted(v) + " is not a " + string(b.kind) + hintFor(b.kind))
	}
	return nil
}

// hintFor names the accepted form of a kind's values.
func hintFor(k value.Kind) string {
	switch k {
	case value.KindDate:
		return " (want YYYY-MM-DD)"
	case value.KindDateTime:
		return " (want YYYY-MM-DD or RFC 3339)"
	case value.KindBool:
		return " (want true or false)"
	case value.KindDuration:
		return " (want a duration such as 90m or 2h)"
	default:
		return ""
	}
}

func (b bound) match(have []string) bool {
	switch b.Op {
	case OpHas:
		return len(have) > 0
	case OpMissing:
		return len(have) == 0
	case OpNe:
		return !b.anyEqual(have)
	case OpEq:
		return b.anyEqual(have)
	case OpFold:
		for _, h := range have {
			for _, w := range b.Values {
				if strings.Contains(strings.ToLower(h), strings.ToLower(w)) {
					return true
				}
			}
		}
		return false
	default:
		// the ordering operators, below.
	}
	for _, h := range have {
		if c, ok := b.compare(h, b.Values[0]); ok && b.holds(c) {
			return true
		}
	}
	return false
}

// holds applies an ordering operator to a comparison result.
func (b bound) holds(c int) bool {
	switch b.Op {
	case OpGt:
		return c > 0
	case OpGe:
		return c >= 0
	case OpLt:
		return c < 0
	case OpLe:
		return c <= 0
	default:
		return false
	}
}

// anyEqual reports a row value equal to one of the term's values.
func (b bound) anyEqual(have []string) bool {
	for _, h := range have {
		for _, w := range b.Values {
			if c, ok := b.compare(h, w); ok && c == 0 {
				return true
			}
		}
	}
	return false
}

// compare orders a row value against a filter value by the field kind;
// ok is false when the row value does not parse (it then matches nothing).
func (b bound) compare(have, want string) (int, bool) {
	if vs := b.field.Values; len(vs) > 0 {
		hi, wi := slices.Index(vs, have), slices.Index(vs, want)
		if hi < 0 {
			return 0, false
		}
		return cmp.Compare(hi, wi), true
	}
	switch b.kind {
	case value.KindNumber, value.KindDuration:
		h, ok1 := parse(b.kind, have)
		w, ok2 := parse(b.kind, want)
		if !ok1 || !ok2 {
			return 0, false
		}
		return cmp.Compare(h.num, w.num), true
	case value.KindDate, value.KindDateTime:
		h, ok1 := parse(value.KindDateTime, have)
		w, ok2 := parse(value.KindDateTime, want)
		if !ok1 || !ok2 {
			return 0, false
		}
		ht := h.t
		// A date-only filter value on a datetime compares calendar days:
		// `due_at=2026-12-01` keeps every time of that day.
		if w.dateOnly || b.kind == value.KindDate {
			ht = day(ht)
		}
		return ht.Compare(w.t), true
	default:
		// text, bool and every text-like kind compare as written.
		return strings.Compare(have, want), true
	}
}

// parsed is one value read as its kind.
type parsed struct {
	num      float64
	t        time.Time
	dateOnly bool
}

// parse reads v as kind k; ok is false when it does not fit.
func parse(k value.Kind, v string) (parsed, bool) {
	switch k {
	case value.KindNumber:
		n, err := strconv.ParseFloat(v, 64)
		return parsed{num: n}, err == nil
	case value.KindDuration:
		d, err := time.ParseDuration(v)
		return parsed{num: float64(d)}, err == nil
	case value.KindDate, value.KindDateTime:
		if t, err := time.Parse(value.DateLayout, v); err == nil {
			return parsed{t: t, dateOnly: true}, true
		}
		if k == value.KindDate {
			return parsed{}, false
		}
		t, err := time.Parse(time.RFC3339Nano, v)
		return parsed{t: t.UTC()}, err == nil
	case value.KindBool:
		return parsed{}, v == "true" || v == "false"
	default:
		// text and text-like kinds take any value.
		return parsed{}, true
	}
}

// day is t's calendar day (midnight UTC).
func day(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

// UnknownKey is the standard error for a key no field or metadata key
// carries, listing the keys there are (callers' Resolvers return it).
func UnknownKey(k Key, known []string) error {
	return errs.New(errs.InvalidInput, "--where: unknown field "+k.String()+" (fields: "+strings.Join(known, ", ")+")")
}
