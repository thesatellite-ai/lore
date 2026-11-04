package metafilter

import (
	"bytes"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
)

// Key is one visible key definition (a live meta_keys row).
type Key struct {
	ID    ids.ID[ids.MetaKey]
	Name  string
	Scope string
	Type  meta.Type
	// Values are the allowed values (name and label) of a key with values:.
	Values []Value
	// Retired keys (merged_into set) never match a bare name.
	Retired bool
}

// Value is one allowed value of a key.
type Value struct{ Name, Label string }

// Sort directions of --sort <key>[:asc|:desc] (XQ10).
const (
	sortAsc  = ":asc"
	sortDesc = ":desc"
)

// cond is one filter term resolved against one definition.
type cond struct {
	term  meta.Term
	key   Key
	path  []string // fields inside an object value (deploy.region)
	want  [][]byte // canonical JSON values to compare with (= != ~=)
	bound *ordered // > >= < <=
}

// clause is one --meta argument: its conditions over every matching
// definition (a row matches the clause when any definition matches).
type clause struct {
	arg   string
	conds []cond
}

// Plan is a compiled filter set. The zero Plan keeps every row.
type Plan struct {
	clauses []clause
	sortBy  []cond
	desc    bool
	// Columns are the filtered (and sorted) key names, in order, for the
	// table output (3.7: each filtered key becomes a column).
	Columns []string
}

// Empty reports a plan with no filter and no sort.
func (p *Plan) Empty() bool { return p == nil || (len(p.clauses) == 0 && len(p.sortBy) == 0) }

// Compile parses filters (the grammar of 3.6), place (--place: a scope
// label such as project:acme-hub, or a level word repo|project|org) and
// sort (a key name, optional :asc|:desc; a leading "meta." is accepted)
// against keys. warnings are the "no metadata key" lines; a filter on an
// unknown name matches nothing.
func Compile(filters []string, place, sort string, keys []Key) (*Plan, []string, error) {
	p := &Plan{}
	var warnings []string
	visible := placeKeys(keys, strings.TrimSpace(place))
	for _, f := range filters {
		t, err := meta.Parse(strings.TrimSpace(f))
		if err != nil {
			return nil, nil, err
		}
		if t.Path != "" {
			return nil, nil, errs.New(errs.MetaOp, "meta: a json path filter ($) is not supported on list yet: "+strconv.Quote(f)).
				WithHint("use lore sql for json paths")
		}
		cl := clause{arg: f}
		matches := resolve(visible, t)
		if len(matches) == 0 {
			warnings = append(warnings, unknownWarning(t.Name(), keys))
		}
		for _, m := range matches {
			c, cerr := compileCond(t, m.key, m.path)
			if cerr != nil {
				return nil, nil, cerr
			}
			cl.conds = append(cl.conds, c)
		}
		p.clauses = append(p.clauses, cl)
		p.addColumn(t.Name())
	}
	if s := strings.TrimSpace(sort); s != "" {
		s = strings.TrimPrefix(s, "meta.")
		switch {
		case strings.HasSuffix(s, sortDesc):
			s, p.desc = strings.TrimSuffix(s, sortDesc), true
		case strings.HasSuffix(s, sortAsc):
			s = strings.TrimSuffix(s, sortAsc)
		}
		t, err := meta.Parse(s + string(meta.OpHas))
		if err != nil {
			return nil, nil, err
		}
		matches := resolve(visible, t)
		if len(matches) == 0 {
			warnings = append(warnings, unknownWarning(t.Name(), keys))
		}
		for _, m := range matches {
			p.sortBy = append(p.sortBy, cond{term: t, key: m.key, path: m.path})
		}
		p.addColumn(t.Name())
	}
	return p, warnings, nil
}

func (p *Plan) addColumn(name string) {
	if !slices.Contains(p.Columns, name) {
		p.Columns = append(p.Columns, name)
	}
}

// placeKeys narrows keys to one place (3.9, XQ6); "" keeps all.
func placeKeys(keys []Key, place string) []Key {
	if place == "" {
		return keys
	}
	var out []Key
	for _, k := range keys {
		level, _, _ := strings.Cut(k.Scope, ":")
		if k.Scope == place || (meta.IsLevel(place) && level == place) {
			out = append(out, k)
		}
	}
	return out
}

type match struct {
	key  Key
	path []string
}

