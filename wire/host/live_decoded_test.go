package host

// live_decoded_test.go — B1-10: lore fix on a repo with thousands of rows
// called engine.Get once per row of the v0.1.17 lore.db, and every Get on a
// live backend decoded the whole live tree again (minutes of CPU, the run
// killed by --all's timeout). A session decodes one live commit once.

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// ltManyTaskID is the id of the i-th generated task (made-up, valid ids).
func ltManyTaskID(i int) string { return fmt.Sprintf("tsk_01a1a6841fc0784d92a62b0a%08x", i) }

// writeManyLive writes n live tasks in one commit of the live ref.
func writeManyLive(tb testing.TB, f ltFixture, n int) {
	tb.Helper()
	changes := make([]store.Change, 0, n)
	for i := range n {
		changes = append(changes, store.Change{Row: ltRow(tb, schema.TableTasks, ltManyTaskID(i), 1, fmt.Sprintf("generated task %d", i), false)})
	}
	if err := f.b.Write(f.ctx, changes); err != nil {
		tb.Fatal(err)
	}
}

// TestLiveFilesDecodedOncePerCommit: two reads of an unchanged live ref
// share one decoded map (no second decode), and a write that moves the ref
// is seen by the next read (the cache is keyed by the commit).
func TestLiveFilesDecodedOncePerCommit(t *testing.T) {
	f := newLiveFixture(t)
	writeManyLive(t, f, 50)
	oid1, a, err := f.b.liveFiles(f.ctx, f.s)
	if err != nil {
		t.Fatal(err)
	}
	_, b, err := f.b.liveFiles(f.ctx, f.s)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 50 || reflect.ValueOf(a).UnsafePointer() != reflect.ValueOf(b).UnsafePointer() {
		t.Fatalf("an unchanged live ref must be decoded once per session: %d rows, same map %v", len(a), reflect.ValueOf(a).UnsafePointer() == reflect.ValueOf(b).UnsafePointer())
	}
	if err := f.b.Write(f.ctx, []store.Change{{Row: ltRow(t, schema.TableTasks, ltTaskA, 2, "one more", false)}}); err != nil {
		t.Fatal(err)
	}
	oid2, c, err := f.b.liveFiles(f.ctx, f.s)
	if err != nil {
		t.Fatal(err)
	}
	if oid2 == oid1 || len(c) != 51 {
		t.Fatalf("a moved live ref must be read again: oid %s→%s, %d rows", oid1, oid2, len(c))
	}
	if _, ok := c[live.RowKey{Table: schema.TableTasks, ID: ltTaskA}]; !ok {
		t.Fatal("the new live row is missing after the ref moved")
	}
	if _, err := f.b.Get(f.ctx, schema.TableTasks, mustAnyID(t, ltTaskA)); err != nil {
		t.Fatalf("Get reads the new live row: %v", err)
	}
}

// BenchmarkLiveGetManyRows is one Get per row of a 3000-row live ref, the
// shape of lore fix's old_db_edits step on a big v0.1.17 repo. Before the
// per-commit cache each Get decoded all 3000 files (quadratic).
func BenchmarkLiveGetManyRows(b *testing.B) {
	f := newLiveFixture(b)
	const n = 3000
	writeManyLive(b, f, n)
	refs := make([]ids.AnyID, n)
	for i := range refs {
		refs[i] = mustAnyID(b, ltManyTaskID(i))
	}
	b.ResetTimer()
	for range b.N {
		for _, id := range refs {
			if _, err := f.b.Get(f.ctx, schema.TableTasks, id); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// mustAnyID parses a made-up test id.
func mustAnyID(tb testing.TB, s string) ids.AnyID {
	tb.Helper()
	id, err := ids.ParseAny(s)
	if err != nil {
		tb.Fatal(err)
	}
	return id
}
