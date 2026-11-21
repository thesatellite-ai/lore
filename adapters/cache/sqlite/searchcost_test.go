package sqlite

import (
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestSearchUnfoldsOnlyRowsWithDotlessI (devco:D-808): a search whose
// hits' snippets hold an i reads a row back only when that row holds a ı;
// before, every such hit cost a read per full-text field (a common word
// in a 6,000-row store: thousands of queries per search). The one ı row
// still shows its own letters.
func TestSearchUnfoldsOnlyRowsWithDotlessI(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("c.db")
	var rows []store.Row
	for i := range 60 {
		rows = append(rows, f.decision(newDec(), fmt.Sprintf("orbit status item %d", i), "the orbit status is fine"))
	}
	rows = append(rows, f.decision(newDec(), "orbit status ılık", "orbit status in ılık"))
	f.apply(c, store.SourceRepo, "1", rows...)
	c.unfoldReads = 0
	hits := must(c.SearchHits(f.ctx, schema.TableDecisions, "orbit status", 100))
	if len(hits) != 61 {
		t.Fatalf("hits = %d, want 61", len(hits))
	}
	if c.unfoldReads > 4 {
		t.Errorf("unfoldSnippet read %d row(s) for 61 hits, want at most 4 (only the ı row)", c.unfoldReads)
	}
	found := false
	for _, h := range hits {
		if strings.Contains(h.Snippet, "ılık") {
			found = true
		}
	}
	if !found {
		t.Errorf("no snippet shows ılık as written: %+v", hits)
	}
}

// TestKindNamesReadFromTheCache (devco:D-808): KindNames maps each kind
// id to its name from the cache's own kinds table (search names its
// resource hits' kinds with it, no SQL lens); a cache with no kinds is
// an empty map.
func TestKindNamesReadFromTheCache(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("c.db")
	if got := must(c.KindNames(f.ctx)); len(got) != 0 {
		t.Fatalf("empty cache: %v, want no names", got)
	}
	k := f.kindWorld(c)
	other := must(ids.New[ids.Kind]())
	f.apply(c, store.SourceRepo, "k2", f.kindRow(other, "person"))
	got := must(c.KindNames(f.ctx))
	if got[k.kind.String()] != "server" || got[other.String()] != "person" || len(got) != 2 {
		t.Errorf("KindNames = %v, want %s=server and %s=person", got, k.kind, other)
	}
}

// TestSearchHitsBareIsSearchHitsWithoutSnippets (devco:D-808): the bare
// search a word's count uses returns SearchHits' ids in SearchHits' order
// (ı rows and expanded-text hits too), every snippet empty.
func TestSearchHitsBareIsSearchHitsWithoutSnippets(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("c.db")
	var rows []store.Row
	for i := range 30 {
		rows = append(rows, f.decision(newDec(), fmt.Sprintf("orbit note %d", i), strings.Repeat("orbit ", i%5+1)))
	}
	rows = append(rows, f.decision(newDec(), "orbit ılık", strings.Repeat("lorem ipsum ", 9000)+"orbit"))
	f.apply(c, store.SourceRepo, "1", rows...)
	for _, q := range []string{"orbit", "ılık OR note"} {
		full := must(c.SearchHits(f.ctx, schema.TableDecisions, q, 20))
		bare := must(c.SearchHitsBare(f.ctx, schema.TableDecisions, q, 20))
		if len(full) == 0 || len(bare) != len(full) {
			t.Fatalf("%q: bare %d hits, full %d", q, len(bare), len(full))
		}
		for i := range full {
			if bare[i].ID != full[i].ID || bare[i].Snippet != "" {
				t.Errorf("%q hit %d: bare %v %q, want id %v and no snippet", q, i, bare[i].ID, bare[i].Snippet, full[i].ID)
			}
		}
	}
}
