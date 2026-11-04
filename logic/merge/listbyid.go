package merge

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// Keys of a list-by-id item.
const (
	// listKeyID is the item's permanent id, the merge key.
	listKeyID = "id"
	// listKeyUpdatedAt orders two versions of one item; a fixed-width UTC
	// timestamp, so text order is time order.
	listKeyUpdatedAt = "updated_at"
)

// StrategyListByID merges two versions of a JSON list of objects keyed by
// "id" (TIME_TRACKING_DESIGN 7.2; tasks.time_entries uses it):
//   - an item present on either side is kept (two clones appending at once
//     both survive; a merge never drops an item);
//   - the same id on both sides: the newer "updated_at" wins for the whole
//     item; a tombstone (deleted_at set) is just an edit, so it wins only
//     when it is newer;
//   - equal "updated_at" with different bytes: the larger canonical bytes
//     win, so the outcome never depends on which side is "ours".
//
// The result is sorted by id (ids are UUIDv7, so that is creation order).
// It refuses a value that is not a list of objects each with a text "id".
func StrategyListByID(a, b value.Value) (value.Value, error) {
	byID := map[string][]byte{}
	for _, side := range []value.Value{a, b} {
		items, err := listItems(side)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			old, seen := byID[it.id]
			if !seen || newerItem(it.raw, old) {
				byID[it.id] = it.raw
			}
		}
	}
	keys := make([]string, 0, len(byID))
	for k := range byID {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([][]byte, len(keys))
	for i, k := range keys {
		parts[i] = byID[k]
	}
	return value.JSONOf(append(append([]byte("["), bytes.Join(parts, []byte(","))...), ']'))
}

// listItem is one element of a list-by-id value.
type listItem struct {
	id  string
	raw []byte
}

// listItems parses v ("absent" and null are an empty list).
func listItems(v value.Value) ([]listItem, error) {
	if v == nil {
		return nil, nil
	}
	if _, null := v.(value.Null); null {
		return nil, nil
	}
	// A row decoded from a file holds the array as a generic list value,
	// one written by the engine as value.JSON: read either as JSON.
	raw, err := v.MarshalJSON()
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "merge: a list-by-id field does not encode as json", err)
	}
	elems, err := canon.Elements(raw)
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "merge: a list-by-id field is not a json list", err)
	}
	out := make([]listItem, 0, len(elems))
	for _, e := range elems {
		id := memberText(e, listKeyID)
		if id == "" {
			return nil, errs.New(errs.InvalidInput, "merge: a list-by-id item has no text id")
		}
		out = append(out, listItem{id: id, raw: e})
	}
	return out, nil
}

// newerItem reports whether a beats b (same id): newer updated_at, ties by
// canonical bytes.
func newerItem(a, b []byte) bool {
	if c := strings.Compare(memberText(a, listKeyUpdatedAt), memberText(b, listKeyUpdatedAt)); c != 0 {
		return c > 0
	}
	return bytes.Compare(a, b) > 0
}

// memberText is the text member key of the JSON object obj ("" when absent
// or not text).
func memberText(obj []byte, key string) string {
	ms, err := canon.Members(obj)
	if err != nil {
		return ""
	}
	for _, m := range ms {
		if m.Key != key {
			continue
		}
		var s string
		if json.Unmarshal(m.Raw, &s) != nil {
			return ""
		}
		return s
	}
	return ""
}

// strategyMerge applies the field strategy s to a field both sides changed
// differently; ok is false for MergeDefault (the row rule applies).
// conflicts lists entry ids a person must settle (MergeEntriesByID only).
func strategyMerge(s schema.MergeStrategy, base, ours, theirs value.Value, newer side) (v value.Value, conflicts []string, ok bool, err error) {
	switch s {
	case schema.MergeListByID:
		v, err = StrategyListByID(ours, theirs)
		return v, nil, true, err
	case schema.MergeSetUnion:
		v, err = StrategySetUnion(base, ours, theirs, newer)
		return v, nil, true, err
	case schema.MergeURLSet:
		v, err = StrategyURLSet(base, ours, theirs, newer)
		return v, nil, true, err
	case schema.MergeEntriesByID:
		v, conflicts, err = StrategyEntriesByID(base, ours, theirs, newer)
		return v, conflicts, true, err
	case schema.MergeDefault:
	}
	return nil, nil, false, nil
}
