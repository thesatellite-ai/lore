package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/store"
)

// StoreIdentity is one attached store's permanent identity as the private
// render (STORE_SERVER_SPEC 7.3, CFG6) names its file:
// ~/.lore/render/<repo_id>/<ID>.md. Several attached sources (an org
// store's two folders) share one ID.
type StoreIdentity struct {
	// Name is the attached source name (engine Stores() spelling).
	Name store.Source
	// ID is the sto_ id from lore-store.yaml; "" for a v0.1.17 marker or
	// a backend without one.
	ID string
	// Folder is the attached folder ("" = the store root).
	Folder string
	// Shareable is true when the marker reads visibility: shareable
	// (absent = internal, fail closed).
	Shareable bool
}

// StoreIdentities lists every attached store except store.SourceRepo, in
// the engine's Stores() order.
func (a *App) StoreIdentities(ctx context.Context) ([]StoreIdentity, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	var out []StoreIdentity
	for _, name := range st.eng.Stores() {
		if name == store.SourceRepo {
			continue
		}
		si := StoreIdentity{Name: name, Folder: st.folders[name]}
		if m, ok := st.byName[name].(markerOf); ok {
			mk := m.Marker()
			if !mk.ID.IsZero() {
				si.ID = mk.ID.String()
			}
			si.Shareable = mk.Visibility == rowfs.VisibilityShareable
		}
		out = append(out, si)
	}
	return out, nil
}
