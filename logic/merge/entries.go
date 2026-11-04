package merge

import (
	"bytes"
	"slices"

	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
)

// Members of one entry of an id-keyed object (resources.fields, STORE_SERVER
// _SPEC 4.4; the same shape as a row's meta entries, METADATA 3.1).
const (
	entryName      = "name"
	entryValue     = "v"
	entryStrict    = "strict"
	entrySensitive = "sensitive"
	entryConflict  = "conflict"
	conflictBase   = "base"
	conflictOurs   = "ours"
	conflictTheirs = "theirs"
	jsonTrue       = "true"
	jsonNull       = "null"
)

// StrategyEntriesByID is the nested merge of a JSON object keyed by a
// permanent id, each entry {name, v, [strict], [sensitive]} (RESOURCES 3.13,
// R4, RQ5; the rules of METADATA 3.12). It reads only its three inputs:
//   - an entry on one side only: added → kept; removed while the other side
//     left it unchanged → removed; removed while the other side changed it →
//     the change wins (strict: a conflict);
//   - name: the newer side's; strict and sensitive: true if either says so;
//   - v changed on one side: that side's; on both: a list merges as a set
//     (StrategySetUnion), except a strict list where one side removed and
//     the other added, which is a conflict; any other value takes the newer
//     side, or is a conflict when strict;
//   - other members: three-way, newer side on a clash.
//
// A conflict replaces the entry's v with {"conflict": {base, ours, theirs}}
// (absent sides are null) and its id is returned in conflicts, so the row
// is an unsettled conflict that a person resolves. An entry that already
// holds a conflict stays one. A side that is not a JSON object is refused
// (the caller turns that into one conflict on the whole field, R10).
func StrategyEntriesByID(base, ours, theirs value.Value, newer side) (merged value.Value, conflicts []string, err error) {
	return entriesMerge(base, ours, theirs, newer, &entryOpts{})
}

// entryOpts are the differences between the two users of the entry merge.
// resources.fields keeps the RESOURCES 3.13 rules (zero value); a row's
// meta (METADATA 3.12, StrategyMeta) also recurses into object values field
// by field, drops a list key the set merge emptied, and records each
// newer-side pick in newerWins.
type entryOpts struct {
	objects   bool
	dropEmpty bool
	newerWins []string
}

// entriesMerge is StrategyEntriesByID with the options of its caller.
func entriesMerge(base, ours, theirs value.Value, newer side, opts *entryOpts) (merged value.Value, conflicts []string, err error) {
	b, err := entryMap(base)
	if err != nil {
		return nil, nil, err
	}
	o, err := entryMap(ours)
	if err != nil {
		return nil, nil, err
	}
	t, err := entryMap(theirs)
	if err != nil {
		return nil, nil, err
	}
	var out []canon.Member
	for _, id := range unionKeys(b, o, t) {
		bv, inB := b[id]
		ov, inO := o[id]
		tv, inT := t[id]
		var (
			entry    []byte
			conflict bool
		)
		switch {
		case inO && inT:
			var won bool
			entry, conflict, won, err = mergeEntry(bv, inB, ov, tv, newer, opts)
			if won {
				opts.newerWins = append(opts.newerWins, id)
			}
		case inO, inT:
			present := ov
			if inT {
				present = tv
			}
			switch {
			case !inB:
				entry = present // added
			case bytes.Equal(present, bv):
				continue // removed on the other side, unchanged here
			case isStrict(bv) || isStrict(present):
				entry, err = conflictEntry(present, bv, inB, ov, inO, tv, inT)
				conflict = true
			default:
				entry = present // a change beats a removal
			}
		default:
			continue // only in base: removed on both sides
		}
		if err != nil {
			return nil, nil, err
		}
		if conflict {
			conflicts = append(conflicts, id)
		}
		if entry == nil {
			continue // a meta list key the set merge emptied (3.12)
		}
		out = append(out, canon.Member{Key: id, Raw: entry})
	}
	obj, err := canon.Object(out)
	if err != nil {
		return nil, nil, errs.Wrap(errs.Internal, "merge: build the merged entries", err)
	}
	j, err := value.JSONOf(obj)
	return j, conflicts, err
}

