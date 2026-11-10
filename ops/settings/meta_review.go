package settings

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/schema"
)

// UnreviewedKey is one metadata key an agent registered that no one has
// reviewed yet (MQ7): what the `lore inbox` review line names
// (AGENT_IDENTITY 8.3, METADATA 3.14).
type UnreviewedKey struct {
	// Name is the key name.
	Name string
	// Store is the attached store the key row lives in.
	Store string
	// CreatedBy is the actor id that wrote the key row (an agent actor,
	// whose owner the inbox names); empty on a row without the audit field.
	CreatedBy string
	// CreatedAt is when the key row was written.
	CreatedAt time.Time
}

// UnreviewedKeys lists every live key row of the attached stores that
// `lore meta keys --unreviewed` lists (the same isUnreviewed test, so the
// inbox count and that list never disagree), sorted by name then store.
func UnreviewedKeys(ctx context.Context, env Env) ([]UnreviewedKey, error) {
	rows, err := liveRows(ctx, env, engine.ReadStoresOf(env), schema.TableMetaKeys)
	if err != nil {
		return nil, err
	}
	var out []UnreviewedKey
	for _, k := range rows {
		if !isUnreviewed(k.Row) {
			continue
		}
		by := ""
		if !k.Row.CreatedBy.IsZero() {
			by = k.Row.CreatedBy.String()
		}
		out = append(out, UnreviewedKey{Name: text(k.Row, mName), Store: string(k.Src), CreatedBy: by, CreatedAt: k.Row.CreatedAt})
	}
	slices.SortFunc(out, func(a, b UnreviewedKey) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.Store, b.Store))
	})
	return out, nil
}
