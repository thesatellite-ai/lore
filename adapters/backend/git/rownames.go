package git

import (
	"context"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/store"
)

// RowNames implements store.RowNamer for a store clone: the row files of
// this folder at HEAD (git ls-tree, no blob read) plus the files the
// roster replay holds (a held removal is still served by a full read). A
// code repo's folder answers ok false: its reads come from the files on
// disk and its row mirror, and the caller reads the rows.
func (b *Backend) RowNames(ctx context.Context) ([]store.RowName, bool, error) {
	if b.mode != modeClone {
		return nil, false, nil
	}
	if err := b.usable(ctx); err != nil {
		return nil, false, err
	}
	h, err := b.head(ctx)
	if err != nil {
		return nil, false, err
	}
	if h == "" {
		return nil, true, nil // no commit yet: no rows
	}
	paths, err := b.changedPaths(ctx, "", h, b.folder.Pathspecs())
	if err != nil {
		return nil, false, err
	}
	auth, err := b.rowAuthorityOf(ctx, h)
	if err != nil {
		return nil, false, err
	}
	if auth != nil {
		for p := range auth.held {
			paths = append(paths, p) // a name twice is harmless: names may be more than the read
		}
	}
	out := make([]store.RowName, 0, len(paths))
	for _, p := range paths {
		rel, ok := b.folder.FileRel(p)
		if !ok {
			continue
		}
		if table, id, ok := rowfs.ParseRowPath(rel); ok {
			out = append(out, store.RowName{Table: table, ID: id})
		}
	}
	return out, true, nil
}
