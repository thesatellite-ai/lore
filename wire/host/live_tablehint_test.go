package host

// live_tablehint_test.go — D-159: every command looked up its policy and
// actor rows with a one-table read, and the live backend converted every
// live task and comment for each of them, all under the clone's live lock;
// ten agent sessions in one clone queued for seconds. A one-table read
// converts only that table's live rows, and a live row is converted once
// per live commit.

import (
	"testing"

	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

func TestLiveReadOfOneTableConvertsOnlyIt(t *testing.T) {
	f := newLiveFixture(t)
	writeManyLive(t, f, 40)
	rows, cur, err := f.b.Read(store.WithTables(f.ctx, schema.TableActors), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Table == schema.TableTasks {
			t.Fatalf("an actors read returned live task %s", r.ID)
		}
	}
	if cur != "" {
		t.Fatalf("a one-table read is not a complete picture, its cursor must be empty: %q", cur)
	}
	if n := len(f.s.decoded.stored); n != 0 {
		t.Fatalf("an actors read converted %d live rows of other tables", n)
	}

	full, cur, err := f.b.Read(f.ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if cur == "" || countTable(full, schema.TableTasks) != 40 {
		t.Fatalf("a full read: %d live tasks, cursor %q", countTable(full, schema.TableTasks), cur)
	}
	again, _, err := f.b.Read(f.ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	first := map[store.Key]*byte{}
	for _, r := range full {
		if r.Table == schema.TableTasks {
			first[r.Key()] = &r.Doc[0]
		}
	}
	for _, r := range again {
		if p, ok := first[r.Key()]; ok && p != &r.Doc[0] {
			t.Fatalf("live task %s was converted again for an unchanged live ref", r.ID)
		}
	}
}

// countTable is how many rows of table rows holds.
func countTable(rows []store.Row, table string) int {
	n := 0
	for _, r := range rows {
		if string(r.Table) == table {
			n++
		}
	}
	return n
}
