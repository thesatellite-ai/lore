package engine

import (
	"bytes"
	"context"
	"maps"
	"slices"

	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// refreshMetaLabels is the lazy label refresh of METADATA 3.8 and 3.12:
// a row written for another reason takes each metadata entry's key name,
// and the names of the allowed values it holds (v_name), from the
// registry by id, so `lore meta rename` and `meta edit --rename-value`
// (which change only the registry) reach the row file on its next write.
// Only labels move: the value ids, flags, scope and unknown members stay
// byte for byte. An entry is left as it is when its key is not visible
// here, is defined under that id with two different names (a move seen
// half way), holds an unsettled conflict, or names a value id the
// registry does not hold. Called by Update after its no-op check, so a
// refresh alone never makes a write (3.12: a label is rewritten only when
// the row is written for another reason, or by rename --rewrite-rows).
func (e *Engine) refreshMetaLabels(ctx context.Context, m map[ids.ID[ids.MetaKey]]value.JSON) (map[ids.ID[ids.MetaKey]]value.JSON, error) {
	if len(m) == 0 {
		return m, nil
	}
	reg, err := e.metaRegistry(ctx)
	if err != nil {
		return nil, err
	}
	defs := map[ids.ID[ids.MetaKey]]*keyDef{}
	split := map[ids.ID[ids.MetaKey]]bool{}
	for i := range reg {
		d := &reg[i]
		if prev, seen := defs[d.id]; seen && prev.name != d.name {
			split[d.id] = true
		}
		defs[d.id] = d
	}
	conflicted := merge.MetaConflicts(row.Row{Meta: m})
	var out map[ids.ID[ids.MetaKey]]value.JSON
	for id, j := range m {
		d, ok := defs[id]
		if !ok || split[id] || slices.Contains(conflicted, id.String()) {
			continue
		}
		en, derr := meta.DecodeEntry(j)
		if derr != nil {
			continue // quarantined: lint and meta get report it
		}
		next := en
		next.Name = d.name
		if len(en.VName) > 0 && len(d.values) > 0 {
			if vn, ok := valueNames(*d, en.V); ok {
				next.VName = vn
			}
		}
		if next.Name == en.Name && bytes.Equal(next.VName, en.VName) {
			continue
		}
		nj, eerr := next.Encode()
		if eerr != nil {
			return nil, eerr
		}
		if out == nil {
			out = maps.Clone(m)
		}
		out[id] = nj
	}
	if out == nil {
		return m, nil
	}
	return out, nil
}

// valueNames is the v_name of v (one value id, or a list of them) from
// d's allowed values; ok=false when v holds anything d does not list.
func valueNames(d keyDef, v []byte) ([]byte, bool) {
	name := func(item []byte) ([]byte, bool) {
		s, err := unquoteJSON(item)
		if err != nil {
			return nil, false
		}
		for _, a := range d.values {
			if a.id.String() == s {
				return canon.String(a.name), true
			}
		}
		return nil, false
	}
	if canon.KindOf(v) != canon.KindArray {
		return name(v)
	}
	items, err := canon.Elements(v)
	if err != nil {
		return nil, false
	}
	ns := make([][]byte, 0, len(items))
	for _, it := range items {
		n, ok := name(it)
		if !ok {
			return nil, false
		}
		ns = append(ns, n)
	}
	out, err := canon.Compact(append(append([]byte("["), joinRaw(ns)...), ']'))
	return out, err == nil
}