// mergeEntry merges one entry present on both sides.
// won reports that a v both sides changed was settled by the newer side.
// A nil entry (meta only) means the merged list is empty: the key goes.
func mergeEntry(bv []byte, inB bool, ov, tv []byte, newer side, opts *entryOpts) (entry []byte, conflict, won bool, err error) {
	if bytes.Equal(ov, tv) {
		return ov, hasConflict(ov), false, nil
	}
	if inB && bytes.Equal(ov, bv) && !hasConflict(tv) {
		return tv, false, false, nil
	}
	if inB && bytes.Equal(tv, bv) && !hasConflict(ov) {
		return ov, false, false, nil
	}
	if hasConflict(ov) || hasConflict(tv) {
		// An unsettled conflict on either side stays unsettled.
		return pickRaw(bv, inB, ov, tv, newer), true, false, nil
	}
	bm, om, tm := memberMap(bv), memberMap(ov), memberMap(tv)
	strict := isStrict(bv) || isStrict(ov) || isStrict(tv)
	res := map[string][]byte{}
	for _, k := range unionKeys(bm, om, tm) {
		bk, inBk := bm[k]
		ok, inOk := om[k]
		tk, inTk := tm[k]
		switch k {
		case entryStrict, entrySensitive:
			if string(ok) == jsonTrue || string(tk) == jsonTrue {
				res[k] = []byte(jsonTrue)
			}
			continue
		case entryValue:
			v, clash, w, verr := mergeEntryValue(bk, inBk, ok, inOk, tk, inTk, strict, newer, opts)
			if verr != nil {
				return nil, false, false, verr
			}
			won = won || w
			if clash {
				conflict = true
				c, cerr := conflictObject(bk, inBk, ok, inOk, tk, inTk)
				if cerr != nil {
					return nil, false, false, cerr
				}
				res[entryConflict] = c
				continue
			}
			if opts.dropEmpty && v != nil && string(v) == "[]" {
				return nil, false, won, nil
			}
			if v != nil {
				res[k] = v
			}
			continue
		}
		if v := pickMember(bk, inBk, ok, inOk, tk, inTk, newer); v != nil {
			res[k] = v
		}
	}
	if _, hasV := res[entryValue]; opts.dropEmpty && !hasV && !conflict {
		return nil, false, won, nil // v gone on the merge (an object emptied): the key goes
	}
	ms := make([]canon.Member, 0, len(res))
	for _, k := range sortedKeys(res) {
		ms = append(ms, canon.Member{Key: k, Raw: res[k]})
	}
	obj, err := canon.Object(ms)
	if err != nil {
		return nil, false, false, errs.Wrap(errs.Internal, "merge: build a merged entry", err)
	}
	return obj, conflict, won, nil
}

