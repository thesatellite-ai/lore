package engine

// delete_overruled on an alive row (DELETION_DESIGN 3.7, DEL-MERGE-EDIT):
// a merge that kept a soft-deleted row alive because the other side edited
// it records the losing delete in delete_overruled. The field is a trash
// field written by the merge, not a table field, so the whole-row check
// (validate.Fields, Whole) would refuse every later edit of that row as
// "unknown field delete_overruled" (found by TestF04_DeleteVersusEdit…:
// `lore task done` on the kept row failed). The writes set it aside before
// the check and put it back after, so an edit keeps the note; restore and a
// new delete clear it (logic/trash Clear, Mark), `lore trash list
// --overruled` lists it.

import (
	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/model/value"
)

// setAsideOverruled returns fields without delete_overruled (a new map
// when it was present) and the note, nil when the row carries none.
func setAsideOverruled(fields map[string]value.Value) (map[string]value.Value, value.Value) {
	note, ok := fields[trash.FieldDeleteOverruled]
	if !ok || note == nil || note.Kind() == value.KindNull {
		return fields, nil
	}
	rest := make(map[string]value.Value, len(fields))
	for k, v := range fields {
		if k != trash.FieldDeleteOverruled {
			rest[k] = v
		}
	}
	return rest, note
}

// putBackOverruled returns fields with the note set aside by
// setAsideOverruled; fields itself when note is nil.
func putBackOverruled(fields map[string]value.Value, note value.Value) map[string]value.Value {
	if note == nil {
		return fields
	}
	out := make(map[string]value.Value, len(fields)+1)
	for k, v := range fields {
		out[k] = v
	}
	out[trash.FieldDeleteOverruled] = note
	return out
}
