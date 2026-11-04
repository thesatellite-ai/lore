package policy

import (
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// Policies row field names (schema.Builtin, table policies).
const (
	fieldKey   = "key"
	fieldValue = "value"
)

// Entry is one policies row as seen from a repo: its level, key and value.
type Entry struct {
	Level  Level
	Key    Key
	Value  value.Value
	Source ids.AnyID // the pol_ row
}

// EntryFromRow reads a live policies row found at level. The value may be
// stored as a json field or decoded by inference (a plain JSON string, bool,
// number or array); both read the same. Tombstones are refused: the caller
// skips them.
func EntryFromRow(level Level, r row.Row) (Entry, error) {
	if r.Table != schema.TablePolicies || r.Deleted {
		return Entry{}, errs.New(errs.InvalidInput, "policy: not a live policies row")
	}
	k, ok := r.Fields[fieldKey].(value.Text)
	if !ok {
		return Entry{}, configErr(r.ID, "has no text key")
	}
	v := r.Fields[fieldValue]
	if j, isJSON := v.(value.JSON); isJSON {
		inferred, err := value.Infer(j.Raw())
		if err != nil {
			return Entry{}, configErr(r.ID, "value is not JSON")
		}
		v = inferred
	}
	return Entry{Level: level, Key: Key(k.String()), Value: v, Source: r.ID}, nil
}

// Effective is the resolved value of one key.
type Effective struct {
	Key   Key
	Value value.Value // nil when unset and the key has no default
	// Level and Source name where the value comes from: the level of the
	// strictest entry (nearest on a tie); for a ShapeSet key, the nearest
	// level that contributed. Both are zero for a default.
	Level     Level
	Source    ids.AnyID
	IsDefault bool
}

// Set is the effective value of every key.
type Set struct {
	m map[Key]Effective
}

// Defaults is the Set with no rows at any level.
func Defaults() Set {
	s, err := Resolve(nil)
	if err != nil {
		panic("policy: defaults do not resolve: " + err.Error()) // programmer error in Keys
	}
	return s
}

// Resolve combines entries from every level: per key the stricter value
// wins (OQ24). It fails closed with E_CONFIG_INVALID, naming the row, on an
// unknown key or a value of the wrong shape: a broken policy row must stop
// writes, never be skipped. Levels must be valid.
func Resolve(entries []Entry) (Set, error) {
	es := slices.Clone(entries)
	byLevel(es, func(e Entry) Level { return e.Level })
	s := Set{m: map[Key]Effective{}}
	for _, d := range Keys() {
		s.m[d.Key] = Effective{Key: d.Key, Value: d.Default, IsDefault: true}
	}
	for _, e := range es {
		if !e.Level.Valid() {
			return Set{}, configErr(e.Source, "has an unknown level "+string(e.Level))
		}
		d, ok := Lookup(e.Key)
		if !ok {
			return Set{}, configErr(e.Source, "has an unknown key "+string(e.Key))
		}
		if err := check(d, e.Value); err != nil {
			return Set{}, configErr(e.Source, err.Error())
		}
		cur := s.m[d.Key]
		next, err := stricter(d, cur, e)
		if err != nil {
			return Set{}, configErr(e.Source, err.Error())
		}
		s.m[d.Key] = next
	}
	return s, nil
}

// stricter folds entry e (levels arrive nearest first) into cur.
func stricter(d KeyDef, cur Effective, e Entry) (Effective, error) {
	take := Effective{Key: d.Key, Value: e.Value, Level: e.Level, Source: e.Source}
	if cur.IsDefault {
		// The default is the baseline only where no level speaks: the first
		// level that sets a key replaces it, even with a looser value (that
		// is what setting it means); after that, levels only tighten.
		return take, nil
	}
	switch d.Shape {
	case ShapeRank:
		if slices.Index(d.Choices, e.Value.String()) < slices.Index(d.Choices, cur.Value.String()) {
			return take, nil
		}
	case ShapeFlag:
		if e.Value.String() == strconv.FormatBool(d.Strict) && cur.Value.String() != e.Value.String() {
			return take, nil
		}
	case ShapeLimit:
		n, _ := intOf(e.Value) // checked by check
		c, _ := intOf(cur.Value)
		if n < c {
			return take, nil
		}
	case ShapeSet:
		u, err := union(cur.Value, e.Value)
		if err != nil {
			return Effective{}, err
		}
		cur.Value = u
	}
	return cur, nil
}

// check validates a value against its key's shape.
func check(d KeyDef, v value.Value) error {
	if v == nil {
		return errs.New(errs.ConfigInvalid, "has no value")
	}
	switch d.Shape {
	case ShapeRank:
		if t, ok := v.(value.Text); !ok || !slices.Contains(d.Choices, t.String()) {
			return errs.New(errs.ConfigInvalid, "value must be one of "+strings.Join(d.Choices, ", "))
		}
	case ShapeFlag:
		if _, ok := v.(value.Bool); !ok {
			return errs.New(errs.ConfigInvalid, "value must be true or false")
		}
	case ShapeLimit:
		n, ok := intOf(v)
		if !ok || n < 0 || (d.Ceiling > 0 && n > d.Ceiling) {
			return errs.New(errs.ConfigInvalid, "value must be a whole number from 0"+ceiling(d.Ceiling))
		}
	case ShapeSet:
		if l, ok := v.(value.List); !ok || (len(l.Items()) > 0 && l.Elem() != value.KindText) {
			return errs.New(errs.ConfigInvalid, "value must be a list of text")
		}
	}
	return nil
}

// Get returns the effective value of k; ok is false for an unknown key.
func (s Set) Get(k Key) (Effective, bool) {
	e, ok := s.m[k]
	return e, ok
}

// OnDetect returns secrets.on_detect (ask when the Set is the zero value,
// the decided default).
func (s Set) OnDetect() OnDetect {
	if e, ok := s.m[KeyOnDetect]; ok && e.Value != nil {
		return OnDetect(e.Value.String())
	}
	return OnDetectAsk
}

// Limit returns a ShapeLimit key's value; ok is false when it is unset (no
// limit) or k is not a limit key.
func (s Set) Limit(k Key) (int64, bool) {
	e, ok := s.m[k]
	if !ok || e.Value == nil {
		return 0, false
	}
	return intOf(e.Value)
}

// Strings returns a ShapeSet key's items, sorted.
func (s Set) Strings(k Key) []string {
	e, ok := s.m[k]
	if !ok {
		return nil
	}
	l, isList := e.Value.(value.List)
	if !isList {
		return nil
	}
	out := make([]string, 0, len(l.Items()))
	for _, it := range l.Items() {
		out = append(out, it.String())
	}
	slices.Sort(out)
	return out
}

// intOf reads a whole number.
func intOf(v value.Value) (int64, bool) {
	n, ok := v.(value.Number)
	if !ok {
		return 0, false
	}
	i, err := n.Int64()
	return i, err == nil
}

// union merges two text lists into a sorted list of distinct items.
func union(a, b value.Value) (value.Value, error) {
	seen := map[string]value.Value{}
	for _, v := range []value.Value{a, b} {
		if l, ok := v.(value.List); ok {
			for _, it := range l.Items() {
				seen[it.String()] = it
			}
		}
	}
	names := make([]string, 0, len(seen))
	for k := range seen {
		names = append(names, k)
	}
	slices.Sort(names)
	items := make([]value.Value, len(names))
	for i, n := range names {
		items[i] = seen[n]
	}
	return value.ListOf(value.KindText, items...)
}

func ceiling(c int64) string {
	if c == 0 {
		return ""
	}
	return " to " + strconv.FormatInt(c, 10)
}

// configErr names the offending row; the value itself is never repeated.
func configErr(id ids.AnyID, msg string) error {
	return errs.New(errs.ConfigInvalid, "policy row "+id.String()+" "+msg)
}
