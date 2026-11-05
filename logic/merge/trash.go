package merge

// Soft-delete merge rules (DELETION_DESIGN 3.7, owner: "delete versus edit:
// the edit wins and the row returns with a note"). A soft-deleted row is an
// ordinary row file, so fields() first merges it field by field; settleTrash
// then fixes the cases a per-field pick gets wrong:
//
//	deleted │ edited (other field)        → alive, delete_overruled set
//	deleted │ deleted                     → deleted; the EARLIER delete's
//	                                        four fields win as one unit
//	restored│ deleted again (new batch)   → alive, delete_overruled set
//	restored│ unchanged, deleted│unchanged→ the per-field pick (one side changed)
//
// Tombstones (purges) are settled before fields() runs (tombstone wins).

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// TrashOutcome names what settleTrash did, for the merge report (3.7
// "Reported as"); "" when no soft-delete rule applied.
type TrashOutcome string

// The soft-delete outcomes of one merge.
const (
	TrashKeptEdit      TrashOutcome = "deleted on one side, edited on the other: kept alive (delete_overruled)"
	TrashKeptRestore   TrashOutcome = "restored on one side, deleted again on the other: kept alive (delete_overruled)"
	TrashDeletedBoth   TrashOutcome = "deleted on both sides: the earlier delete kept"
	overruledTimestamp              = time.RFC3339
)

// deleteFields are the four fields one delete writes as a unit.
var deleteFields = []string{trash.FieldDeletedAt, trash.FieldDeletedBy, trash.FieldDeletedReason, trash.FieldDeletedBatch}

// overruledNote is the delete_overruled object (3.7), keys in sorted order.
type overruledNote struct {
	At            string `json:"at"`
	DeletedAt     string `json:"deleted_at"`
	DeletedBatch  string `json:"deleted_batch,omitempty"`
	DeletedBy     string `json:"deleted_by,omitempty"`
	DeletedReason string `json:"deleted_reason,omitempty"`
	KeptFor       string `json:"kept_for,omitempty"`
}

// settleTrash applies the soft-delete rules to res (fields() output) for
// the sides base, ours, theirs; newer is the newer side (its HLC stamps the
// note, so both machines write the same bytes).
func settleTrash(res *Result, base, ours, theirs, newer row.Row) {
	dB, dO, dT := trash.Is(base), trash.Is(ours), trash.Is(theirs)
	switch {
	case dO && dT:
		if sameDelete(ours, theirs) {
			return
		}
		win := ours
		if earlierDelete(theirs, ours) {
			win = theirs
		}
		setDelete(&res.Row, win)
		res.Trash = TrashDeletedBoth
	case dO != dT:
		del, alive := ours, theirs
		if dT {
			del, alive = theirs, ours
		}
		switch {
		case dB && !sameDelete(del, base): // restored vs deleted again
			keepAlive(&res.Row, del, alive, newer)
			res.Trash = TrashKeptRestore
		case !dB && edited(base, alive): // deleted vs edited: the edit wins
			keepAlive(&res.Row, del, alive, newer)
			res.Trash = TrashKeptEdit
		}
	}
}

// edited reports whether side changed a field other than the delete and
// audit fields since base (3.7 "what edited means"; the audit fields are
// envelope members, not Fields). No base (both created): edited.
func edited(base, side row.Row) bool {
	if base.ID.IsZero() {
		return true
	}
	for _, k := range keys(base.Fields, side.Fields) {
		if isTrashField(k) {
			continue
		}
		if !value.Equal(base.Fields[k], side.Fields[k]) {
			return true
		}
	}
	return false
}

// isTrashField reports a delete field (deleted_*, delete_overruled).
func isTrashField(k string) bool {
	for _, f := range trash.Fields() {
		if k == f {
			return true
		}
	}
	return false
}

// sameDelete reports whether a and b carry the same four delete fields.
func sameDelete(a, b row.Row) bool {
	for _, k := range deleteFields {
		if !value.Equal(a.Fields[k], b.Fields[k]) {
			return false
		}
	}
	return true
}

// earlierDelete reports whether a was deleted before b (ties: the smaller
// batch id, so the choice never depends on the argument order).
func earlierDelete(a, b row.Row) bool {
	ia, _ := trash.Read(a)
	ib, _ := trash.Read(b)
	if !ia.At.Equal(ib.At) {
		return ia.At.Before(ib.At)
	}
	return strings.Compare(ia.Batch, ib.Batch) < 0
}

// setDelete copies win's four delete fields onto out as one unit.
func setDelete(out *row.Row, win row.Row) {
	f := cloneFields(out.Fields)
	for _, k := range deleteFields {
		if v, ok := win.Fields[k]; ok && v != nil {
			f[k] = v
		} else {
			delete(f, k)
		}
	}
	out.Fields = f
}

// keepAlive clears the delete fields of out and records the losing delete
// of del in delete_overruled, kept for alive's last writer.
func keepAlive(out *row.Row, del, alive, newer row.Row) {
	in, _ := trash.Read(del)
	note := overruledNote{At: newer.HLC.Wall.UTC().Format(overruledTimestamp), DeletedBatch: in.Batch, DeletedBy: in.By, DeletedReason: in.Reason}
	if !in.At.IsZero() {
		note.DeletedAt = in.At.UTC().Format(overruledTimestamp)
	}
	if !alive.UpdatedBy.IsZero() {
		note.KeptFor = alive.UpdatedBy.String()
	}
	f := cloneFields(out.Fields)
	for _, k := range trash.Fields() {
		delete(f, k)
	}
	if raw, err := json.Marshal(note); err == nil {
		if v, jerr := value.JSONOf(raw); jerr == nil {
			f[trash.FieldDeleteOverruled] = v
		}
	}
	out.Fields = f
}

// cloneFields copies a field map (nil → empty).
func cloneFields(m map[string]value.Value) map[string]value.Value {
	f := make(map[string]value.Value, len(m)+1)
	for k, v := range m {
		f[k] = v
	}
	return f
}
