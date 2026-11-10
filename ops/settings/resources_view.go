package settings

import (
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/store"
)

// ResourceViewer is an optional Env upgrade: the stores whose resources
// this place may read. wire.App answers only the repo store in a client
// repo (RES 3.9: no store resources in a client repo's reads, even on an
// agency machine with the store attached). An Env without it reads every
// attached store.
type ResourceViewer interface {
	ResourceStores() []store.Source
}

// resStores is where resource reads (show, list, lint, report, bulk
// matches, repoint, graph) look. Referrer checks and kind-field use checks
// still read every store: a store row may point at a client row, and a
// delete must not strand it.
func (o ops) resStores() []store.Source {
	if v, ok := o.env.(ResourceViewer); ok {
		return v.ResourceStores()
	}
	return engine.ReadStoresOf(o.env)
}
