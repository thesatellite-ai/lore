package folder

import (
	"context"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
)

// FindInFolders implements engine.FolderFinder: the data folder of this
// store (any folder, not only the one this Backend opened) that holds
// table/id (rowfs.Root.FindRow).
func (b *Backend) FindInFolders(ctx context.Context, table row.Table, id ids.AnyID) (string, bool, error) {
	if err := b.usable(ctx); err != nil {
		return "", false, err
	}
	return b.root.FindRow(table, id)
}
