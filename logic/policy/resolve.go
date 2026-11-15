package policy

import (
	"maps"
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
	// unknown are the rows whose key this build does not know: written by
	// a newer lore (forward compatible, devco:V-16), they are ignored.
	unknown []Entry
}

// Unknown returns the policy rows Resolve ignored because this build does
// not know their key (a newer lore wrote them), in resolution order. A
// caller says so once ("ignored; a newer lore knows it") instead of
// failing every read and write in the folder (devco:V-16).
func (s Set) Unknown() []Entry { return slices.Clone(s.unknown) }

// Defaults is the Set with no rows at any level.
func Defaults() Set {
	s, err := Resolve(nil)
	if err != nil {
		panic("policy: defaults do not resolve: " + err.Error()) // programmer error in Keys
	}
	return s
}

// Resolve combines entries from every level: per key the stricter value
// wins (OQ24). It fails closed with E_CONFIG_INVALID, naming the row, on a
// value of the wrong shape for a key this build knows: a broken policy row
// must stop writes, never be skipped. A key this build does not know is a
// newer lore's (branches.* reached 0.1.18 builds without branch layers,
// devco:V-16): it is ignored and listed in Set.Unknown, so a teammate one
// release behind keeps reading and writing; in a namespace that guards
// writes (guardNamespaces) it still fails closed, with the upgrade hint. Behaviour a newer key needs on
// writes is guarded by the folder's format features (formatgate), not by
// the policy row. `lore policy set` still refuses a key it does not know.
// Levels must be valid.
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
			if guardsWrites(e.Key) {
				return Set{}, errs.New(errs.ConfigInvalid, "policy row "+e.Source.String()+" sets "+string(e.Key)+", a key this lore does not know; its namespace guards writes (who may write, secrets, deletes), so lore stops rather than skip it").
					WithHint("lore upgrade gets the lore that knows it (a teammate on a newer lore set it); or remove the row: lore policy unset --key " + string(e.Key) + " --yes")
			}
			s.unknown = append(s.unknown, e)
			continue
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

// guardNamespaces are the policy namespaces whose keys refuse or guard a
// write (who may write, secret handling, deletes): an unknown key there
// may be a newer lore's stricter rule, so it fails closed instead of being
// ignored (devco:V-16: "unless the key is strict and a write depends on
// it"). Every other namespace (branches, tasks, context, …) is forward
// compatible.
var guardNamespaces = []string{"agents", "secrets", "delete"}

// guardsWrites reports whether k is in a guard namespace.
func guardsWrites(k Key) bool {
	ns, _, _ := strings.Cut(string(k), ".")
	return slices.Contains(guardNamespaces, ns)
}

