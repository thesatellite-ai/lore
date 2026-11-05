package sqlite

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/metafilter"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// quarIDs is MetaQuarantined as "source|id" strings.
func quarIDs(t *testing.T, f *fixture, c *Cache) ([]string, []MetaQuarantine) {
	t.Helper()
	qs, err := c.MetaQuarantined(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(qs))
	for _, q := range qs {
		out = append(out, string(q.Source)+"|"+q.ID)
	}
	return out, qs
}

// TestMetaQuarantineTable pins METADATA 3.13 in the cache: a bad entry
// name, an entry with no v and a row over the import cap are recorded in
// _meta_quarantine (problems name keys, never values) and kept out of
// row_meta; a fix, a tombstone and Rebuild(source) clear the record; each
// source keeps its own; a conflict entry is not a quarantine.
func TestMetaQuarantineTable(t *testing.T) {
	f := newFixture(t)
	c := f.open("c.db")
	k1, k2 := must(ids.New[ids.MetaKey]()), must(ids.New[ids.MetaKey]())
	badName, noV, overCap, good, conflict := newMem(), newMem(), newMem(), newMem(), newMem()
	big := `{"name":"blob","scope":"repo","v":"` + strings.Repeat("x", metafilter.ImportMaxBytes+10) + `"}`
	f.apply(c, store.SourceRepo, "1",
		f.metaMemory(badName, "a", map[ids.ID[ids.MetaKey]]string{k1: `{"name":"Client-Name","scope":"repo","v":"` + metaProbe + `"}`}),
		f.metaMemory(noV, "b", map[ids.ID[ids.MetaKey]]string{k1: `{"name":"client","scope":"repo"}`}),
		f.metaMemory(overCap, "c", map[ids.ID[ids.MetaKey]]string{k2: big}),
		f.metaMemory(good, "d", map[ids.ID[ids.MetaKey]]string{k1: `{"name":"client","scope":"repo","v":"acme"}`}),
		f.metaMemory(conflict, "e", map[ids.ID[ids.MetaKey]]string{k1: `{"conflict":{"base":"a","ours":"b","theirs":"c"},"name":"client","scope":"repo"}`}),
	)
	f.apply(c, "agency", "1", f.metaMemory(badName, "a", map[ids.ID[ids.MetaKey]]string{k1: `{"name":"Client-Name","scope":"repo","v":"x"}`}))
	got, qs := quarIDs(t, f, c)
	want := map[string]bool{"repo|" + badName.String(): true, "repo|" + noV.String(): true, "repo|" + overCap.String(): true, "agency|" + badName.String(): true}
	if len(got) != len(want) {
		t.Fatalf("quarantined %v", got)
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected quarantine %s", g)
		}
	}
	for _, q := range qs {
		if len(q.Problems) == 0 || q.Table != "memories" {
			t.Errorf("record %+v", q)
		}
		for _, p := range q.Problems {
			if strings.Contains(p, metaProbe) || strings.Contains(p, "xxxx") {
				t.Errorf("problem text leaks a value: %q", p)
			}
		}
	}
	// row_meta leaves quarantined rows out, keeps the good one
	for _, id := range []ids.AnyID{badName, noV, overCap} {
		if n := count(t, c, `SELECT COUNT(*) FROM row_meta WHERE entity_id = ?`, id.String()); n != 0 {
			t.Errorf("quarantined %s has %d row_meta rows", id, n)
		}
	}
	if n := count(t, c, `SELECT COUNT(*) FROM row_meta WHERE entity_id = ? AND item_idx = 0`, good.String()); n != 1 {
		t.Errorf("good row has %d value rows", n)
	}
	// fix: the record goes and row_meta shows the row
	f.apply(c, store.SourceRepo, "2", f.metaMemory(badName, "a", map[ids.ID[ids.MetaKey]]string{k1: `{"name":"client_name","scope":"repo","v":"ok"}`}))
	if n := count(t, c, `SELECT COUNT(*) FROM row_meta WHERE entity_id = ? AND _source = 'repo' AND item_idx = 0`, badName.String()); n != 1 {
		t.Errorf("fixed row not in row_meta: %d", n)
	}
	// tombstone clears
	f.apply(c, store.SourceRepo, "3", f.tomb("memories", noV))
	got, _ = quarIDs(t, f, c)
	if len(got) != 2 || !strings.Contains(strings.Join(got, ","), overCap.String()) {
		t.Errorf("after fix + tombstone %v", got)
	}
	// Rebuild(repo) clears repo's records only
	if err := c.Rebuild(f.ctx, store.SourceRepo); err != nil {
		t.Fatal(err)
	}
	got, _ = quarIDs(t, f, c)
	if len(got) != 1 || got[0] != "agency|"+badName.String() {
		t.Errorf("after rebuild %v", got)
	}
}
