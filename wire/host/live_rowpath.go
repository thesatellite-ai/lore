package host

// live_rowpath.go — where a row read through the live overlay comes from,
// for `lore path` and `lore edit` (wire.LiveCopier): a row whose newest
// version is on the live ref (refs/lore/live) has no file in the working
// tree's .lore/data yet, so its path there would show an older copy or
// nothing at all.

import (
	"context"
	"errors"

	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

var _ wire.LiveCopier = (*liveBackend)(nil)

// LiveCopy implements wire.LiveCopier: ok is true when Get answers
// table/id from the live ref (its live version is newer than the branch
// file, or there is no branch file), or from the remote default branch's
// copy that is not pulled yet.
func (b *liveBackend) LiveCopy(ctx context.Context, table row.Table, id ids.AnyID) (wire.LiveCopy, bool, error) {
	s := b.session(ctx)
	if s == nil || !readsLive(s, table) {
		return wire.LiveCopy{}, false, nil
	}
	k := live.RowKey{Table: string(table), ID: id.String()}
	_, files, err := b.liveFiles(ctx, s)
	if err != nil {
		return wire.LiveCopy{}, false, err
	}
	branchRow, berr := b.inner.Get(ctx, table, id)
	if berr != nil && !errors.Is(berr, store.ErrNotFound) {
		return wire.LiveCopy{}, false, berr
	}
	f, onLive := files[k]
	if !onLive {
		if _, landed := b.landedCopy(ctx, s, k, branchRow, berr == nil); landed {
			return wire.LiveCopy{Ref: "refs/remotes/" + s.ref.RemoteName + "/" + s.defaultBranch(ctx),
				Path: mainDataPath(b.folder, k.Table+"/"+k.ID+rowFileExt), NotPulled: true}, true, nil
		}
		return wire.LiveCopy{}, false, nil
	}
	lr, err := liveStoreRow(k, f)
	if err != nil {
		return wire.LiveCopy{}, false, err
	}
	if berr == nil && branchRow.HLC.Compare(lr.HLC) > 0 { // newer's rule
		return wire.LiveCopy{}, false, nil // the branch file is the newer version
	}
	return wire.LiveCopy{Ref: s.ref.Name, Path: live.RowPath(b.folder, k.Table, k.ID)}, true, nil
}