// stricter folds entry e (levels arrive nearest first) into cur.
func stricter(d KeyDef, cur Effective, e Entry) (Effective, error) {
	take := Effective{Key: d.Key, Value: e.Value, Level: e.Level, Source: e.Source}
	if cur.IsDefault && e.Level == LevelMachine && cur.Value != nil {
		// FQ11: a machine value never "sets" a key; it only tightens, the
		// built-in default included (a looser local value is ignored).
		next, err := stricter(d, Effective{Key: d.Key, Value: cur.Value, Level: LevelMachine}, e)
		if err != nil || next.Value == nil || value.Equal(next.Value, cur.Value) {
			return cur, err
		}
		next.Level, next.IsDefault = LevelMachine, false
		return next, nil
	}
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
	case ShapeWeight, ShapeText:
		// OV15B-URG, FB-TASK-3: nearest wins; levels arrive nearest first,
		// so the first level that set it (taken above) stays.
	case ShapeSet:
		u, err := union(cur.Value, e.Value)
		if err != nil {
			return Effective{}, err
		}
		cur.Value = u
	case ShapeAllow:
		// RQ2: levels intersect; a farther level that removes items wins
		// and is named as the source (so policy set reports the widening).
		i, err := intersect(cur.Value, e.Value)
		if err != nil {
			return Effective{}, err
		}
		if len(i.Items()) < listLen(cur.Value) {
			return Effective{Key: d.Key, Value: i, Level: e.Level, Source: e.Source}, nil
		}
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
	case ShapeWeight:
		h, ok := hundredthsOf(v)
		if !ok || h > d.Ceiling*hundredth || h < -d.Ceiling*hundredth {
			return errs.New(errs.ConfigInvalid, "value must be a number with at most two decimals"+weightRange(d.Ceiling))
		}
	case ShapeText:
		if t, ok := v.(value.Text); !ok || strings.TrimSpace(t.String()) == "" {
			return errs.New(errs.ConfigInvalid, "value must be non-empty text")
		}
	case ShapeSet:
		if l, ok := v.(value.List); !ok || (len(l.Items()) > 0 && l.Elem() != value.KindText) {
			return errs.New(errs.ConfigInvalid, "value must be a list of text")
		}
	case ShapeAllow:
		l, ok := v.(value.List)
		if !ok || (len(l.Items()) > 0 && l.Elem() != value.KindText) {
			return errs.New(errs.ConfigInvalid, "value must be a list of: "+strings.Join(d.Choices, ", "))
		}
		for _, it := range l.Items() {
			if !slices.Contains(d.Choices, it.String()) {
				return errs.New(errs.ConfigInvalid, "value must be a list of: "+strings.Join(d.Choices, ", "))
			}
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

// Weight returns a ShapeWeight key's value in integer hundredths (12.5 →
// 1250); ok is false when no level sets it (use the built-in default).
func (s Set) Weight(k Key) (int64, bool) {
	e, ok := s.m[k]
	if !ok || e.Value == nil || e.IsDefault {
		return 0, false
	}
	return hundredthsOf(e.Value)
}

// hundredth is how many hundredths make one point.
const hundredth = 100

// hundredthsOf reads a number with at most two decimals as hundredths,
// by its decimal text (no float), so "0.1" is exactly 10.
func hundredthsOf(v value.Value) (int64, bool) {
	n, ok := v.(value.Number)
	if !ok {
		return 0, false
	}
	s := n.String()
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > 2 || whole == "" || strings.ContainsAny(whole+frac, "eE+-") {
		return 0, false
	}
	frac += strings.Repeat("0", 2-len(frac))
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, false
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, false
	}
	h := w*hundredth + f
	if neg {
		h = -h
	}
	return h, true
}

// weightRange is the " from -c to c" suffix of a weight error.
func weightRange(c int64) string {
	if c <= 0 {
		return ""
	}
	n := strconv.FormatInt(c, 10)
	return " from -" + n + " to " + n
}

// Flag returns a ShapeFlag key's value; ok is false when it is unset (and
// has no default) or k is not a flag key.
func (s Set) Flag(k Key) (bool, bool) {
	e, ok := s.m[k]
	if !ok || e.Value == nil {
		return false, false
	}
	b, isBool := e.Value.(value.Bool)
	if !isBool {
		return false, false
	}
	return b.Bool(), true
}

// Text returns a ShapeText key's value; ok is false when it is unset (and
// has no default) or k is not a text key.
func (s Set) Text(k Key) (string, bool) {
	e, ok := s.m[k]
	if !ok || e.Value == nil {
		return "", false
	}
	t, isText := e.Value.(value.Text)
	if !isText {
		return "", false
	}
	return t.String(), true
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

// intersect is the sorted distinct items present in both text lists.
func intersect(a, b value.Value) (value.List, error) {
	in := map[string]bool{}
	if l, ok := b.(value.List); ok {
		for _, it := range l.Items() {
			in[it.String()] = true
		}
	}
	seen := map[string]value.Value{}
	if l, ok := a.(value.List); ok {
		for _, it := range l.Items() {
			if in[it.String()] {
				seen[it.String()] = it
			}
		}
	}
	names := slices.Sorted(maps.Keys(seen))
	items := make([]value.Value, len(names))
	for i, n := range names {
		items[i] = seen[n]
	}
	return value.ListOf(value.KindText, items...)
}

// listLen is the item count of a list value (0 for anything else).
func listLen(v value.Value) int {
	if l, ok := v.(value.List); ok {
		return len(l.Items())
	}
	return 0
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
