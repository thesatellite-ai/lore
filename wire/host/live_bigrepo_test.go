package host

// live_bigrepo_test.go — D-215 / D-217: on a repo with 2,400 live tasks
// every command paid for every live row twice over: the cache refresh
// converted each live row again although the live ref had not moved, and
// engine.Get asked for the purged-id list on every row read, each time
// reading every blob of the live tree (task search: 3 s of a 3.4 s run).

import (
	"reflect"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestLiveReadSinceSkipsLiveRows: a read with the cursor of the previous
// full read, the live ref unchanged, returns only what changed since (no
// live row again, none converted again), and still sees the next live write in full.
func TestLiveReadSinceSkipsLiveRows(t *testing.T) {
	f := newLiveFixture(t)
	writeManyLive(t, f, 40)
	all, cur, err := f.b.Read(f.ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 40 || !strings.HasPrefix(string(cur), liveCursorPrefix) {
		t.Fatalf("full read: %d rows, cursor %q", len(all), cur)
	}
	f.s.decoded.mu.Lock()
	f.s.decoded.stored = nil // forget the full read's conversions
	f.s.decoded.mu.Unlock()
	again, cur2, err := f.b.Read(f.ctx, cur)
	if err != nil {
		t.Fatal(err)
	}
	f.s.decoded.mu.Lock()
	converted := len(f.s.decoded.stored)
	f.s.decoded.mu.Unlock()
	if converted != 0 {
		t.Fatalf("a read since an unchanged live ref converted %d live rows again, want 0", converted)
	}
	for _, r := range again {
		if r.Table == schema.TableTasks {
			t.Fatalf("a read since an unchanged live ref returned live task %s again (%d rows)", r.ID, len(again))
		}
	}
	if err := f.b.Write(f.ctx, []store.Change{{Row: ltRow(t, schema.TableTasks, ltTaskA, 2, "one more", false)}}); err != nil {
		t.Fatal(err)
	}
	moved, _, err := f.b.Read(f.ctx, cur2)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range moved {
		found = found || r.ID.String() == ltTaskA
	}
	if !found {
		t.Fatalf("a read after a live write must return the new row (%d rows)", len(moved))
	}
}

// TestLivePurgedOncePerCommit: the purged-id list of one live commit is
// read once per session, not once per row read.
func TestLivePurgedOncePerCommit(t *testing.T) {
	f := newLiveFixture(t)
	writeManyLive(t, f, 10)
	if _, err := f.b.Purged(f.ctx); err != nil {
		t.Fatal(err)
	}
	a, hit := f.s.decoded.purgedOf(mustResolve(t, f))
	if !hit {
		t.Fatal("Purged did not keep the live commit's purged ids")
	}
	if _, err := f.b.Purged(f.ctx); err != nil {
		t.Fatal(err)
	}
	b, _ := f.s.decoded.purgedOf(mustResolve(t, f))
	if reflect.ValueOf(a).UnsafePointer() != reflect.ValueOf(b).UnsafePointer() {
		t.Fatal("a second Purged of an unchanged live ref read the tree again")
	}
}

// mustResolve is the live ref's commit.
func mustResolve(t *testing.T, f ltFixture) string {
	t.Helper()
	oid, ok, err := f.s.ref.Repo.ResolveRef(f.ctx, f.s.ref.Name)
	if err != nil || !ok {
		t.Fatalf("live ref: %v %v", ok, err)
	}
	return oid
}

// TestLiveGetConvertsOncePerCommit (D-217): Get of a live row uses the
// session's per-commit conversion, so a doctor or fix reading every row by
// id after a full read converts none of them again.
func TestLiveGetConvertsOncePerCommit(t *testing.T) {
	f := newLiveFixture(t)
	writeManyLive(t, f, 5)
	id := mustAnyID(t, ltManyTaskID(3))
	if _, err := f.b.Get(f.ctx, schema.TableTasks, id); err != nil {
		t.Fatal(err)
	}
	f.s.decoded.mu.Lock()
	_, kept := f.s.decoded.stored[keyOfID(schema.TableTasks, id)]
	f.s.decoded.mu.Unlock()
	if !kept {
		t.Fatal("Get converted the live row without keeping it for the commit")
	}
}
