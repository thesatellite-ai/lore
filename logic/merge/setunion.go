package merge

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
)

// side names which input of a three-way merge is the newer one; the
// strategies need it only to settle an element (or entry) both sides
// changed differently.
type side bool

const (
	newerOurs   side = true
	newerTheirs side = false
)

// elemKey turns one JSON list element into its merge identity; "" means
// the element has no identity (dropped as corrupt by the caller).
type elemKey func(raw []byte) string

// setKeyValue is the identity of StrategySetUnion: the element's
// normalised value. A string is compared trimmed (" act_1 " and "act_1"
// are one assignee); anything else by its canonical JSON (an object's key
// order never matters).
func setKeyValue(raw []byte) string {
	if canon.KindOf(raw) == canon.KindString {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return "s:" + strings.TrimSpace(s)
		}
	}
	c, err := canon.Compact(raw)
	if err != nil {
		return ""
	}
	return "j:" + string(c)
}

// urlEntryKey is the identity of StrategyURLSet: the entry's normalised
// url (PROVENANCE_DESIGN 10.2: the http and ssh forms of one repo are one
// entry). An entry whose url does not normalise falls back to its whole
// canonical value, so a hand-edited entry is kept, never dropped.
func urlEntryKey(raw []byte) string {
	var e repourl.Entry
	if json.Unmarshal(raw, &e) == nil {
		if k := e.Key(); k != "" {
			return "u:" + k
		}
	}
	return setKeyValue(raw)
}

// StrategySetUnion is the three-way set merge of a list field (RQ5, OV12:
// tasks.assignees, tasks.watchers, tasks.blocked_by, the top-level
// shared_as): an element either side added is kept; an element one side
// removed stays removed unless the other side added it anew (it was not in
// base). Identity is the normalised value (setKeyValue). The result is
// sorted by identity, so both machines write the same bytes.
func StrategySetUnion(base, ours, theirs value.Value, newer side) (value.Value, error) {
	return setMerge(base, ours, theirs, newer, setKeyValue, nil, pickRaw)
}

// StrategyURLSet is the set merge of repos.urls (PROVENANCE_DESIGN 7, 10.2:
// union by url): entries keyed by repourl.Normalize of their url; the same
// url changed on both sides (closed with `to` on one, made primary on the
// other) takes the newer side's entry, except that a confirmed entry
// (primary or mirror: someone ran `lore repo set-url` or init) always beats
// an unconfirmed sighting of the same url (pickURLEntry, G-17). Sorted by
// `from`, then url, so the history reads oldest first like the repos row
// sample.
func StrategyURLSet(base, ours, theirs value.Value, newer side) (value.Value, error) {
	return setMerge(base, ours, theirs, newer, urlEntryKey, func(a, b []byte) int {
		if c := strings.Compare(memberText(a, "from"), memberText(b, "from")); c != 0 {
			return c
		}
		return strings.Compare(urlEntryKey(a), urlEntryKey(b))
	}, pickURLEntry)
}

// pickURLEntry settles one url entry both sides hold differently. A
// confirmed side wins over an unconfirmed one whichever is newer: a clone
// that saw the new remote before pulling the set-url records it as
// "seen, unconfirmed" a few seconds later, and the newer-side rule then
// dropped the confirmation, leaving the repo with no primary address
// (G-17). Anything else is pickRaw.
func pickURLEntry(bv []byte, inB bool, ov, tv []byte, newer side) []byte {
	if !bytes.Equal(ov, tv) {
		oc, tc := urlEntryConfirmed(ov), urlEntryConfirmed(tv)
		switch {
		case oc && !tc:
			return ov
		case tc && !oc:
			return tv
		}
	}
	return pickRaw(bv, inB, ov, tv, newer)
}

// urlEntryConfirmed reports whether a url entry is a confirmed address
// (primary or mirror), open or closed; an unconfirmed sighting or an
// entry that does not decode is not.
func urlEntryConfirmed(raw []byte) bool {
	var e repourl.Entry
	if json.Unmarshal(raw, &e) != nil {
		return false
	}
	return e.Kind == repourl.KindPrimary || e.Kind == repourl.KindMirror
}

