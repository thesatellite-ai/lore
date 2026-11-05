package meta

import (
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
)

// Rules are the everyday checks a key definition may carry (METADATA
// 3.19): a pattern (RE2) on text values, a min/max bound on numbers, dates
// and datetimes, and deprecation. A violation warns on a normal key and is
// refused on a strict key (E_META_VALUE); the caller decides which, this
// package only finds the violation.
type Rules struct {
	Pattern    string
	Min, Max   string
	Deprecated bool
	ReplacedBy string
	// Sensitive masks the value in Violation's message (3.20: a warning or
	// an E_META_VALUE refusal never quotes a sensitive value). It checks
	// nothing itself, so Empty ignores it.
	Sensitive bool
}

// Empty reports rules that check nothing.
func (r Rules) Empty() bool { return r.Pattern == "" && r.Min == "" && r.Max == "" && !r.Deprecated }

// CheckRuleDefs validates the rule definitions themselves for type t (the
// `meta edit --pattern/--min/--max` input): a pattern must compile as RE2,
// a bound must parse as the key's (element) type, and only numbers, dates
// and datetimes take bounds.
func CheckRuleDefs(t Type, r Rules) error {
	if r.Pattern != "" {
		if _, err := regexp.Compile(r.Pattern); err != nil {
			return errs.Wrap(errs.InvalidInput, "meta: --pattern is not a valid RE2 expression", err)
		}
	}
	k := elemKind(t)
	for _, b := range []string{r.Min, r.Max} {
		if b == "" {
			continue
		}
		if !ordered(k) {
			return errs.New(errs.InvalidInput, "meta: --min/--max apply to number, date, datetime and duration keys, not "+t.String())
		}
		if _, err := bound(k, b); err != nil {
			return errs.Wrap(errs.InvalidInput, "meta: bound "+quote(b)+" is not a "+string(k), err)
		}
	}
	if r.Min != "" && r.Max != "" {
		lo, _ := bound(k, r.Min)
		hi, _ := bound(k, r.Max)
		if lo.cmp(hi) > 0 {
			return errs.New(errs.InvalidInput, "meta: --min "+r.Min+" is above --max "+r.Max)
		}
	}
	return nil
}

// Violation returns why the coerced value raw (canonical JSON) of key name
// breaks r, or "" when it fits. Each element of a list is checked. A broken
// rule definition (a stored pattern that does not compile) is an error.
func Violation(name string, t Type, raw []byte, r Rules) (string, error) {
	var re *regexp.Regexp
	if r.Pattern != "" {
		var err error
		if re, err = regexp.Compile(r.Pattern); err != nil {
			return "", errs.Wrap(errs.ConfigInvalid, "meta: the pattern of "+name+" does not compile", err)
		}
	}
	k := elemKind(t)
	shown := func(s string, quoted bool) string {
		switch {
		case r.Sensitive:
			return Show(Entry{Sensitive: true})
		case quoted:
			return fmt.Sprintf("%q", s)
		}
		return s
	}
	for _, s := range Strings(raw) {
		if re != nil && (k == value.KindText || k == value.KindEmail || k == value.KindURL || k == value.KindDNS) && !re.MatchString(s) {
			return fmt.Sprintf("%s: %s does not match the pattern %s", name, shown(s, true), r.Pattern), nil
		}
		if !ordered(k) || (r.Min == "" && r.Max == "") {
			continue
		}
		v, err := bound(k, s)
		if err != nil {
			continue // a value of another type is the type check's to report
		}
		if r.Min != "" {
			if lo, berr := bound(k, r.Min); berr == nil && v.cmp(lo) < 0 {
				return fmt.Sprintf("%s: %s is below the minimum %s", name, shown(s, false), r.Min), nil
			}
		}
		if r.Max != "" {
			if hi, berr := bound(k, r.Max); berr == nil && v.cmp(hi) > 0 {
				return fmt.Sprintf("%s: %s is above the maximum %s", name, shown(s, false), r.Max), nil
			}
		}
	}
	return "", nil
}

// DeprecatedNote is the warning a write of a deprecated key gets (3.19:
// new writes warn, refused on strict keys).
func DeprecatedNote(name string, r Rules) string {
	if !r.Deprecated {
		return ""
	}
	if r.ReplacedBy != "" {
		return name + " is deprecated; use " + r.ReplacedBy
	}
	return name + " is deprecated"
}

// MissingRequired lists the names in required (key name → tables it is
// required on) that table needs and have lacks, sorted (3.19
// E_META_REQUIRED, checked on add only).
func MissingRequired(table string, required map[string][]string, have []string) []string {
	var out []string
	for name, tables := range required {
		if slices.Contains(tables, table) && !slices.Contains(have, name) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// Strings is a stored value as plain strings: a string unquoted, a list's
// items, any other scalar as its JSON text (filters, export, lint).
func Strings(raw []byte) []string {
	switch canon.KindOf(raw) {
	case canon.KindString:
		if s, err := unquote(raw); err == nil {
			return []string{s}
		}
	case canon.KindArray:
		items, err := canon.Elements(raw)
		if err != nil {
			return nil
		}
		var out []string
		for _, it := range items {
			out = append(out, Strings(it)...)
		}
		return out
	case canon.KindInvalid, canon.KindNull:
		return nil
	case canon.KindBool, canon.KindNumber, canon.KindObject:
	}
	return []string{string(raw)}
}

// Names is an entry's values for display and filters: the allowed-value
// names (v_name) when the key has them, the values otherwise.
func Names(e Entry) []string {
	if len(e.VName) > 0 {
		return Strings(e.VName)
	}
	return Strings(e.V)
}

// elemKind is the kind one value of t has.
func elemKind(t Type) value.Kind {
	if t.Kind == value.KindList {
		return elemOf(t)
	}
	return t.Kind
}

// ordered reports the kinds min/max apply to (3.19).
func ordered(k value.Kind) bool {
	return k == value.KindNumber || k == value.KindDate || k == value.KindDateTime || k == value.KindDuration // RES 3.1: duration bounds (OV32)
}

// orderedValue is a parsed bound or value.
type orderedValue struct {
	n *big.Rat
	t time.Time
}

func (a orderedValue) cmp(b orderedValue) int {
	if a.n != nil && b.n != nil {
		return a.n.Cmp(b.n)
	}
	return a.t.Compare(b.t)
}

// bound parses s as a value of kind k for comparison.
func bound(k value.Kind, s string) (orderedValue, error) {
	s = strings.TrimSpace(s)
	switch k {
	case value.KindNumber:
		r, ok := new(big.Rat).SetString(s)
		if !ok {
			return orderedValue{}, errs.New(errs.InvalidInput, "not a number")
		}
		return orderedValue{n: r}, nil
	case value.KindDate:
		t, err := time.Parse(time.DateOnly, s)
		return orderedValue{t: t}, err
	case value.KindDateTime:
		t, err := time.Parse(time.RFC3339Nano, s)
		return orderedValue{t: t}, err
	case value.KindDuration: // compared as nanoseconds
		d, err := time.ParseDuration(s)
		if err != nil {
			return orderedValue{}, err
		}
		return orderedValue{n: big.NewRat(int64(d), 1)}, nil
	case value.KindText, value.KindBool, value.KindIP, value.KindCIDR, value.KindDNS, value.KindURL,
		value.KindEmail, value.KindRef, value.KindSecretRef, value.KindJSON, value.KindList, value.KindNull:
	}
	return orderedValue{}, errs.New(errs.InvalidInput, "not an ordered type")
}