// mergeEntryValue settles v. clash is true when a person must decide; won
// when both sides changed it and the newer side was taken (reported).
func mergeEntryValue(b []byte, inB bool, o []byte, inO bool, t []byte, inT bool, strict bool, newer side, opts *entryOpts) (v []byte, clash, won bool, err error) {
	switch {
	case inO && inT && bytes.Equal(o, t):
		return o, false, false, nil
	case inB && inO && bytes.Equal(o, b):
		return t, false, false, nil // nil when theirs removed v
	case inB && inT && bytes.Equal(t, b):
		return o, false, false, nil
	case !inB && !inO:
		return t, false, false, nil
	case !inB && !inT:
		return o, false, false, nil
	}
	if opts.objects && inO && inT && canon.KindOf(o) == canon.KindObject && canon.KindOf(t) == canon.KindObject &&
		(!inB || canon.KindOf(b) == canon.KindObject) {
		// META-B8-OBJ: an object value (a dotted key, META-B7-OBJ) merges
		// per field with the same rules, so deploy.env on one side and
		// deploy.region on the other both survive (3.12 object row).
		return mergeObjectValue(b, inB, o, t, strict, newer, opts)
	}
	// Both sides changed v differently.
	if inO && inT && canon.KindOf(o) == canon.KindArray && canon.KindOf(t) == canon.KindArray &&
		(!inB || canon.KindOf(b) == canon.KindArray) {
		// RQ5 (resources): a strict list where one side removed and the
		// other added is a person's call; a meta list never conflicts (3.12).
		if strict && !opts.objects && removeVsAdd(b, o, t) {
			return nil, true, false, nil
		}
		bv, ov, tv, perr := rawLists(b, inB, o, t)
		if perr != nil {
			return nil, false, false, perr
		}
		m, merr := setMerge(bv, ov, tv, newer, setKeyValue, nil)
		if merr != nil {
			return nil, false, false, merr
		}
		raw, rerr := m.MarshalJSON()
		return raw, false, false, rerr
	}
	if strict {
		return nil, true, false, nil
	}
	if newer == newerOurs {
		return o, false, true, nil // nil when ours removed v
	}
	return t, false, true, nil
}

// mergeObjectValue merges two object values field by field: each field
// is settled by mergeEntryValue (nested objects recurse, lists union); a
// strict clash on any field is a clash of the whole value.
func mergeObjectValue(b []byte, inB bool, o, t []byte, strict bool, newer side, opts *entryOpts) (v []byte, clash, won bool, err error) {
	var bm map[string][]byte
	if inB {
		bm = memberMap(b)
	}
	om, tm := memberMap(o), memberMap(t)
	res := map[string][]byte{}
	for _, k := range unionKeys(bm, om, tm) {
		bk, inBk := bm[k]
		ok, inOk := om[k]
		tk, inTk := tm[k]
		fv, c, w, ferr := mergeEntryValue(bk, inBk, ok, inOk, tk, inTk, strict, newer, opts)
		if ferr != nil || c {
			return nil, c, false, ferr
		}
		won = won || w
		if fv != nil && (!opts.dropEmpty || string(fv) != "[]") {
			res[k] = fv
		}
	}
	if len(res) == 0 {
		return nil, false, won, nil // every field gone: the key goes
	}
	ms := make([]canon.Member, 0, len(res))
	for _, k := range sortedKeys(res) {
		ms = append(ms, canon.Member{Key: k, Raw: res[k]})
	}
	obj, err := canon.Object(ms)
	if err != nil {
		return nil, false, false, errs.Wrap(errs.Internal, "merge: build a merged object value", err)
	}
	c, err := canon.Compact(obj)
	return c, false, won, err
}

// removeVsAdd reports a strict list clash: one side removed an element of
// base while the other side added one (RQ5: a person decides).
func removeVsAdd(b, o, t []byte) bool {
	bs, os, ts := elemSet(b), elemSet(o), elemSet(t)
	removed := func(side map[string]bool) bool {
		for k := range bs {
			if !side[k] {
				return true
			}
		}
		return false
	}
	added := func(side map[string]bool) bool {
		for k := range side {
			if !bs[k] {
				return true
			}
		}
		return false
	}
	return (removed(os) && added(ts)) || (removed(ts) && added(os))
}

// elemSet is the set of a JSON list's element identities.
func elemSet(raw []byte) map[string]bool {
	out := map[string]bool{}
	elems, err := canon.Elements(raw)
	if err != nil {
		return out
	}
	for _, e := range elems {
		if c, cerr := canon.Compact(e); cerr == nil {
			out[setKeyValue(c)] = true
		}
	}
	return out
}

// rawLists wraps the three list bytes as values for setMerge.
func rawLists(b []byte, inB bool, o, t []byte) (bv, ov, tv value.Value, err error) {
	if inB {
		if bv, err = value.JSONOf(b); err != nil {
			return nil, nil, nil, err
		}
	}
	if ov, err = value.JSONOf(o); err != nil {
		return nil, nil, nil, err
	}
	tv, err = value.JSONOf(t)
	return bv, ov, tv, err
}

