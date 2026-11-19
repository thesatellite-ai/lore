package wire

// showorigin.go — what `lore show` asks the App beyond the row itself
// (devco D-455, D-456): the name of the store an `_o.store` sto_ id
// names, and the rows that point at the shown row.

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// storesSubdir is the folder of store clones under the lore home.
const storesSubdir = "stores"

// StoreName implements ops/row.StoreNamer: the attached store whose
// marker carries id, named by its clone's place under the home
// (`<org>/<store>`, the folders `lore setup` cloned it into), else by the
// name it is attached under. ok is false when no attached store has id.
func (a *App) StoreName(ctx context.Context, id string) (string, bool) {
	st, err := a.state(ctx)
	if err != nil {
		return "", false
	}
	names := make([]store.Source, 0, len(st.byName))
	for n := range st.byName {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		b := st.byName[n]
		mk, ok := currentMarker(b)
		if !ok || mk.ID.IsZero() || mk.ID.String() != id {
			continue
		}
		if d, dok := concrete(b).(storeDirOf); dok {
			if place := clonePlaceName(a.LoreHome(), d.Dir()); place != "" {
				return place, true
			}
		}
		base, _, _ := strings.Cut(string(n), targetSep)
		return base, true
	}
	return "", false
}

// clonePlaceName is dir's place under <home>/stores (`<org>/<store>`), ""
// when dir is no store clone of that home.
func clonePlaceName(home, dir string) string {
	if home == "" || dir == "" {
		return ""
	}
	rel, err := filepath.Rel(filepath.Join(home, storesSubdir), dir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return ""
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) > 2 {
		parts = parts[:2]
	}
	return strings.Join(parts, "/")
}

// Referrers implements ops/row.Referrers: the live rows of the attached
// stores whose fields name id (engine.Referrers).
func (a *App) Referrers(ctx context.Context, id ids.AnyID) ([]engine.Referrer, error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return nil, err
	}
	return eng.Referrers(ctx, id)
}
