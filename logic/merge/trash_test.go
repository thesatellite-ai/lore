package merge

import (
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
)

// trashed is r soft-deleted at ms by a in batch bat (trash.Mark).
func trashed(t *testing.T, r row.Row, h hlc.HLC, ms int, a ids.ID[ids.Actor], reason, bat string) row.Row {
	t.Helper()
	b, err := ids.Parse[ids.Batch](bat)
	if err != nil {
		t.Fatal(err)
	}
	r.HLC = h
	out, err := trash.Mark(r, trash.Stamp{At: t0.Add(time.Duration(ms) * time.Millisecond), By: a, Reason: reason, Batch: b})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

const (
	batA = "bat_01936f3300007d4e8f5a6b7c8d9e0f01"
	batB = "bat_01936f3300007d4e8f5a6b7c8d9e0f02"
)

// TestTrashMergeMatrix (DELETION 3.7): every soft-delete row of the merge
// table, in both argument orders (the outcome must not depend on which side
// is "ours").
func TestTrashMergeMatrix(t *testing.T) {
	p := PolicyFor(testDef)
	base := live(stamp(0, actorA), text("body"), text("v1"))
	edit := live(stamp(20, actorB), text("body"), text("v2"))
	edit.UpdatedBy = actorB
	del := trashed(t, base, stamp(10, actorA), 10, actorA, "duplicate", batA)
	delLater := trashed(t, base, stamp(30, actorB), 30, actorB, "stale", batB)
	restored := live(stamp(40, actorA), text("body"), text("v1"))
	cases := []struct {
		name              string
		base, ours, their row.Row
		alive             bool
		overruled         bool
		batch             string
		outcome           TrashOutcome
	}{
		{"deleted vs edited: edit wins", base, del, edit, true, true, "", TrashKeptEdit},
		{"deleted vs unchanged: deleted", base, del, base, false, false, batA, ""},
		{"deleted on both sides: earlier wins", base, del, delLater, false, false, batA, TrashDeletedBoth},
		{"restored vs unchanged delete: alive", del, restored, del, true, false, "", ""},
		{"restored vs deleted again: alive", del, restored, delLater, true, true, "", TrashKeptRestore},
	}
	for _, c := range cases {
		for _, swap := range []bool{false, true} {
			o, th := c.ours, c.their
			if swap {
				o, th = th, o
			}
			res, err := Rows(p, c.base, o, th)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			in, deleted := trash.Read(res.Row)
			_, hasNote := res.Row.Fields[trash.FieldDeleteOverruled]
			switch {
			case deleted == c.alive:
				t.Errorf("%s (swap %v): deleted = %v", c.name, swap, deleted)
			case hasNote != c.overruled:
				t.Errorf("%s (swap %v): delete_overruled = %v", c.name, swap, hasNote)
			case !c.alive && in.Batch != c.batch:
				t.Errorf("%s (swap %v): batch %q, want %q", c.name, swap, in.Batch, c.batch)
			case res.Trash != c.outcome:
				t.Errorf("%s (swap %v): outcome %q, want %q", c.name, swap, res.Trash, c.outcome)
			}
			if c.outcome == TrashKeptEdit && res.Row.Fields["body"].String() != "v2" {
				t.Errorf("%s: the edit was lost: %v", c.name, res.Row.Fields["body"])
			}
			if hasNote && !strings.Contains(res.Row.Fields[trash.FieldDeleteOverruled].String(), `"deleted_batch":"bat_`) {
				t.Errorf("%s: note = %s", c.name, res.Row.Fields[trash.FieldDeleteOverruled])
			}
		}
	}
}
