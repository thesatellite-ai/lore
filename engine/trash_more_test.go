package engine

import (
	"context"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// TestTrashStats (DELETION 4.5, 3.7): stats count the trash per table and
// deleter, the expired rows and the tombstones (a purge adds one); no row
// carries delete_overruled, so --overruled lists none.
func TestTrashStats(t *testing.T) {
	ctx := context.Background()
	f, _ := purgeFixture(t)
	old := f.memFields("archived long ago")
	old["archived_at"] = value.DateTimeFrom(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	recent := f.memFields("archived yesterday")
	recent["archived_at"] = value.DateTimeFrom(time.Now().Add(-24 * time.Hour))
	var made []ids.AnyID
	for _, fields := range []map[string]value.Value{old, recent, f.memFields("alive"), f.memFields("to trash")} {
		w, err := f.eng.Create(ctx, kbStore, schema.TableMemories, fields)
		if err != nil {
			t.Fatal(err)
		}
		made = append(made, w.Row.ID)
	}
	if _, err := f.eng.Delete(ctx, Ref{ID: made[3]}); err != nil {
		t.Fatal(err)
	}
	st, err := f.eng.TrashStats(ctx)
	if err != nil || st.Rows != 1 || st.ByTable[string(schema.TableMemories)] != 1 || st.ByDeleter[testActor.String()] != 1 || st.Tombstones[string(kbStore)] != 0 {
		t.Fatalf("stats = %+v, %v", st, err)
	}
	if _, err := f.eng.Purge(ctx, Ref{ID: made[0]}); err != nil {
		t.Fatal(err)
	}
	if st, _ = f.eng.TrashStats(ctx); st.Tombstones[string(kbStore)] != 1 || st.Rows != 1 || st.Expired != 0 {
		t.Fatalf("stats after purge = %+v", st)
	}
	if ov, oerr := f.eng.Overruled(ctx); oerr != nil || len(ov) != 0 {
		t.Fatalf("overruled = %v, %v", ov, oerr)
	}
}
