package merge

import (
	"bytes"
	"testing"

	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// both runs Rows in both argument orders and fails unless the two results
// are the same bytes (DELETION 3.7: every machine merging the same three
// rows writes the same file, whichever side is "ours").
func both(t *testing.T, name string, base, a, b row.Row) Result {
	t.Helper()
	p := PolicyFor(testDef)
	r1, err := Rows(p, base, a, b)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	r2, err := Rows(p, base, b, a)
	if err != nil {
		t.Fatalf("%s (swapped): %v", name, err)
	}
	if !bytes.Equal(encode(t, r1.Row), encode(t, r2.Row)) {
		t.Fatalf("%s: result depends on argument order:\n%s\n%s", name, encode(t, r1.Row), encode(t, r2.Row))
	}
	return r1
}

// TestTrashMergeMatrixFull (DELETION 3.7) covers the rows of the merge
// table TestTrashMergeMatrix does not: purge (tombstone) beats every other
// state, two tombstones tie-break by bytes, a soft delete that also edited
// meets an edit, a restore meets a purge, two deletes at the same instant,
// and what does NOT count as an edit (audit-only and metadata-only).
func TestTrashMergeMatrixFull(t *testing.T) {
	base := live(stamp(0, actorA), text("body"), text("v1"), text("kind"), text("core"))
	edit := live(stamp(20, actorB), text("body"), text("v2"), text("kind"), text("core"))
	edit.UpdatedBy = actorB
	del := trashed(t, base, stamp(10, actorA), 10, actorA, "duplicate", batA)
	delLater := trashed(t, base, stamp(30, actorB), 30, actorB, "stale ✓", batB)
	purge := tomb(stamp(15, actorA))

	// Tombstone vs anything → tombstone (purge beats edit, delete, restore).
	restored := live(stamp(40, actorA), text("body"), text("v1"), text("kind"), text("core"))
	for name, other := range map[string]row.Row{
		"edited": edit, "soft-deleted": del, "unchanged": base, "restored": restored,
		"edited a new field": live(stamp(50, actorB), text("body"), text("v1"), text("kind"), text("core"), text("rank"), value.Int(3)),
	} {
		res := both(t, "tombstone vs "+name, base, purge, other)
		if !res.Row.Deleted {
			t.Errorf("tombstone vs %s: kept a live row", name)
		}
		if len(res.Row.Fields) != 0 {
			t.Errorf("tombstone vs %s: tombstone carries fields %v", name, res.Row.Fields)
		}
	}
	// Restored (base deleted) vs purged → tombstone.
	if got := both(t, "restored vs purged", del, restored, tomb(stamp(45, actorB))); !got.Row.Deleted {
		t.Error("restored vs purged: not a tombstone")
	}

	// Two tombstones with the same HLC wall but other actors: one wins,
	// the same one in either order (the HLC orders by actor, then bytes).
	t1, t2 := tomb(stamp(7, actorA)), tomb(stamp(7, actorB))
	res := both(t, "tombstones same wall", Absent, t1, t2)
	if want := earlierHLC(t1.HLC, t2.HLC); res.Row.HLC.Compare(want) != 0 {
		t.Errorf("same-wall tombstones kept %v, want the lower HLC %v", res.Row.HLC, want)
	}
	// Two tombstones, base alive: still the earlier.
	if got := both(t, "tombstones over a live base", base, tomb(stamp(9, actorB)), tomb(stamp(3, actorA))); got.Row.HLC.Compare(stamp(3, actorA)) != 0 {
		t.Errorf("tombstones over base kept %v", got.Row.HLC)
	}

	// Soft-deleted AND edited vs edited (other field): field merge, alive,
	// delete_overruled set, both edits kept.
	delEdit := live(stamp(12, actorA), text("body"), text("v1"), text("kind"), text("insight"))
	delEdit = trashed(t, delEdit, stamp(12, actorA), 12, actorA, "dup", batA)
	res = both(t, "deleted+edited vs edited", base, delEdit, edit)
	if _, deleted := trash.Read(res.Row); deleted {
		t.Error("deleted+edited vs edited: row still deleted")
	}
	if _, ok := res.Row.Fields[trash.FieldDeleteOverruled]; !ok {
		t.Error("deleted+edited vs edited: no delete_overruled")
	}
	if field(res.Row, "body") != "v2" || field(res.Row, "kind") != "insight" {
		t.Errorf("deleted+edited vs edited lost an edit: body %s kind %s", field(res.Row, "body"), field(res.Row, "kind"))
	}
	if res.Trash != TrashKeptEdit {
		t.Errorf("deleted+edited vs edited outcome %q", res.Trash)
	}

	// Two deletes at the same instant: the smaller batch id wins as one
	// unit (reason, by, batch from the same delete).
	sameA := trashed(t, base, stamp(10, actorA), 10, actorA, "reason a", batA)
	sameB := trashed(t, base, stamp(10, actorB), 10, actorB, "reason b", batB)
	res = both(t, "deleted both, same instant", base, sameA, sameB)
	in, deleted := trash.Read(res.Row)
	if !deleted || in.Batch != batA || in.Reason != "reason a" || in.By != actorA.String() {
		t.Errorf("same-instant deletes kept %+v, want batch a's whole delete", in)
	}
	if res.Trash != TrashDeletedBoth {
		t.Errorf("same-instant outcome %q", res.Trash)
	}
	// The later delete never leaks a field into the kept unit.
	res = both(t, "deleted both, later has a reason", base, del, delLater)
	if kept, _ := trash.Read(res.Row); kept.Reason != "duplicate" || kept.Batch != batA {
		t.Errorf("earlier delete not kept as one unit: %+v", kept)
	}

	// The same delete on both sides (one batch synced twice): no outcome.
	if got := both(t, "same delete twice", base, del, del); got.Trash != "" {
		t.Errorf("same delete twice reported %q", got.Trash)
	}

	// What is NOT an edit: an audit-only touch (newer HLC, same fields)
	// and a metadata-only edit: the delete wins (DEL-MERGE-EDIT).
	touched := base
	touched.HLC, touched.UpdatedAt, touched.UpdatedBy = stamp(25, actorB), stamp(25, actorB).Wall, actorB
	metaOnly := touched
	key, err := ids.Parse[ids.MetaKey]("mky_01936f3300007d4e8f5a6b7c8d9e0f99")
	if err != nil {
		t.Fatal(err)
	}
	mv, err := value.JSONOf([]byte(`{"v":"acme"}`))
	if err != nil {
		t.Fatal(err)
	}
	metaOnly.Meta = map[ids.ID[ids.MetaKey]]value.JSON{key: mv}
	for name, other := range map[string]row.Row{"audit-only touch": touched, "metadata-only edit": metaOnly} {
		got := both(t, "deleted vs "+name, base, del, other)
		if _, deleted := trash.Read(got.Row); !deleted {
			t.Errorf("deleted vs %s: the delete lost (%v)", name, got.Row.Fields)
		}
		if _, ok := got.Row.Fields[trash.FieldDeleteOverruled]; ok {
			t.Errorf("deleted vs %s: delete_overruled set", name)
		}
	}

	// Both created (no base), one side deleted: an edit by definition,
	// the row stays alive.
	addAdd := both(t, "add/add, one deleted", Absent, del, edit)
	if _, d := trash.Read(addAdd.Row); d {
		t.Error("add/add with one delete: the delete won over a fresh row")
	}
}

func earlierHLC(a, b hlc.HLC) hlc.HLC {
	if a.Compare(b) <= 0 {
		return a
	}
	return b
}
