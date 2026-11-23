package wire

// idcopies.go — a command by id sees every copy of the row the repo's
// lists see (devco:D-934, bug hunt H-195).
//
// A store attached whole (a stores.yaml entry with no folder) opens its
// root folder only; lists, search and sql also read every folder of it
// (readscope.go, lensFolders, D-484). `lore store adopt --target
// <store>/<folder>` copies the repo's rows into such a folder keeping
// their ids, so after an edit of the repo copy the list showed the newest
// copy while `lore show --id X` answered the repo copy without a word
// (the folder copy was out of its sight), and an edit or move by id could
// never be told that a second copy exists.
//
//	op with <id> ─▶ an attached store holds it live
//	             └▶ a folder of a store attached whole holds a live
//	                <id>.json too ─▶ attach that folder (read-only, this
//	                command only) ─▶ the engine sees both copies:
//	                E_AMBIGUOUS_ID naming the newest copy (pickCopy)
//
// Only the folders lists read: an org folder this repo does not attach,
// or a personal folder, is another project's place (an id an older lore
// derived without the folder repeats there as a different row, D-689),
// and stays out of a lookup that already found the row here.

import (
	"context"
	"path/filepath"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/store"
)

// attachCopiesBeside attaches, for this command, every folder of a store
// the repo attaches whole that holds a live row file of id (the folders a
// list reads beside the attached stores). It reports whether it attached
// any; the next Engine call reopens with them.
func (a *App) attachCopiesBeside(ctx context.Context, id string) bool {
	attached := false
	for _, f := range a.lensFolders(ctx) {
		dir, err := a.storeDir(ctx, attach.Store{Name: f.Name, URL: f.URL, Branch: f.Branch})
		if err != nil {
			continue // a store not cloned here has no folders to read
		}
		hits, _ := filepath.Glob(filepath.Join(dir, filepath.FromSlash(f.Folder), "*", id+rowFileExt))
		if len(hits) == 0 {
			continue
		}
		if live, _ := liveHit(hits, id, string(f.Name)); !live {
			continue // a tombstone there is no second copy
		}
		a.attachExtra(attach.Store{Name: store.Source(f.Name), URL: f.URL, Folder: f.Folder, Branch: f.Branch, ReadOnly: true})
		attached = true
	}
	return attached
}
