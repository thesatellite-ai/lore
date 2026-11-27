package git

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// TestKeptReadsShareOneBudget (bug hunt H-280, devco:D-1018): the reads
// the folders' backends keep share one budget per process; past it the
// backend that kept its reads longest ago drops them (its next read asks
// git again) and the newest keeps its own. Before, every backend kept its
// whole read for the process: a doctor over a big store's 213 folders
// held every row file of the store.
func TestKeptReadsShareOneBudget(t *testing.T) {
	ctx := context.Background()
	var backends []*Backend
	for range 3 {
		b := clone(t, newRemote(t, true))
		g := newGen(t)
		for _, body := range []string{"one", "two", "three"} {
			write(t, b, store.Change{Row: g.row(newID(t), body, hlc.HLC{})})
		}
		backends = append(backends, b)
	}
	rows, _, err := backends[0].Read(ctx, "")
	if err != nil || len(rows) != 3 {
		t.Fatalf("read: %d rows, %v", len(rows), err)
	}
	one := rowsBytes(rows)
	was := keptReadBudget
	keptReadBudget = one + one/2 // one backend's read fits, two do not
	t.Cleanup(func() { keptReadBudget = was })
	calls := countGit(t, backends[0])
	for _, b := range backends {
		if _, _, err := b.Read(ctx, ""); err != nil {
			t.Fatal(err)
		}
	}
	kept := func(b *Backend) bool {
		b.fullMu.Lock()
		defer b.fullMu.Unlock()
		return b.fullRows != nil
	}
	if kept(backends[0]) || kept(backends[1]) {
		t.Error("the backends read first still keep their reads past the budget")
	}
	if !kept(backends[2]) {
		t.Error("the backend read last dropped its own read")
	}
	keptReads.Lock()
	total := keptReads.total
	keptReads.Unlock()
	if total > keptReadBudget {
		t.Errorf("the kept reads hold %d bytes, over the budget of %d", total, keptReadBudget)
	}
	before := calls("ls-tree")
	again, _, err := backends[0].Read(ctx, "")
	if err != nil || len(again) != 3 {
		t.Fatalf("read after the drop: %d rows, %v", len(again), err)
	}
	if calls("ls-tree") != before+1 {
		t.Error("a backend whose read was dropped answered without asking git")
	}
}

// TestPassingReadIsNotKept (bug hunt H-284, devco:D-1022): a full read
// marked passing (store.WithPassingRead: a cache built from nothing) is
// not kept for later readers, and a later plain read asks git again and
// is kept. Before, the cache build of a 1.26M-row store kept each folder
// it had just written, up to the kept-read budget.
func TestPassingReadIsNotKept(t *testing.T) {
	ctx := context.Background()
	b := clone(t, newRemote(t, true))
	g := newGen(t)
	for _, body := range []string{"one", "two"} {
		write(t, b, store.Change{Row: g.row(newID(t), body, hlc.HLC{})})
	}
	kept := func() bool {
		b.fullMu.Lock()
		defer b.fullMu.Unlock()
		return b.fullRows != nil
	}
	b.fullMu.Lock()
	b.fullHead, b.fullRows = "", nil // the writes above may have kept a read
	b.fullMu.Unlock()
	rows, _, err := b.Read(store.WithPassingRead(ctx), "")
	if err != nil || len(rows) != 2 {
		t.Fatalf("passing read: %d rows, %v", len(rows), err)
	}
	if kept() {
		t.Fatal("the backend kept a passing read")
	}
	calls := countGit(t, b)
	if _, _, err := b.Read(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if calls("ls-tree") != 1 || !kept() {
		t.Fatalf("plain read after a passing one: %d ls-tree calls, kept %v; want 1 and kept", calls("ls-tree"), kept())
	}
}
