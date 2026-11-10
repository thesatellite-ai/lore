package wire

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/ops/profilestore"
)

// The row counts of a duplicate actor (TEAM_PROFILES 6.4): App implements
// the profilestore.ActorRowCounter upgrade over every store this App has
// attached, read-only, so `actor contact add --merge` can show what the
// merge will make reports follow before the person confirms.

var _ profilestore.ActorRowCounter = (*App)(nil)

// ActorRowCounts implements profilestore.ActorRowCounter: the live rows
// whose envelope created_by is actor, per table, in every attached store
// (a row id seen in two stores counts once). A store or table that cannot
// be listed fails the read: the preview then says the counts could not be
// read rather than show a partial count as complete.
func (a *App) ActorRowCounts(ctx context.Context, actor ids.AnyID) ([]profilestore.TableCount, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	who := actor.String()
	seen := map[ids.AnyID]bool{}
	counts := map[string]int{}
	for _, src := range st.eng.Stores() {
		for _, def := range st.eng.Registry().Tables() {
			rows, lerr := st.eng.List(ctx, src, def.Name)
			if lerr != nil {
				return nil, lerr
			}
			for _, r := range rows {
				if r.Deleted || seen[r.ID] || r.CreatedBy.IsZero() || r.CreatedBy.String() != who {
					continue
				}
				seen[r.ID] = true
				counts[string(def.Name)]++
			}
		}
	}
	out := make([]profilestore.TableCount, 0, len(counts))
	for t, n := range counts {
		out = append(out, profilestore.TableCount{Table: t, Rows: n})
	}
	slices.SortFunc(out, func(x, y profilestore.TableCount) int { return strings.Compare(x.Table, y.Table) })
	return out, nil
}