// pickFunc settles one element both sides hold (pickRaw, pickURLEntry).
type pickFunc func(bv []byte, inB bool, ov, tv []byte, newer side) []byte

// setMerge is the shared three-way keyed set merge. order sorts the result
// (nil: by key); pick settles an element both sides hold. It refuses a
// side that is not a JSON list.
func setMerge(base, ours, theirs value.Value, newer side, key elemKey, order func(a, b []byte) int, pick pickFunc) (value.Value, error) {
	b, err := keyedElems(base, key)
	if err != nil {
		return nil, err
	}
	o, err := keyedElems(ours, key)
	if err != nil {
		return nil, err
	}
	t, err := keyedElems(theirs, key)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, k := range unionKeys(b, o, t) {
		bv, inB := b[k]
		ov, inO := o[k]
		tv, inT := t[k]
		switch {
		case inO && inT:
			out[k] = pick(bv, inB, ov, tv, newer)
		case inO && !inT:
			// theirs lacks it: removed by theirs (kept removed) unless it
			// was never in base (added by ours) or ours changed it since
			// (a change beats a removal, METADATA 3.12).
			if !inB || !bytes.Equal(ov, bv) {
				out[k] = ov
			}
		case inT && !inO:
			if !inB || !bytes.Equal(tv, bv) {
				out[k] = tv
			}
		}
	}
	parts := make([][]byte, 0, len(out))
	ks := make([]string, 0, len(out))
	for k := range out {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	for _, k := range ks {
		parts = append(parts, out[k])
	}
	if order != nil {
		slices.SortStableFunc(parts, order)
	}
	raw := append(append([]byte("["), bytes.Join(parts, []byte(","))...), ']')
	return sameShape(raw, ours, theirs)
}

// pickRaw settles one element present on both sides: equal sides agree, a
// side equal to base did not change, otherwise the newer side.
func pickRaw(bv []byte, inB bool, ov, tv []byte, newer side) []byte {
	switch {
	case bytes.Equal(ov, tv):
		return ov
	case inB && bytes.Equal(ov, bv):
		return tv
	case inB && bytes.Equal(tv, bv):
		return ov
	case newer == newerOurs:
		return ov
	}
	return tv
}

// keyedElems reads a list value into identity → canonical element bytes.
// Absent and null are the empty set; an element with no identity is
// skipped (a hand edit cannot make the merge fail).
func keyedElems(v value.Value, key elemKey) (map[string][]byte, error) {
	out := map[string][]byte{}
	if v == nil {
		return out, nil
	}
	if _, null := v.(value.Null); null {
		return out, nil
	}
	raw, err := v.MarshalJSON()
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "merge: a set field does not encode as json", err)
	}
	if canon.KindOf(raw) == canon.KindNull {
		return out, nil
	}
	elems, err := canon.Elements(raw)
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "merge: a set field is not a json list", err)
	}
	for _, e := range elems {
		c, cerr := canon.Compact(e)
		if cerr != nil {
			continue
		}
		if k := key(c); k != "" {
			if old, seen := out[k]; !seen || bytes.Compare(c, old) > 0 {
				out[k] = c // a duplicate inside one side: keep one, deterministically
			}
		}
	}
	return out, nil
}

// unionKeys is the sorted union of the maps' keys.
func unionKeys(ms ...map[string][]byte) []string {
	seen := map[string]struct{}{}
	for _, m := range ms {
		for k := range m {
			seen[k] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// sameShape returns raw as the value type the sides used: a typed list
// when a side was a value.List (a decoded file), value.JSON when a side was
// JSON (an engine write), else whatever Infer makes of it.
func sameShape(raw []byte, sides ...value.Value) (value.Value, error) {
	for _, s := range sides {
		switch v := s.(type) {
		case value.List:
			if l, err := value.ParseList(v.Elem(), raw); err == nil {
				return l, nil
			}
		case value.JSON:
			return value.JSONOf(raw)
		}
	}
	return value.Infer(raw)
}
