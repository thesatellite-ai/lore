package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestMetaWriteSettlesAConflictEntry (METADATA 3.12): a row whose meta
// holds an unsettled {"conflict": …} entry (no v) can still be written:
// setting that key replaces the conflict with the value (lore meta set
// <id> <key>=<value>, the command the merge report names); a write of
// another key keeps the conflict entry byte for byte, so the row stays
// blocked from landing and committing until a person settles it.
func TestMetaWriteSettlesAConflictEntry(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	cost := metaKeyRow(t, f, "cost", "")
	metaKeyRow(t, f, "team", "")
	conflict, err := value.JSONOf([]byte(`{"conflict":{"base":"3","ours":"4","theirs":"5"},"name":"cost","scope":"repo"}`))
	if err != nil {
		t.Fatal(err)
	}
	cur := map[ids.ID[ids.MetaKey]]value.JSON{cost: conflict}
	def, ok := f.eng.reg.Table(schema.TableMemories)
	if !ok {
		t.Fatal("no memories table")
	}
	rowID, err := ids.ParseAny("mem_01936f3300007d4e8f5a6b7c8d9e0f1a")
	if err != nil {
		t.Fatal(err)
	}
	apply := func(arg string) map[ids.ID[ids.MetaKey]]value.JSON {
		t.Helper()
		out, _, err := f.eng.applyMetaOn(ctx, call{}, store.SourceRepo, def, rowID, cur, metaWrite(t, []metaArg{{Text: arg}}), false, &screened{})
		if err != nil {
			t.Fatalf("%s on a row with a meta conflict: %v", arg, err)
		}
		return out
	}

	other := apply("team=core")
	if got := merge.MetaConflicts(row.Row{Meta: other}); len(got) != 1 || got[0] != cost.String() {
		t.Fatalf("a write of another key must keep the conflict entry, conflicts=%v meta=%v", got, other)
	}
	if !value.Equal(other[cost], conflict) {
		t.Fatalf("the kept conflict entry changed: %s", other[cost].Raw())
	}

	settled := apply("cost=4.5")
	if got := merge.MetaConflicts(row.Row{Meta: settled}); len(got) != 0 {
		t.Fatalf("setting the key must settle the conflict: %v", got)
	}
	if v := metaValues(t, row.Row{Meta: settled})["cost"]; v != `"4.5"` {
		t.Fatalf("cost = %s after settling, want \"4.5\"", v)
	}
}
