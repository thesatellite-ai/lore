package sqlite

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/store"
)

// TestFreshApplyRunsNoHeldLookups: a cache built from nothing stores its
// rows without looking each one up first (H-285, devco:D-1023): no held
// copy and no copy of the id in another source can exist. A source new to
// a cache other sources fill still asks whether the id is held elsewhere,
// a source that holds rows asks both, and the same id twice in one Apply
// is looked up (TestFreshBuildMatchesReplaceBuild proves the contents).
func TestFreshApplyRunsNoHeldLookups(t *testing.T) {
	f := newFixture(t)
	c := f.open("seen.db")
	prj := newPrj()
	var rows []store.Row
	for range 50 {
		rows = append(rows, f.memory(newMem(), prj, "a note built from nothing"))
	}
	step := func(name string, source store.Source, cursor string, want int64, rows ...store.Row) {
		t.Helper()
		before := heldLookups.Load()
		f.apply(c, source, cursor, rows...)
		if got := heldLookups.Load() - before; got != want {
			t.Fatalf("%s: %d held lookups, want %d", name, got, want)
		}
	}
	// the same id twice in the first build, the older copy last: it is
	// looked up (once: the newer copy is held) and not stored
	back := newMem()
	older := f.memory(back, prj, "older copy")
	newer := f.memory(back, prj, "newer copy")
	step("first build of a", "a", "1", 1, append(rows, newer, older)...)
	if got := strings1(t, c, `SELECT doc FROM "`+tblRows+`" WHERE id = ?`, back.String()); len(got) != 1 || !strings.Contains(got[0], "newer copy") {
		t.Fatalf("an older copy after a newer one in a first build left %q, want the newer copy", got)
	}
	twice := newMem()
	// b's own note and the first copy ask idHeld; the second copy finds the first
	step("same id twice in a new source", "b", "1", 1+1+1,
		f.memory(newMem(), prj, "b's own note"), f.memory(twice, prj, "first"), f.memory(twice, prj, "second"))
	step("a source that holds rows", "a", "2", 2, f.memory(newMem(), prj, "a later note"))
	if got := strings1(t, c, `SELECT doc FROM "`+tblRows+`" WHERE id = ?`, twice.String()); len(got) != 1 || !strings.Contains(got[0], "second") {
		t.Fatalf("the id stored twice in one Apply holds %q, want the second version", got)
	}
}
