package sqlite

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// TestRefreshRefsReexpandsAnOlderFile pins devco D-793 (owner P-12): a
// cache file whose references were expanded under older reference rules
// (an older binary: no refs stamp) is expanded again on RefreshRefs, then
// stamped, so the next open pays one one-row read and expands nothing; a
// new refs version expands again. Before it only a manual `lore search
// rebuild` brought row_expanded and row_refs up to the binary's rules.
func TestRefreshRefsReexpandsAnOlderFile(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("c.db")
	res := must(ids.New[ids.Resource]()).Any()
	mem := newMem()
	plain := newMem()
	f.apply(c, store.SourceRepo, "m1",
		f.memory(mem, newPrj(), "met ${ref:"+res.String()+"} about orbit"),
		f.memory(plain, newPrj(), "no reference here"))
	expanded := func() []string {
		return strings1(t, c, `SELECT text FROM "`+tblExpanded+`" WHERE entity_id = ?`, mem.String())
	}
	if got := expanded(); len(got) != 1 || got[0] != "met ${ref:"+res.String()+"} about orbit" {
		t.Fatalf("under the old rules the reference stays as written, got %q", got)
	}

	f.exp.set(res.String(), "alice_ng") // the new rules resolve it
	f.exp.resetCalls()
	n, err := c.RefreshRefs(f.ctx, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("[D-793] RefreshRefs on an unstamped file expanded %d fields, want 1 (the one holding a reference)", n)
	}
	if got := expanded(); len(got) != 1 || got[0] != "met alice_ng about orbit" {
		t.Errorf("[D-793] after RefreshRefs row_expanded is %q, want the new rules' text", got)
	}

	f.exp.set(res.String(), "changed")
	f.exp.resetCalls()
	again := f.open("c.db") // the next open
	if n, err := again.RefreshRefs(f.ctx, "v1"); err != nil || n != 0 {
		t.Errorf("[D-793] RefreshRefs on a stamped file: %d fields, %v; want 0 (one stamp read)", n, err)
	}
	if calls := f.exp.resetCalls(); len(calls) != 0 {
		t.Errorf("[D-793] a stamped file was expanded again: %q", calls)
	}
	if n, err := again.RefreshRefs(f.ctx, "v2"); err != nil || n != 1 {
		t.Errorf("[D-793] RefreshRefs at a newer stamp: %d fields, %v; want 1", n, err)
	}
	if err := again.StampRefs(f.ctx, "v3"); err != nil {
		t.Fatal(err)
	}
	f.exp.resetCalls()
	if n, err := again.RefreshRefs(f.ctx, "v3"); err != nil || n != 0 || len(f.exp.resetCalls()) != 0 {
		t.Errorf("[D-793] RefreshRefs after StampRefs at that stamp: %d fields, %v; want 0, nothing expanded", n, err)
	}
}
