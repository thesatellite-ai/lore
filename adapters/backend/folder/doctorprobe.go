package folder

// doctorprobe.go — `lore doctor`'s read-only view of a folder store's lock
// (CLI_REFERENCE 3.7: doctor names the holder of a held store lock).

import "github.com/thesatellite-ai/lore/adapters/backend/rowfs"

// LockHolder probes the store's .lore/lock without waiting.
func (b *Backend) LockHolder() (rowfs.LockState, error) { return b.root.LockHolder() }