// resolve finds the definitions a term names: by id, else by the full
// dotted name, else by its top segment with the rest as a field path
// inside an object value (META-B7-OBJ). A term's own scope narrows too.
func resolve(keys []Key, t meta.Term) []match {
	var out []match
	inScope := func(k Key) bool {
		if t.Place != "" {
			return k.Scope == t.Place
		}
		level, _, _ := strings.Cut(k.Scope, ":")
		return t.Level == "" || level == t.Level
	}
	if !t.KeyID.IsZero() {
		for _, k := range keys {
			if k.ID == t.KeyID {
				out = append(out, match{key: k, path: t.Fields})
			}
		}
		return out
	}
	for _, k := range keys {
		if !k.Retired && k.Name == t.Key && inScope(k) {
			out = append(out, match{key: k})
		}
	}
	if len(out) > 0 {
		return out
	}
	segs := meta.Segments(t.Key)
	for i := len(segs) - 1; i >= 1; i-- {
		top := strings.Join(segs[:i], ".")
		for _, k := range keys {
			if !k.Retired && k.Name == top && inScope(k) {
				out = append(out, match{key: k, path: segs[i:]})
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

// unknownWarning is the 3.7 line for a name no visible key has.
func unknownWarning(name string, keys []Key) string {
	w := "warning: no metadata key " + strconv.Quote(name)
	best, dist := "", 3
	for _, k := range keys {
		if d := editDistance(name, k.Name); d < dist {
			best, dist = k.Name, d
		}
	}
	if best != "" {
		w += ". Did you mean " + best + "?"
	}
	return w
}

// compileCond types the term's value by k (3.7 "typed by the registry").
func compileCond(t meta.Term, k Key, path []string) (cond, error) {
	c := cond{term: t, key: k, path: path}
	elem := k.Type
	if elem.Kind == value.KindList {
		elem = meta.Type{Kind: elemKind(k.Type)}
	}
	if len(path) > 0 {
		elem = meta.Type{} // a field inside an object value: inferred
	}
	switch t.Op {
	case meta.OpHas, meta.OpMissing:
		return c, nil
	case meta.OpGt, meta.OpGe, meta.OpLt, meta.OpLe:
		kind := elem.Kind
		if kind == "" {
			kind = inferOrdered(t.Value)
		}
		b, err := parseOrdered(kind, t.Value)
		if err != nil {
			return cond{}, errs.New(errs.MetaOp, "meta: "+string(t.Op)+" compares numbers, dates, datetimes and durations; "+
				t.Name()+" is "+typeWord(k, path)).WithHint("use = or ~=, or lore sql")
		}
		c.bound = &b
		return c, nil
	case meta.OpIn:
		return cond{}, errs.New(errs.MetaOp, "meta: << (ip in range) is not supported on list yet").WithHint("use lore sql")
	case meta.OpEq, meta.OpNe, meta.OpLike:
	}
	if t.Op == meta.OpLike {
		c.want = [][]byte{canon.String(strings.ToLower(t.Value))}
		return c, nil
	}
	if len(k.Values) > 0 && len(path) == 0 {
		// OQ1: a value by name or label means the same allowed value.
		for _, v := range k.Values {
			if v.Name == t.Value || (v.Label != "" && strings.EqualFold(v.Label, t.Value)) {
				c.want = append(c.want, canon.String(v.Name))
			}
		}
		if len(c.want) == 0 {
			c.want = [][]byte{canon.String(t.Value)}
		}
		return c, nil
	}
	if elem.Kind != "" && elem.Kind != value.KindJSON {
		if co, err := meta.Coerce(elem, t.Value); err == nil {
			c.want = append(c.want, co.Raw)
		}
	}
	// The raw text too: a non-strict key may hold a text value (3.7).
	c.want = append(c.want, canon.String(t.Value))
	if n, err := value.NumberOf(t.Value); err == nil {
		if raw, merr := n.MarshalJSON(); merr == nil {
			c.want = append(c.want, raw)
		}
	}
	return c, nil
}

func typeWord(k Key, path []string) string {
	if len(path) > 0 {
		return "a field of a json value"
	}
	return k.Type.String()
}

func elemKind(t meta.Type) value.Kind {
	if t.Elem == "" {
		return value.KindText
	}
	return t.Elem
}

// Note is one "skipped" or "not checked" line of a filtered list.
type Note string

// Match reports whether a row with meta m passes every clause. note is
// set when the row was skipped (a value of the wrong kind, an unsettled
// conflict) rather than compared.
func (p *Plan) Match(rowID string, m map[ids.ID[ids.MetaKey]]value.JSON) (bool, Note) {
	if p == nil {
		return true, ""
	}
	for _, cl := range p.clauses {
		ok, note := cl.match(rowID, m)
		if !ok {
			return false, note
		}
	}
	return true, ""
}

func (cl clause) match(rowID string, m map[ids.ID[ids.MetaKey]]value.JSON) (bool, Note) {
	if len(cl.conds) == 0 {
		return false, ""
	}
	var note Note
	for _, c := range cl.conds {
		raw, present, conflict := lookup(m, c.key.ID, c.path)
		if conflict {
			note = Note("note: 1 row skipped: " + c.term.Name() + " on " + rowID + " holds an unsettled merge conflict")
			continue
		}
		switch c.term.Op {
		case meta.OpHas:
			if present {
				return true, ""
			}
			continue
		case meta.OpMissing:
			if !present {
				return true, ""
			}
			continue
		case meta.OpEq, meta.OpNe, meta.OpLike, meta.OpGt, meta.OpGe, meta.OpLt, meta.OpLe, meta.OpIn:
		}
		if !present {
			if c.term.Op == meta.OpNe {
				return true, ""
			}
			continue
		}
		items := [][]byte{raw}
		if canon.KindOf(raw) == canon.KindArray {
			if els, err := canon.Elements(raw); err == nil {
				items = els
			}
		}
		hit, skipped := c.compare(items)
		if skipped {
			note = Note("note: 1 row skipped: " + c.term.Name() + " on " + rowID + " is " + kindWord(raw))
			continue
		}
		if hit {
			return true, ""
		}
	}
	return false, note
}

// compare tests the value's items; skipped when an ordered compare met a
// value of another kind.
func (c cond) compare(items [][]byte) (hit, skipped bool) {
	switch c.term.Op {
	case meta.OpEq:
		return anyEqual(items, c.want), false
	case meta.OpNe:
		return !anyEqual(items, c.want), false
	case meta.OpLike:
		needle := strings.ToLower(c.term.Value)
		for _, it := range items {
			if strings.Contains(strings.ToLower(display(it)), needle) {
				return true, false
			}
		}
		return false, false
	case meta.OpGt, meta.OpGe, meta.OpLt, meta.OpLe:
		for _, it := range items {
			v, err := parseOrdered(c.bound.kind, display(it))
			if err != nil {
				return false, true
			}
			d := v.cmp(*c.bound)
			if (c.term.Op == meta.OpGt && d > 0) || (c.term.Op == meta.OpGe && d >= 0) ||
				(c.term.Op == meta.OpLt && d < 0) || (c.term.Op == meta.OpLe && d <= 0) {
				return true, false
			}
		}
		return false, false
	case meta.OpHas, meta.OpMissing, meta.OpIn:
	}
	return false, false
}

func anyEqual(items, want [][]byte) bool {
	for _, it := range items {
		c, err := canon.Compact(it)
		if err != nil {
			continue
		}
		for _, w := range want {
			if bytes.Equal(c, w) {
				return true
			}
		}
	}
	return false
}

// lookup finds the value of key id (and path inside it) in m. The value
// of a key with allowed values is its name (v_name), else v.
func lookup(m map[ids.ID[ids.MetaKey]]value.JSON, id ids.ID[ids.MetaKey], path []string) (raw []byte, present, conflict bool) {
	j, ok := m[id]
	if !ok {
		return nil, false, false
	}
	ms, err := canon.Members(j.Raw())
	if err != nil {
		return nil, false, false
	}
	var v, vname []byte
	for _, mb := range ms {
		switch mb.Key {
		case meta.EntryV:
			v = mb.Raw
		case meta.EntryVName:
			vname = mb.Raw
		case entryConflict:
			return nil, true, true
		}
	}
	if len(vname) > 0 && len(path) == 0 {
		v = vname
	}
	for _, f := range path {
		inner, found := member(v, f)
		if !found {
			return nil, false, false
		}
		v = inner
	}
	return v, len(v) > 0, false
}

func member(obj []byte, k string) ([]byte, bool) {
	ms, err := canon.Members(obj)
	if err != nil {
		return nil, false
	}
	for _, m := range ms {
		if m.Key == k {
			return m.Raw, true
		}
	}
	return nil, false
}

// entryConflict is the member of an entry holding an unsettled conflict
// (3.12; logic/merge writes it).
const entryConflict = "conflict"

// display is a JSON scalar as text (strings unquoted).
func display(raw []byte) string {
	if canon.KindOf(raw) == canon.KindString {
		var e meta.Entry
		e.V = raw
		return meta.Show(e)
	}
	return string(raw)
}

func kindWord(raw []byte) string {
	switch canon.KindOf(raw) {
	case canon.KindString:
		return "text"
	case canon.KindNumber:
		return "a number"
	case canon.KindBool:
		return "a bool"
	case canon.KindArray:
		return "a list"
	case canon.KindObject:
		return "an object"
	case canon.KindNull, canon.KindInvalid:
	}
	return "not a value"
}

// Compare orders two rows' meta by the --sort key: ascending unless
// :desc; rows without the key always last (3.7); a list sorts by its
// first element. 0 when no sort was asked.
func (p *Plan) Compare(a, b map[ids.ID[ids.MetaKey]]value.JSON) int {
	if p == nil || len(p.sortBy) == 0 {
		return 0
	}
	av, aok := p.sortValue(a)
	bv, bok := p.sortValue(b)
	switch {
	case !aok && !bok:
		return 0
	case !aok:
		return 1
	case !bok:
		return -1
	}
	d := compareRaw(av, bv)
	if p.desc {
		d = -d
	}
	return d
}

func (p *Plan) sortValue(m map[ids.ID[ids.MetaKey]]value.JSON) ([]byte, bool) {
	for _, c := range p.sortBy {
		raw, ok, conflict := lookup(m, c.key.ID, c.path)
		if !ok || conflict {
			continue
		}
		if canon.KindOf(raw) == canon.KindArray {
			els, err := canon.Elements(raw)
			if err != nil || len(els) == 0 {
				continue
			}
			raw = els[0]
		}
		return raw, true
	}
	return nil, false
}

// compareRaw: numbers numerically, then everything else as text; a
// number sorts before text.
func compareRaw(a, b []byte) int {
	an, aerr := parseOrdered(value.KindNumber, string(a))
	bn, berr := parseOrdered(value.KindNumber, string(b))
	switch {
	case aerr == nil && berr == nil:
		return an.cmp(bn)
	case aerr == nil:
		return -1
	case berr == nil:
		return 1
	}
	return strings.Compare(display(a), display(b))
}

// ordered is a parsed number, date, datetime or duration.
type ordered struct {
	kind value.Kind
	n    *big.Rat
	t    time.Time
	d    time.Duration
}

func (a ordered) cmp(b ordered) int {
	switch {
	case a.n != nil && b.n != nil:
		return a.n.Cmp(b.n)
	case a.kind == value.KindDuration:
		return cmpInt(int64(a.d), int64(b.d))
	}
	return a.t.Compare(b.t)
}

func cmpInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// parseOrdered reads s as kind k.
func parseOrdered(k value.Kind, s string) (ordered, error) {
	s = strings.TrimSpace(s)
	o := ordered{kind: k}
	var err error
	switch k {
	case value.KindNumber:
		r, ok := new(big.Rat).SetString(s)
		if !ok {
			return ordered{}, errs.New(errs.InvalidInput, "not a number")
		}
		o.n = r
		return o, nil
	case value.KindDate:
		o.t, err = time.Parse(time.DateOnly, s)
		return o, err
	case value.KindDateTime:
		o.t, err = time.Parse(time.RFC3339Nano, s)
		return o, err
	case value.KindDuration:
		o.d, err = time.ParseDuration(s)
		return o, err
	case value.KindText, value.KindBool, value.KindIP, value.KindCIDR, value.KindDNS, value.KindURL,
		value.KindEmail, value.KindRef, value.KindJSON, value.KindList, value.KindNull:
	}
	return ordered{}, errs.New(errs.InvalidInput, "not an ordered type")
}

// inferOrdered guesses the kind of a bound on an untyped field.
func inferOrdered(s string) value.Kind {
	for _, k := range []value.Kind{value.KindNumber, value.KindDate, value.KindDateTime, value.KindDuration} {
		if _, err := parseOrdered(k, s); err == nil {
			return k
		}
	}
	return value.KindText
}

// editDistance is the Levenshtein distance (the "did you mean" hint).
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
