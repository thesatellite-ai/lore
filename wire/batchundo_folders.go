package wire

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	opsbatch "github.com/thesatellite-ai/lore/ops/batch"
	"github.com/thesatellite-ai/lore/store"
)

// attachBatchFolders attaches, for this one command, every store folder
// the batch `lore batch undo --id <bat_>` names wrote into and this repo
// does not attach (devco:D-925). Why: a batch run from a code repo that
// wrote into a folder of the personal store (`lore batch apply` with
// `"to": "me/notes"` lines) or of an org store this repo does not attach
// was undone from the same repo as "already deleted" for every row,
// because undo writes each row in the store the batch log names and that
// store was not open here; `lore memory delete --id <row>` from the same
// folder found it (idfolder.go, D-755). One row id per store is enough:
// finding it attaches its whole folder. Any other operation: nothing.
func (a *App) attachBatchFolders(ctx context.Context, e op.Entry, d op.Decoder) {
	if e.Spec().Name != opsbatch.OpUndo {
		return
	}
	in, err := e.Decode(d)
	if err != nil {
		return
	}
	raw, _ := in.String("id")
	bid, err := ids.Parse[ids.Batch](strings.TrimSpace(raw))
	if err != nil {
		return // the operation refuses the id itself
	}
	evs, err := a.BatchEvents(ctx)
	if err != nil {
		return // the operation reports the unreadable log itself
	}
	eng, err := a.Engine(ctx)
	if err != nil {
		return
	}
	open := eng.Stores()
	var done []store.Source
	for _, ev := range evs {
		if ev.Batch != bid || slices.Contains(open, ev.Store) || slices.Contains(done, ev.Store) {
			continue
		}
		done = append(done, ev.Store)
		_, _ = a.attachFolderOfRow(ctx, ev.ID.String()) // not found anywhere: undo names the row as it does today
	}
}
