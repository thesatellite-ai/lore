package wire

import (
	"context"
	"slices"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/store"
)

// ShareableStores lists the attached stores (never store.SourceRepo) whose
// lore-store.yaml marker reads visibility: shareable (CFG4/CFG6: a committed
// render holds repo rows plus these stores only). A backend without a
// marker, or a marker without a visibility, counts as internal (fail
// closed). Order is the engine's Stores() order.
func (a *App) ShareableStores(ctx context.Context) ([]store.Source, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	var out []store.Source
	for _, name := range st.eng.Stores() {
		if name == store.SourceRepo {
			continue
		}
		m, ok := st.byName[name].(markerOf)
		if ok && m.Marker().Visibility == rowfs.VisibilityShareable {
			out = append(out, name)
		}
	}
	return out, nil
}

// StoreLevel implements settings.Leveler: the attached store's settings
// level (engine.Level); ok=false before the App opened or for a store that
// is not attached.
func (a *App) StoreLevel(name store.Source) (policy.Level, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.st == nil || !slices.Contains(a.st.eng.Stores(), name) {
		return "", false
	}
	return a.st.eng.Level(name), true
}
