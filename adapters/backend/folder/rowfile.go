package folder

import (
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
)

// RowFile is where the row file of (table, id) sits in this folder store:
// dir is the store root, rel the slash path relative to it (the same
// answer as the git backend's RowFile, so `lore path` asks one question of
// either). The file need not exist.
func (b *Backend) RowFile(table row.Table, id ids.AnyID) (dir, rel string) {
	return b.root.Dir(), b.folder.StorePath(table, id)
}
