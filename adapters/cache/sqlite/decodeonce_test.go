package sqlite

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// TestApplyDecodesEachRowOnce (bug hunt H-284, devco:D-1022): Apply
// decodes each applied row's document once and hands it to the typed
// projection and to the meta quarantine; the quarantine is still
// recorded. Before, the quarantine decoded every row again, and on a big
// cache build the decode memo (cleared by the build's own reads) missed:
// each row file was decoded three or four times.
func TestApplyDecodesEachRowOnce(t *testing.T) {
	f := newFixture(t)
	c := f.open("c.db")
	k := must(ids.New[ids.MetaKey]())
	bad, good := newMem(), newMem()
	rows := []store.Row{
		f.metaMemory(bad, "a", map[ids.ID[ids.MetaKey]]string{k: `{"name":"client","scope":"repo"}`}), // no v: quarantined
		f.metaMemory(good, "b", map[ids.ID[ids.MetaKey]]string{k: `{"name":"client","scope":"repo","v":"acme"}`}),
	}
	for range 20 {
		rows = append(rows, f.metaMemory(newMem(), "c", map[ids.ID[ids.MetaKey]]string{k: `{"name":"client","scope":"repo","v":"x"}`}))
	}
	before := appliedDecodes.Load()
	f.apply(c, store.SourceRepo, "1", rows...)
	if n := appliedDecodes.Load() - before; n != int64(len(rows)) {
		t.Fatalf("Apply of %d rows decoded %d documents, want each once", len(rows), n)
	}
	got, _ := quarIDs(t, f, c)
	if len(got) != 1 || got[0] != "repo|"+bad.String() {
		t.Fatalf("quarantined %v, want only %s", got, bad)
	}
}
