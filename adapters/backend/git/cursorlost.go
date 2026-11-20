package git

// cursorlost.go — a store clone moved behind the cache's cursor (owner
// P-5, devco D-763). The cache's cursor of a clone-mode store is the HEAD
// commit it last read; Read lists the row files changed between it and
// HEAD. After a hand reset of the clone to an older commit (or a clone
// made again from a remote that lost commits) HEAD no longer descends
// from the cursor: the files the reset dropped show as deleted in that
// diff, and deleted files are not deletes, so the cache kept rows the
// clone no longer has (search and sql listed them; doctor's Missing named
// them as lost files). CursorLost lets the engine see that and rebuild.

import (
	"context"

	"github.com/thesatellite-ai/lore/store"
)

// CursorLost implements store.CursorChecker: in a store clone, a commit
// cursor that is gone from the clone or is not an ancestor of HEAD is
// lost. The usual case (the cursor is HEAD) costs no git process; a cursor
// behind HEAD costs two (resolve, merge-base). Repo mode reads files by
// their stat cursor, which a reset cannot leave behind in this way.
func (b *Backend) CursorLost(ctx context.Context, cursor store.Cursor) (bool, error) {
	c := string(cursor)
	if b.mode != modeClone || !commitIDPattern.MatchString(c) {
		return false, nil
	}
	if err := b.usable(ctx); err != nil {
		return false, err
	}
	h, err := b.head(ctx)
	if err != nil || h == "" || h == c {
		return false, err
	}
	from, err := b.resolve(ctx, b.work, c)
	if err != nil {
		return false, err
	}
	if from == "" {
		return true, nil // the commit is gone (a clone made again): nothing to diff from
	}
	anc, err := b.isAncestor(ctx, from, h)
	return !anc, err
}
