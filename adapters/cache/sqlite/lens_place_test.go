package sqlite

import (
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/store"
)

// placeCounts runs `SELECT source, place, COUNT(*) … GROUP BY 1,2` and
// returns "source|place" → count.
func placeCounts(t *testing.T, l *Lens) map[string]string {
	t.Helper()
	res := must(l.Query(t.Context(), `SELECT source, place, COUNT(*) FROM memories_all GROUP BY 1, 2`))
	if len(res.Columns) < 2 || res.Columns[0] != "source" || res.Columns[1] != LensPlaceColumn {
		t.Fatalf("columns = %v, want source, place first", res.Columns)
	}
	out := map[string]string{}
	for _, r := range res.Rows {
		out[r[0].String()+"|"+r[1].String()] = r[2].String()
	}
	return out
}

// TestLensPlaceColumn pins devco D-568: every <table>_all view carries
// `place`, the label search prints for the source ("acme/web" for the
// org entry "acme" attached at folder web), beside the unchanged `source`
// queries filter on; a source with no label (the repo) is its name, and
// a combined file labels each row by its own _source.
func TestLensPlaceColumn(t *testing.T) {
	t.Parallel()
	_, srcs, _, _ := threeCaches(t)
	places := map[string]string{"agency": "agency/web", "golang": "golang/api"}
	l := must(OpenLensWith(t.Context(), srcs, LensOptions{Places: places}))
	t.Cleanup(func() { _ = l.Close() })
	got := placeCounts(t, l)
	want := map[string]string{"repo|repo": "2", "agency|agency/web": "2", "golang|golang/api": "2"}
	if len(got) != len(want) {
		t.Fatalf("source|place counts = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("source|place counts = %v, want %v", got, want)
		}
	}
	// queries keep filtering on source
	res := must(l.Query(t.Context(), `SELECT COUNT(*) FROM memories_all WHERE source = 'agency'`))
	if res.Rows[0][0].String() != "2" {
		t.Fatalf("WHERE source = 'agency': %v", res.Rows)
	}

	f := newFixture(t)
	path := filepath.Join(f.dir, "combined.db")
	c := must(Open(f.ctx, path, f.reg, f.exp))
	prj := newPrj()
	for _, n := range []string{"agency", "golang", "plain"} {
		f.apply(c, store.Source(n), "1", f.memory(newMem(), prj, "row of "+n))
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	cl := must(OpenLensWith(f.ctx, []LensSource{{Name: "combined", Path: path, Combined: true}}, LensOptions{Places: places}))
	t.Cleanup(func() { _ = cl.Close() })
	got = placeCounts(t, cl)
	want = map[string]string{"agency|agency/web": "1", "golang|golang/api": "1", "plain|plain": "1"}
	if len(got) != len(want) {
		t.Fatalf("combined source|place counts = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("combined source|place counts = %v, want %v", got, want)
		}
	}
}
