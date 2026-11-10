package git

// doctorprobe.go — the read-only views `lore doctor` takes of a git store
// (CLI_REFERENCE 3.7): who holds its lock, and where a row file sits in the
// git tree so doctor can read the row's history.

import (
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
)

// LockHolder probes the store lock (clone mode: the clone's lock beside
// the worktree; repo mode: the repo's .lore/lock) without waiting.
func (b *Backend) LockHolder() (rowfs.LockState, error) { return b.root.LockHolder() }

// RowFile is where the row file of (table, id) sits in this store's git
// checkout: dir is the worktree (clone mode) or the repo top (repo mode),
// rel the slash path relative to dir. The file need not exist.
func (b *Backend) RowFile(table row.Table, id ids.AnyID) (dir, rel string) {
	return b.root.Dir(), b.folder.StorePath(table, id)
}

// WorkDir is the git checkout this store commits in: the clone's worktree,
// or the repo top in repo mode.
func (b *Backend) WorkDir() string { return b.root.Dir() }
