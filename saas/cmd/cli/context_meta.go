// context_meta.go — `lore context --meta` (CONTEXT_BUDGET 6.13): metadata
// is never rendered into a context file, and lore context prints the
// metadata of the rows it returns only when asked.
//
//	pickOf ─► pick.metas (each entry's key id, name, value, own sensitive label)
//	--meta ─► withMeta: registry sensitive flags (App.MetaKeyFlags, METADATA 3.20)
//	       ─► one "meta: k=v …" line after each row (full and line form),
//	          sensitive values masked; --json: pick.meta {name: value}
package main

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/logic/refpoint"
	"github.com/thesatellite-ai/lore/model/row"
)

// pickMeta is one metadata entry of a picked row.
type pickMeta struct {
	keyID, name, val string
	sensitive        bool
}

// metasOf decodes r's metadata entries, by name; an entry that does not
// decode is skipped.
func metasOf(r row.Row) []pickMeta {
	var out []pickMeta
	for kid, j := range r.Meta {
		e, err := meta.DecodeEntry(j)
		if err != nil {
			continue
		}
		v := string(e.V)
		var s string
		if json.Unmarshal(e.V, &s) == nil {
			v = s
		}
		out = append(out, pickMeta{keyID: kid.String(), name: e.Name, val: v, sensitive: e.Sensitive})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// withMeta adds each pick's metadata line and JSON map. A pick without
// metadata is unchanged. The registry flags are read once; failing to read
// them fails the command rather than printing a value unmasked (3.20).
func withMeta(ctx context.Context, res contextResult) (contextResult, error) {
	flags, err := coreApp.MetaKeyFlags(ctx)
	if err != nil {
		return res, coreErr(err)
	}
	for i := range res.picks {
		p := &res.picks[i]
		if len(p.metas) == 0 {
			continue
		}
		p.Meta = map[string]string{}
		parts := make([]string, 0, len(p.metas))
		for _, m := range p.metas {
			v := m.val
			if m.sensitive || flags.KeySensitive(m.keyID) {
				v = refpoint.Mask
			}
			p.Meta[m.name] = v
			parts = append(parts, m.name+"="+oneLine(v))
		}
		line := "meta: " + strings.Join(parts, " ") + "\n"
		p.full = strings.TrimSuffix(p.full, "\n") + line + "\n"
		p.line += "  " + line
		p.Bytes = len(p.full)
	}
	return res, nil
}
