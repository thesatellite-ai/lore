package wire

// meta_keysblock.go — the data of the rendered keys block (METADATA 3.14,
// FA-META-10) for the render command, which sits outside the core:
//
//	render ─► (*App).MetaKeysBlock(ctx, sources)
//	   ─► live meta_keys rows of those stores, internal ones left out (3.11)
//	   ─► use counts: every core table's rows of those stores, per key id
//	   ─► meta.KeysBlock(keys, meta.render_keys_budget of the repo's chain)

import (
	"context"

	"github.com/thesatellite-ai/lore/logic/direction"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// The meta_keys fields the block reads.
const (
	metaKeyName, metaKeyType, metaKeyDesc = "name", "type", "description"
	metaKeyReviewed, metaKeyInternal      = "reviewed", "internal"
	metaKeyMergedInto                     = "merged_into"
)

// MetaKeysBlock is the markdown keys block of the keys the given stores
// define ("" when none), for a rendered file that holds those stores'
// rows (the caller passes the same sources it renders, e.g. the repo and
// its shareable stores). Keys marked internal never appear (3.11); a
// sensitive key shows its name only (3.20); a key defined in a
// client-class store shows no description (X17). Errors are returned, so
// a render never prints a block it could not read in full.
func (a *App) MetaKeysBlock(ctx context.Context, sources []store.Source) (string, error) {
	byID := map[string]*meta.KeyInfo{}
	var order []string
	for _, src := range sources {
		rows, err := a.List(ctx, src, schema.TableMetaKeys)
		if err != nil {
			return "", err
		}
		client := false
		if len(rows) > 0 {
			class, cerr := a.StoreClass(ctx, src)
			if cerr != nil {
				return "", cerr
			}
			client = class == direction.Client
		}
		for _, r := range rows {
			if r.Deleted || keyBool(r.Fields[metaKeyInternal]) || keyText(r.Fields[metaKeyMergedInto]) != "" {
				continue
			}
			id := r.ID.String()
			if _, seen := byID[id]; seen {
				continue
			}
			byID[id] = &meta.KeyInfo{Name: keyText(r.Fields[metaKeyName]), Type: keyText(r.Fields[metaKeyType]),
				Description: keyText(r.Fields[metaKeyDesc]), Reviewed: keyBool(r.Fields[metaKeyReviewed]),
				Sensitive: keyBool(r.Fields[metaKeySensitive]), ClientAuthored: client, Uses: map[string]int{}}
			order = append(order, id)
		}
	}
	if len(order) == 0 {
		return "", nil
	}
	for _, src := range sources {
		for _, t := range CoreTables() {
			rows, err := a.List(ctx, src, t)
			if err != nil {
				return "", err
			}
			for _, r := range rows {
				if r.Deleted {
					continue
				}
				for kid := range r.Meta {
					if k, ok := byID[kid.String()]; ok {
						k.Uses[t.String()]++
					}
				}
			}
		}
	}
	keys := make([]meta.KeyInfo, 0, len(order))
	for _, id := range order {
		keys = append(keys, *byID[id])
	}
	budget := int64(meta.RenderKeysBudget)
	pol, err := a.Policy(ctx, store.SourceRepo)
	if err != nil {
		return "", err
	}
	if n, ok := pol.Limit(policy.KeyMetaRenderKeysBudget); ok && n > 0 {
		budget = n // FA-META-11: the team's meta.render_keys_budget
	}
	return meta.KeysBlock(keys, int(budget)), nil
}

// keyText is a text field's value ("" when absent or not text).
func keyText(v value.Value) string {
	if v == nil || v.Kind() != value.KindText {
		return ""
	}
	return v.String()
}

// keyBool is a bool field's value (false when absent).
func keyBool(v value.Value) bool {
	b, ok := v.(value.Bool)
	return ok && b.Bool()
}
