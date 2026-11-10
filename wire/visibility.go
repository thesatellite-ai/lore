package wire

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/policy"
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
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
		m, ok := concrete(st.byName[name]).(markerOf)
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

// StoreLabel is how a row's store is shown beside it ("[label]"): the
// attached name, with "/<folder>" added when the store is attached at a
// non-root folder and its name does not already say so. v0.1.17 showed
// the org store's linked folder this way (`[northwind/acme]`), while the
// core attaches that folder under the bare org name so `--to northwind`
// keeps working. The repo store, a store attached at its root, a
// `<store>/<folder>` name, and a name this App does not know (or an App
// not opened yet) are returned unchanged.
func (a *App) StoreLabel(name store.Source) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.st == nil || strings.Contains(string(name), targetSep) {
		return string(name)
	}
	f := strings.Trim(a.st.folders[name], "/")
	if f == "" || f == rowfs.FolderRoot {
		return string(name)
	}
	return string(name) + targetSep + f
}

var _ opssettings.ResourceViewer = (*App)(nil)

// ResourceStores implements settings.ResourceViewer: in a client repo only
// the repo store (RES 3.9: a client repo never lists store resources, even
// on an agency machine with the store attached); elsewhere every attached
// store, as Stores.
func (a *App) ResourceStores() []store.Source {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.st == nil {
		return nil
	}
	if a.st.eng.IsClient(store.SourceRepo) {
		return []store.Source{store.SourceRepo}
	}
	return a.st.eng.Stores()
}

// ClientRepo reports whether the repo store is a client repo (opening the
// App if needed), for surfaces outside the core that list store rows
// (RES 3.9: the old external rows must leave resources out there).
func (a *App) ClientRepo(ctx context.Context) (bool, error) {
	st, err := a.state(ctx)
	if err != nil {
		return false, err
	}
	return st.eng.IsClient(store.SourceRepo), nil
}