// conflictEntry is the entry for a removed-versus-changed strict clash: the
// present side's labels plus the conflict object of the three v values.
func conflictEntry(present, bv []byte, inB bool, ov []byte, inO bool, tv []byte, inT bool) ([]byte, error) {
	pick := func(raw []byte, in bool) ([]byte, bool) {
		if !in {
			return nil, false
		}
		v, ok := memberMap(raw)[entryValue]
		return v, ok
	}
	b, hb := pick(bv, inB)
	o, ho := pick(ov, inO)
	t, ht := pick(tv, inT)
	c, err := conflictObject(b, hb, o, ho, t, ht)
	if err != nil {
		return nil, err
	}
	res := memberMap(present)
	delete(res, entryValue)
	res[entryConflict] = c
	ms := make([]canon.Member, 0, len(res))
	for _, k := range sortedKeys(res) {
		ms = append(ms, canon.Member{Key: k, Raw: res[k]})
	}
	return canon.Object(ms)
}

// conflictObject is {"base": …, "ours": …, "theirs": …}; an absent side is
// null (METADATA 3.12 conflict shape).
func conflictObject(b []byte, inB bool, o []byte, inO bool, t []byte, inT bool) ([]byte, error) {
	or := func(raw []byte, in bool) []byte {
		if !in {
			return []byte(jsonNull)
		}
		return raw
	}
	return canon.Object([]canon.Member{
		{Key: conflictBase, Raw: or(b, inB)},
		{Key: conflictOurs, Raw: or(o, inO)},
		{Key: conflictTheirs, Raw: or(t, inT)},
	})
}

// pickMember is the three-way rule for one member; nil means absent.
func pickMember(b []byte, inB bool, o []byte, inO bool, t []byte, inT bool, newer side) []byte {
	switch {
	case inO && inT && bytes.Equal(o, t):
		return o
	case inB && inO && bytes.Equal(o, b):
		return t
	case inB && inT && bytes.Equal(t, b):
		return o
	case !inB && !inO:
		return t
	case !inB && !inT:
		return o
	case newer == newerOurs:
		return o
	}
	return t
}

// entryMap reads an id-keyed object into id → canonical entry bytes.
// Absent and null are empty; anything else that is not an object of
// objects is refused.
func entryMap(v value.Value) (map[string][]byte, error) {
	out := map[string][]byte{}
	if v == nil {
		return out, nil
	}
	if _, null := v.(value.Null); null {
		return out, nil
	}
	raw, err := v.MarshalJSON()
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "merge: an entries field does not encode as json", err)
	}
	if canon.KindOf(raw) == canon.KindNull {
		return out, nil
	}
	ms, err := canon.Members(raw)
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "merge: an entries field is not a json object", err)
	}
	for _, m := range ms {
		if canon.KindOf(m.Raw) != canon.KindObject {
			return nil, errs.New(errs.InvalidInput, "merge: entry "+m.Key+" is not an object")
		}
		c, cerr := canon.Compact(m.Raw)
		if cerr != nil {
			return nil, errs.Wrap(errs.InvalidInput, "merge: entry "+m.Key, cerr)
		}
		out[m.Key] = c
	}
	return out, nil
}

// memberMap reads one canonical entry into member → raw (empty on error).
func memberMap(raw []byte) map[string][]byte {
	out := map[string][]byte{}
	ms, err := canon.Members(raw)
	if err != nil {
		return out
	}
	for _, m := range ms {
		out[m.Key] = m.Raw
	}
	return out
}

// hasConflict reports whether the entry holds an unsettled conflict.
func hasConflict(raw []byte) bool {
	_, ok := memberMap(raw)[entryConflict]
	return ok
}

// isStrict reports an entry marked strict (R4: strict travels in the row).
func isStrict(raw []byte) bool { return string(memberMap(raw)[entryStrict]) == jsonTrue }

// sortedKeys is m's keys, sorted.
func sortedKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
