package merge

import (
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// FieldMeta is the name a row's metadata takes in Result: a conflict or a
// newer-side pick on entry <mky id> is reported as "meta.<mky id>"; a meta
// that could not be merged at all is the one conflict "meta" (R10).
const FieldMeta = row.KeyMeta

// StrategyMeta is the nested merge of a row's meta (METADATA 3.12, MQ4,
// R4): entry by entry with the StrategyEntriesByID rules, plus three meta
// rules. An object value merges per field (META-B8-OBJ), a list value is
// a set union that never conflicts and whose emptied list removes the key,
// and every v both sides changed that the newer side settled is listed in
// newerWins (the merge report's "auto-resolved" line). A strict key changed
// on both sides holds {"conflict": {base, ours, theirs}} and is listed in
// conflicts. It reads only its three inputs, so every machine and either
// direction write the same bytes.
func StrategyMeta(base, ours, theirs map[ids.ID[ids.MetaKey]]value.JSON, newer side) (merged map[ids.ID[ids.MetaKey]]value.JSON, conflicts, newerWins []string, err error) {
	b, err := metaObject(base)
	if err != nil {
		return nil, nil, nil, err
	}
	o, err := metaObject(ours)
	if err != nil {
		return nil, nil, nil, err
	}
	t, err := metaObject(theirs)
	if err != nil {
		return nil, nil, nil, err
	}
	opts := &entryOpts{objects: true, dropEmpty: true}
	v, conflicts, err := entriesMerge(b, o, t, newer, opts)
	if err != nil {
		return nil, nil, nil, err
	}
	raw, err := v.MarshalJSON()
	if err != nil {
		return nil, nil, nil, errs.Wrap(errs.Internal, "merge: encode merged meta", err)
	}
	ms, err := canon.Members(raw)
	if err != nil {
		return nil, nil, nil, errs.Wrap(errs.Internal, "merge: read merged meta", err)
	}
	merged = make(map[ids.ID[ids.MetaKey]]value.JSON, len(ms))
	for _, m := range ms {
		id, perr := ids.Parse[ids.MetaKey](m.Key)
		if perr != nil {
			return nil, nil, nil, errs.Wrap(errs.InvalidInput, "merge: meta key "+m.Key, perr)
		}
		j, jerr := value.JSONOf(m.Raw)
		if jerr != nil {
			return nil, nil, nil, jerr
		}
		merged[id] = j
	}
	slices.Sort(conflicts)
	newerWins = slices.DeleteFunc(slices.Clone(opts.newerWins), func(id string) bool { return slices.Contains(conflicts, id) })
	slices.Sort(newerWins)
	return merged, conflicts, newerWins, nil
}

// metaObject is a meta map as one id-keyed JSON object (nil for none).
func metaObject(m map[ids.ID[ids.MetaKey]]value.JSON) (value.Value, error) {
	if len(m) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(m))
	byKey := make(map[string]value.JSON, len(m))
	for k, v := range m {
		keys = append(keys, k.String())
		byKey[k.String()] = v
	}
	slices.Sort(keys)
	ms := make([]canon.Member, 0, len(keys))
	for _, k := range keys {
		ms = append(ms, canon.Member{Key: k, Raw: byKey[k].Raw()})
	}
	obj, err := canon.Object(ms)
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "merge: meta is not an object of entries", err)
	}
	return value.JSONOf(obj)
}

// MetaConflicts is the recursive conflict scanner of a row's meta (3.12):
// the ids of the entries that hold an unsettled {"conflict": …}, sorted.
// Text conflict markers are found by HasMarkers; a meta conflict is
// structured, so every check that blocks marker text (merge driver,
// pre-commit, landing, the cache's conflict mark) also asks this.
func MetaConflicts(r row.Row) []string {
	var out []string
	for k, v := range r.Meta {
		if hasConflict(v.Raw()) {
			out = append(out, k.String())
		}
	}
	slices.Sort(out)
	return out
}

// metaField is the Result name of one meta entry.
func metaField(id string) string { return FieldMeta + "." + strings.TrimSpace(id) }
