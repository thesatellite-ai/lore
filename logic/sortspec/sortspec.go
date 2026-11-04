// Package sortspec is the one `--sort` grammar (XQ10, CLI_REFERENCE 4.5):
//
//	sort := key [ ':asc' | ':desc' ]   ; a plain key sorts ascending
//
// XQ10 replaced the old `--sort [-]<key>`: `-key` is refused with the hint
// "use <key>:desc" so a v0.1.17 habit fails loudly instead of sorting
// ascending by a key named "-key". Repeated --sort flags are tie-breakers,
// first flag first.
//
// Layer L1: pure. Parse reads the flags, Check validates the keys against
// the caller's list, Compare orders two rows through the caller's typed
// Values. A missing value sorts last in both directions, so `due_at:desc`
// and `due_at` both put undated rows at the end.
package sortspec

import (
	"cmp"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Direction words (XQ10: `:asc|:desc` is the documented form).
const (
	sep  = ":"
	asc  = "asc"
	desc = "desc"
	// oldDesc is the v0.1.17 descending prefix, refused (XQ10).
	oldDesc = "-"
)

// Spec is one sort key.
type Spec struct {
	Key  string
	Desc bool
}

// String is the spec in its documented form (`key:asc` / `key:desc`).
func (s Spec) String() string {
	if s.Desc {
		return s.Key + sep + desc
	}
	return s.Key + sep + asc
}

// Parse reads the values of repeated --sort flags.
func Parse(raw []string) ([]Spec, error) {
	out := make([]Spec, 0, len(raw))
	for _, r := range raw {
		s, err := parseOne(r)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func parseOne(r string) (Spec, error) {
	if strings.HasPrefix(r, oldDesc) {
		k := strings.TrimPrefix(r, oldDesc)
		// XQ10: one sort grammar; the `-key` form is gone.
		return Spec{}, errs.New(errs.InvalidInput, "--sort "+quoted(r)+": a leading - is not a sort direction").
			WithHint("use " + k + sep + desc)
	}
	key, dir, has := strings.Cut(r, sep)
	if key == "" {
		return Spec{}, errs.New(errs.InvalidInput, "--sort "+quoted(r)+": no key; want <key>, <key>:asc or <key>:desc")
	}
	if !validKey(key) {
		return Spec{}, errs.New(errs.InvalidInput, "--sort "+quoted(r)+": a key is lowercase ASCII (a-z, 0-9, _, .)")
	}
	switch {
	case !has, dir == asc:
		return Spec{Key: key}, nil
	case dir == desc:
		return Spec{Key: key, Desc: true}, nil
	default:
		return Spec{}, errs.New(errs.InvalidInput, "--sort "+quoted(r)+": the direction is asc or desc").
			WithHint("use " + key + sep + asc + " or " + key + sep + desc)
	}
}

// validKey reports a key of the filter alphabet (logic/filter): starts
// with a lowercase letter, then letters, digits, '_' and '.'.
func validKey(k string) bool {
	if k[0] < 'a' || k[0] > 'z' {
		return false
	}
	for i := 1; i < len(k); i++ {
		c := k[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' && c != '.' {
			return false
		}
	}
	return true
}

// Check refuses a key the caller cannot sort by, listing the ones it can.
func Check(specs []Spec, known []string) error {
	for _, s := range specs {
		found := false
		for _, k := range known {
			if k == s.Key {
				found = true
				break
			}
		}
		if !found {
			return errs.New(errs.InvalidInput, "--sort: unknown key "+s.Key+" (keys: "+strings.Join(known, ", ")+")")
		}
	}
	return nil
}

// valueKind tells Value's arms apart.
type valueKind int

const (
	kindMissing valueKind = iota
	kindText
	kindNumber
	kindTime
)

// Value is one sortable value: Text, Number, Time or Missing. Values of
// different arms compare by arm order (a caller types a key consistently).
type Value struct {
	kind valueKind
	text string
	num  float64
	t    time.Time
}

// Missing is the value of a row without the key; it sorts last.
var Missing = Value{}

// Text sorts by byte order (case kept, as `=` compares).
func Text(s string) Value {
	if s == "" {
		return Missing
	}
	return Value{kind: kindText, text: s}
}

// Number sorts numerically.
func Number(n float64) Value { return Value{kind: kindNumber, num: n} }

// Rank sorts an enum by its declared position (priority low < urgent).
func Rank(i int) Value { return Value{kind: kindNumber, num: float64(i)} }

// Time sorts by instant; the zero time is Missing.
func Time(t time.Time) Value {
	if t.IsZero() {
		return Missing
	}
	return Value{kind: kindTime, t: t}
}

// Compare orders rows a and b by specs (first spec first); get returns a
// row's value for a key. 0 means equal under every spec (callers use a
// stable sort to keep their default order for ties).
func Compare(specs []Spec, a, b func(key string) Value) int {
	for _, s := range specs {
		va, vb := a(s.Key), b(s.Key)
		// Missing sorts last whatever the direction.
		switch {
		case va.kind == kindMissing && vb.kind == kindMissing:
			continue
		case va.kind == kindMissing:
			return 1
		case vb.kind == kindMissing:
			return -1
		}
		c := compareValues(va, vb)
		if s.Desc {
			c = -c
		}
		if c != 0 {
			return c
		}
	}
	return 0
}

func compareValues(a, b Value) int {
	if a.kind != b.kind {
		return cmp.Compare(a.kind, b.kind)
	}
	switch a.kind {
	case kindNumber:
		return cmp.Compare(a.num, b.num)
	case kindTime:
		return a.t.Compare(b.t)
	case kindText:
		return strings.Compare(a.text, b.text)
	default:
		return 0
	}
}

func quoted(s string) string { return `"` + s + `"` }
