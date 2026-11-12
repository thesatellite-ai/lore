package git

import (
	"bytes"
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// TestSettleRow: a row both clones changed is refused by the sync (no
// merger can merge it); RowSides shows both copies; SettleRow takes the
// chosen side on the next sync, and both clones end equal. A side that
// changes again after the choice is a new conflict, never overwritten.
func TestSettleRow(t *testing.T) {
	t.Parallel()
	for _, takeTheirs := range []bool{true, false} {
		ctx := context.Background()
		remote := newRemote(t, true)
		a, b := clone(t, remote), clone(t, remote)
		ga, gb := newGen(t), newGen(t)
		x := ga.row(newID(t), "x", hlc.HLC{})
		write(t, a, store.Change{Row: x})
		syncOK(t, a)
		syncOK(t, b)
		write(t, a, store.Change{Row: ga.row(x.ID, "a side", x.HLC), Expect: x.HLC})
		syncOK(t, a)
		write(t, b, store.Change{Row: gb.row(x.ID, "b side", x.HLC), Expect: x.HLC})
		if rep, err := b.Sync(ctx); err != nil || len(rep.Refused) != 1 {
			t.Fatalf("sync = %+v %v, want x refused", rep, err)
		}
		s, ok, err := b.RowSides(ctx, x.ID)
		if err != nil || !ok {
			t.Fatalf("RowSides = %v %v, want both sides", ok, err)
		}
		if !bytes.Contains(s.Mine, []byte("b side")) || !bytes.Contains(s.Theirs, []byte("a side")) || !bytes.Contains(s.Base, []byte(`"x"`)) || s.Table != memories {
			t.Fatalf("RowSides = %+v", s)
		}
		if _, ok, _ := a.RowSides(ctx, x.ID); ok {
			t.Fatal("the clone that pushed sees a conflict")
		}
		if err := b.SettleRow(ctx, x.ID, takeTheirs); err != nil {
			t.Fatal(err)
		}
		syncOK(t, b)
		syncOK(t, a)
		want := "b side"
		if takeTheirs {
			want = "a side"
		}
		for _, c := range []*Backend{a, b} {
			if got := string(get(t, c, x.ID).Doc); !bytes.Contains([]byte(got), []byte(`"`+want+`"`)) {
				t.Fatalf("take theirs %v: body %q, want %q", takeTheirs, got, want)
			}
		}
		if _, ok, _ := b.RowSides(ctx, x.ID); ok {
			t.Fatal("a settled row still shows as a conflict")
		}
		if err := b.SettleRow(ctx, x.ID, true); !errs.Has(err, errs.NotFound) {
			t.Fatalf("settle a settled row = %v, want E_NOT_FOUND", err)
		}
	}
}

// TestSettleRowStale: the remote changed the row again after the person
// chose: the choice does not apply, the row is refused again.
func TestSettleRowStale(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote := newRemote(t, true)
	a, b := clone(t, remote), clone(t, remote)
	ga, gb := newGen(t), newGen(t)
	x := ga.row(newID(t), "x", hlc.HLC{})
	write(t, a, store.Change{Row: x})
	syncOK(t, a)
	syncOK(t, b)
	a1 := ga.row(x.ID, "a side", x.HLC)
	write(t, a, store.Change{Row: a1, Expect: x.HLC})
	syncOK(t, a)
	write(t, b, store.Change{Row: gb.row(x.ID, "b side", x.HLC), Expect: x.HLC})
	if _, err := b.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.SettleRow(ctx, x.ID, false); err != nil {
		t.Fatal(err)
	}
	write(t, a, store.Change{Row: ga.row(x.ID, "a again", a1.HLC), Expect: a1.HLC})
	syncOK(t, a)
	if rep, err := b.Sync(ctx); err != nil || len(rep.Refused) != 1 {
		t.Fatalf("sync after the remote changed again = %+v %v, want x refused", rep, err)
	}
}

// TestSettleByEdit: after a refused sync, an edit of the row here (any
// value, not only the remote's) settles it: the next sync keeps it.
func TestSettleByEdit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote := newRemote(t, true)
	a, b := clone(t, remote), clone(t, remote)
	ga, gb := newGen(t), newGen(t)
	x := ga.row(newID(t), "x", hlc.HLC{})
	write(t, a, store.Change{Row: x})
	syncOK(t, a)
	syncOK(t, b)
	write(t, a, store.Change{Row: ga.row(x.ID, "a side", x.HLC), Expect: x.HLC})
	syncOK(t, a)
	b1 := gb.row(x.ID, "b side", x.HLC)
	write(t, b, store.Change{Row: b1, Expect: x.HLC})
	if rep, err := b.Sync(ctx); err != nil || len(rep.Refused) != 1 {
		t.Fatalf("sync = %+v %v, want x refused", rep, err)
	}
	write(t, b, store.Change{Row: gb.row(x.ID, "agreed", b1.HLC), Expect: b1.HLC})
	syncOK(t, b)
	syncOK(t, a)
	for _, c := range []*Backend{a, b} {
		if got := string(get(t, c, x.ID).Doc); !bytes.Contains([]byte(got), []byte(`"agreed"`)) {
			t.Fatalf("body after the settling edit: %s", got)
		}
	}
}
