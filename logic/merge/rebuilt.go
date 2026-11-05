package merge

import (
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// fieldRebuilt marks a meta_keys row recreated by `lore meta rebuild`
// from data rows (METADATA 3.8, 3.18; schema field "rebuilt").
const fieldRebuilt = "rebuilt"

// replaceRebuilt settles a meta_keys merge where exactly one side is a
// rebuilt guess and the other the real key row: the real row wins whole,
// flags included, whatever the HLCs say (METADATA 3.18 step 6, R6), so a
// guessed row can never flip the real key to internal for good. ok is
// false for every other pair (both rebuilt, both real, a tombstone on
// either side: the normal rules decide).
func replaceRebuilt(ours, theirs row.Row) (row.Row, bool) {
	if ours.Table != schema.TableMetaKeys || ours.Deleted || theirs.Deleted {
		return row.Row{}, false
	}
	o, t := isRebuilt(ours), isRebuilt(theirs)
	switch {
	case o && !t:
		return theirs, true
	case t && !o:
		return ours, true
	}
	return row.Row{}, false
}

// isRebuilt reports rebuilt: true on r.
func isRebuilt(r row.Row) bool {
	b, ok := r.Fields[fieldRebuilt].(value.Bool)
	return ok && b.Bool()
}
