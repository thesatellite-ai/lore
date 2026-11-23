package wire

import (
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestDoctorSearchIndexFindsAndRebuildsAStaleCache (devco D-920, hunt
// H-178): a cache an older lore left holding a newer copy of a row than
// its file (the cursor past the file, so no refresh reads it again) is a
// search_index warning naming the row and `lore search rebuild --store
// repo`; doctor --fix rebuilds that cache, after which search finds the
// file's text and the check is quiet. A sound cache gives no finding.
func TestDoctorSearchIndexFindsAndRebuildsAStaleCache(t *testing.T) {
	t.Parallel()
	ctx := background()
	a := newTestApp(t, gitRepo(t))
	withProject(t, a)
	eng, err := a.Engine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	body, err := value.TextOf("the invoice queue drains every ten minutes")
	if err != nil {
		t.Fatal(err)
	}
	pid, err := a.Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := value.RefOf(pid.Any())
	if err != nil {
		t.Fatal(err)
	}
	kind, err := value.TextOf(schema.MemoryKindCore)
	if err != nil {
		t.Fatal(err)
	}
	src, err := value.TextOf("test")
	if err != nil {
		t.Fatal(err)
	}
	w, err := eng.Create(ctx, store.SourceRepo, schema.TableMemories, map[string]value.Value{"body": body, "project_id": ref, "kind": kind, "source_kind": src})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := a.DoctorSearchIndex(ctx, false); err != nil || len(got) != 0 {
		t.Fatalf("a sound cache: %+v %v", got, err)
	}

	// What an older lore left: a newer copy only the cache holds.
	st, err := a.state(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := eng.CurrentRows(ctx, store.SourceRepo)
	if err != nil {
		t.Fatal(err)
	}
	var newer store.Row
	for _, sr := range rows {
		if sr.ID != w.Row.ID {
			continue
		}
		r, err := sr.Decode()
		if err != nil {
			t.Fatal(err)
		}
		if r.Fields["body"], err = value.TextOf("the invoice queue drains every hourly batch"); err != nil {
			t.Fatal(err)
		}
		r.HLC.Wall = r.HLC.Wall.Add(time.Hour)
		if newer, err = store.FromRow(r); err != nil {
			t.Fatal(err)
		}
	}
	if newer.ID.IsZero() {
		t.Fatal("the memory is not among the store's rows")
	}
	cur, err := st.caches.Cursor(ctx, store.SourceRepo)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.caches.Apply(ctx, store.SourceRepo, []store.Row{newer}, cur); err != nil {
		t.Fatal(err)
	}
	if hits, err := a.Search(ctx, schema.TableMemories, "hourly", 10); err != nil || len(hits) != 1 {
		t.Fatalf("the stale copy is not in the index (the test sets up nothing): %+v %v", hits, err)
	}

	got, err := a.DoctorSearchIndex(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Check != DoctorCheckSearchIndex || got[0].Store != store.SourceRepo || got[0].Fixed ||
		!strings.Contains(got[0].Message, "holds 1 row in another version") || !strings.Contains(got[0].Message, newer.ID.String()) ||
		!strings.HasPrefix(got[0].Fix, "lore search rebuild --store repo") {
		t.Fatalf("findings %+v", got)
	}

	fixed, err := a.DoctorSearchIndex(ctx, true)
	if err != nil || len(fixed) != 1 || !fixed[0].Fixed {
		t.Fatalf("--fix: %+v %v", fixed, err)
	}
	if hits, err := a.Search(ctx, schema.TableMemories, "hourly", 10); err != nil || len(hits) != 0 {
		t.Fatalf("after the rebuild the index still finds the copy no file holds: %+v %v", hits, err)
	}
	if hits, err := a.Search(ctx, schema.TableMemories, "minutes", 10); err != nil || len(hits) != 1 {
		t.Fatalf("after the rebuild the index does not find the file's text: %+v %v", hits, err)
	}
	if again, err := a.DoctorSearchIndex(ctx, false); err != nil || len(again) != 0 {
		t.Fatalf("after the fix: %+v %v", again, err)
	}
}

// TestRebuildDropsRowsUnderAnEarlierAttachmentName (devco D-920, public
// issue #142): a folder cache still holding rows under the name the folder
// was attached under before is a search_index warning, and the rebuild of
// the current name clears that name too, so search stops finding the old
// text. Before, the rebuild cleared only the current name's rows.
func TestRebuildDropsRowsUnderAnEarlierAttachmentName(t *testing.T) {
	t.Parallel()
	ctx := background()
	a := newTestApp(t, gitRepo(t))
	withProject(t, a)
	eng, err := a.Engine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	st, err := a.state(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := eng.CurrentRows(ctx, store.SourceRepo)
	if err != nil || len(rows) == 0 {
		t.Fatalf("rows %v %v", rows, err)
	}
	f, err := st.caches.of(store.SourceRepo)
	if err != nil {
		t.Fatal(err)
	}
	const earlier = store.Source("kb/app")
	if err := f.Apply(ctx, earlier, rows, ""); err != nil {
		t.Fatal(err)
	}
	got, err := a.DoctorSearchIndex(ctx, false)
	if err != nil || len(got) != 1 || !strings.Contains(got[0].Message, "under the name kb/app") {
		t.Fatalf("findings %+v %v", got, err)
	}
	if err := st.caches.Rebuild(ctx, store.SourceRepo); err != nil {
		t.Fatal(err)
	}
	srcs, err := f.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range srcs {
		if s == earlier {
			t.Fatalf("the rebuild left the rows under the earlier name: %v", srcs)
		}
	}
	if again, err := a.DoctorSearchIndex(ctx, false); err != nil || len(again) != 0 {
		t.Fatalf("after the rebuild: %+v %v", again, err)
	}
}
