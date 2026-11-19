package wire

// cloneforeign.go — a lore home copied from another one (devco D-534).
//
// state/store-clones.json of an older lore named clones by absolute path,
// set-url's `why: moved` included even for a clone at its default place.
// A home made by copying ~/.lore (a preview home) then fetched and wrote
// in the ORIGINAL home's clones. ReadCloneRecord now ignores such an
// entry; New drops it from the record and says so once, naming the clone
// this home uses instead.

import (
	"fmt"
	"io"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
)

// dropForeignClones removes the record entries that name another home's
// clones and prints one line for each (nothing when there is none).
func dropForeignClones(home string, w io.Writer) {
	if home == "" {
		return
	}
	found, err := gitbackend.DropForeignClones(home)
	for _, f := range found {
		own := "its default place"
		if def, ok := gitbackend.DefaultCloneDirOfKey(home, f.Key); ok {
			own = underHome(def)
		}
		_, _ = fmt.Fprintln(w, "lore: warning: "+gitbackend.CloneRecordFile+" named "+f.Path+" for store "+f.Key+", a clone under another lore home (this home was copied from it); ignored, this home uses its own clone at "+own)
	}
	if err != nil {
		_, _ = fmt.Fprintln(w, "lore: warning: could not update "+gitbackend.CloneRecordPath(home)+" (the entries above are ignored anyway): "+err.Error())
	}
}
