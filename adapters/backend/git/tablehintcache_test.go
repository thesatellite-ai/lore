package git

// A read narrowed to a few tables (store.WithTables) at an unchanged HEAD
// answers from the backend's table reads (Backend.tableRows, bug hunt
// H-214) instead of listing the tree again; a commit makes the next one
// read again, and every answer is the caller's own copy.

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

func TestTableReadAtOneCommitListsTheTreeOnce(t *testing.T) {
	ctx := store.WithTables(context.Background(), memories)
	b := clone(t, newRemote(t, true))
	g := newGen(t)
	write(t, b, store.Change{Row: g.row(newID(t), "first", hlc.HLC{})})
	calls := countGit(t, b)

	rows, _, err := b.Read(ctx, "")
	if err != nil || len(rows) != 1 {
		t.Fatalf("first read: %v %v", rows, err)
	}
	rows[0].Doc[0] = 'X' // the caller's copy: the kept read must not share it
	for range 3 {
		again, cur, err := b.Read(ctx, "")
		if err != nil || len(again) != 1 || cur != "" {
			t.Fatalf("again: %v %q %v (a narrowed read returns no cursor)", again, cur, err)
		}
		if again[0].Doc[0] == 'X' {
			t.Fatal("the kept read handed out the buffer a caller changed")
		}
	}
	if n := calls("ls-tree"); n != 1 {
		t.Fatalf("four narrowed reads at one commit listed the tree %d times, want 1", n)
	}

	write(t, b, store.Change{Row: g.row(newID(t), "second", hlc.HLC{})}) // a commit: HEAD moves
	after, _, err := b.Read(ctx, "")
	if err != nil || len(after) != 2 {
		t.Fatalf("read after a commit: %d rows, %v; want 2", len(after), err)
	}
	if n := calls("ls-tree"); n != 2 {
		t.Fatalf("the narrowed read after a commit did not list the tree again (%d lists)", n)
	}
}
