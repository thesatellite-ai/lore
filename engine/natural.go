package engine

import (
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// NaturalID is the v0.1.17 deterministic id of a row of prefix p whose
// natural key is parts (in the table's unique-index order): ids.Natural.
// Exported so an operation can find a natural-key row (a tag by project
// and name) without a lookup.
func NaturalID(p ids.Prefix, parts ...string) (ids.AnyID, error) {
	id, err := ids.Natural(p, parts...)
	if err != nil {
		return ids.AnyID{}, errs.Wrap(errs.Internal, "natural id", err)
	}
	return id, nil
}

// naturalParts is what a natural id of def in store st hashes: the key
// parts alone (v0.1.17's bytes) for a store without a namespace or a table
// keyed globally (actors: one id per person everywhere), else the store's
// namespace, the table and the key parts. Rows already on disk keep the
// ids they were written with; lookups go by key (FindByKey). Natural ids
// per store (L78 + L101): the same key in two stores must be two rows, or a
// write to one store would collide with a row it cannot see.
func naturalParts(st Attached, def schema.TableDef, keyParts []string) []string {
	if st.KeyNamespace == "" || def.GlobalKey {
		return keyParts
	}
	return append([]string{st.KeyNamespace, string(def.Name)}, keyParts...)
}

// naturalKey returns the first unique index of def and the values of its
// fields in fields; ok is false when the table has none or a key field is
// unset (the row then gets a random id).
func naturalKey(def schema.TableDef, fields map[string]value.Value) ([]string, []string, bool) {
	for _, ix := range def.Indexes {
		if !ix.Unique || len(ix.Fields) == 0 {
			continue
		}
		parts := make([]string, 0, len(ix.Fields))
		for _, f := range ix.Fields {
			v, ok := fields[f]
			if !ok || v.Kind() == value.KindNull || v.String() == "" {
				return nil, nil, false
			}
			parts = append(parts, v.String())
		}
		return ix.Fields, parts, true
	}
	return nil, nil, false
}
