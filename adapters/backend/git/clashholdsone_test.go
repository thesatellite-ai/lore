package git

import (
	"bytes"
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// TestClashHoldsOnlyItsRow (devco O-5, O-6): an offline clone edited two
// rows; only one of them clashes with the remote. The sync sends the one
// that does not clash at once (the other clone sees it) and holds the
// clashing one, named. Taking the remote's copy of the held row shows it
// here at once, before any sync, and the next syncs settle both clones
// alike.
func TestClashHoldsOnlyItsRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote := newRemote(t, true)
	a, b := clone(t, remote), clone(t, remote)
	ga, gb := newGen(t), newGen(t)
	x := ga.row(newID(t), "x", hlc.HLC{})
	y := ga.row(newID(t), "y", hlc.HLC{})
	write(t, a, store.Change{Row: x}, store.Change{Row: y})
	syncOK(t, a)
	syncOK(t, b)
	write(t, a, store.Change{Row: ga.row(x.ID, "x from a", x.HLC), Expect: x.HLC})
	syncOK(t, a)
	write(t, b, store.Change{Row: gb.row(x.ID, "x from b", x.HLC), Expect: x.HLC})
	write(t, b, store.Change{Row: gb.row(y.ID, "y from b", y.HLC), Expect: y.HLC})
	rep, err := b.Sync(ctx)
	if err != nil || len(rep.Refused) != 1 || rep.Refused[0].ID != x.ID {
		t.Fatalf("sync = %+v %v, want only x refused", rep, err)
	}
	if rep.Sent != 1 {
		t.Fatalf("sync sent %d rows, want y sent while x waits: %+v", rep.Sent, rep)
	}
	syncOK(t, a)
	if got := get(t, a, y.ID).Doc; !bytes.Contains(got, []byte(`"y from b"`)) {
		t.Fatalf("the other clone reads y = %s, want b's edit (it did not clash)", got)
	}
	if got := get(t, a, x.ID).Doc; !bytes.Contains(got, []byte(`"x from a"`)) {
		t.Fatalf("the other clone reads x = %s, want its own edit (b's waits)", got)
	}
	if _, ok, _ := b.RowSides(ctx, x.ID); !ok {
		t.Fatal("x is no longer a conflict after the partial send")
	}
	if _, ok, _ := b.RowSides(ctx, y.ID); ok {
		t.Fatal("y shows as a conflict after it was sent")
	}
	if err := b.SettleRow(ctx, x.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := get(t, b, x.ID).Doc; !bytes.Contains(got, []byte(`"x from a"`)) {
		t.Fatalf("after take other, b reads x = %s, want the remote's copy at once", got)
	}
	syncOK(t, b)
	syncOK(t, a)
	for _, c := range []*Backend{a, b} {
		if got := get(t, c, x.ID).Doc; !bytes.Contains(got, []byte(`"x from a"`)) {
			t.Fatalf("x after settling = %s", got)
		}
		if got := get(t, c, y.ID).Doc; !bytes.Contains(got, []byte(`"y from b"`)) {
			t.Fatalf("y after settling = %s", got)
		}
	}
}
