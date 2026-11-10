package engine

import "github.com/thesatellite-ai/lore/store"

// ReadOnly reports whether the named store is attached read-only
// (`read_only: true` in stores.yaml, spec 7.1): a write to it is
// E_STORE_READ_ONLY (writable). Callers that pick a target on their own,
// such as the machine profile (OV59), skip such a store instead of
// failing on it. An unknown name is not read-only.
func (e *Engine) ReadOnly(name store.Source) bool { return e.stores[name].ReadOnly }
