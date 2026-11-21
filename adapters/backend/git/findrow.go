package git

import (
	"context"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
)

// FindInFolders implements engine.FolderFinder: the data folder of this
// clone's worktree (any folder, not only the one this Backend opened)
// that holds table/id (rowfs.Root.FindRow). The engine uses it for F15 on
// a pointer at a row of a folder the repo does not attach.
func (b *Backend) FindInFolders(ctx context.Context, table row.Table, id ids.AnyID) (string, bool, error) {
	if err := b.usable(ctx); err != nil {
		return "", false, err
	}
	if b.mode == modeRepo {
		// The code repo is one store folder of its own (no sibling data
		// folders), and walking a whole code tree would be slow.
		return "", false, nil
	}
	return b.root.FindRow(table, id)
}

// ReadSiblingTable is the rows of table in every other data folder of this
// clone's worktree (rowfs.Root.ReadSiblingTable): the reference resolver
// reads the resources and kinds of a personal store's other folders
// through it without opening them (devco D-793). A code repo has none.
func (b *Backend) ReadSiblingTable(ctx context.Context, table row.Table) ([]rowfs.FolderRows, error) {
	if err := b.usable(ctx); err != nil {
		return nil, err
	}
	if b.mode == modeRepo {
		return nil, nil
	}
	return b.root.ReadSiblingTable(ctx, b.folder.Name(), table)
}
