package wire

// meta_keyflags.go — the key registry's sensitive flags for printers that
// sit outside ops/settings (METADATA 3.20 "one masker"):
//
//	render_private ${meta:…} ─► (*App).MetaKeyFlags(ctx)
//	   ─► every live meta_keys row of every attached store, read once
//	   ─► refpoint.KeyFlags: sensitive by key id
//	refpoint.MetaOfWith(r, flags) masks when the entry OR the registry says so

import (
	"context"

	"github.com/thesatellite-ai/lore/logic/refpoint"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// metaKeySensitive is the meta_keys field holding the flag.
const metaKeySensitive = "sensitive"

// MetaKeyFlags returns the registry's sensitive flag by key id, read once
// from every attached store. An error (a store that cannot be listed) is
// returned, never swallowed: a printer that cannot know the flag must not
// guess "not sensitive".
func (a *App) MetaKeyFlags(ctx context.Context) (refpoint.KeyFlags, error) {
	sens := map[string]bool{}
	for _, src := range a.Stores() {
		rows, err := a.List(ctx, src, schema.TableMetaKeys)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if b, ok := r.Fields[metaKeySensitive].(value.Bool); ok && b.Bool() && !r.Deleted {
				sens[r.ID.String()] = true
			}
		}
	}
	return refpoint.KeyFlagsFunc(func(id string) bool { return sens[id] }), nil
}
