package sqlite

import (
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// decTag and tagTag type the ids the tests mint (no typed tag exists for
// these tables in model/ids).
type (
	decTag struct{}
	tagTag struct{}
)

func (decTag) Prefix() ids.Prefix { return ids.PrefixDecision }
func (tagTag) Prefix() ids.Prefix { return ids.PrefixTag }

func newDec() ids.AnyID { return must(ids.New[decTag]()).Any() }
func newTag() ids.AnyID { return must(ids.New[tagTag]()).Any() }

// decision builds a decisions row with title, body and tag_ids.
func (f *fixture) decision(id ids.AnyID, title, body string, tags ...ids.AnyID) store.Row {
	f.t.Helper()
	items := make([]value.Value, len(tags))
	for i, tg := range tags {
		items[i] = must(value.RefOf(tg))
	}
	return f.live(schema.TableDecisions, id, map[string]value.Value{
		"title":            text(f.t, title),
		"body":             text(f.t, body),
		schema.FieldTagIDs: must(value.ListOf(value.KindRef, items...)),
	})
}

// TestSearchHitsWeights: bm25 weights follow FTSWeightOf — the same word
// in a title (weight 4) ranks above it in a body (3), which ranks above a
// field of weight 1 (status); scores are positive and descending (higher
// is better, v0.1.17's sign).
func TestSearchHitsWeights(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("c.db")
	def, ok := f.reg.Table(schema.TableDecisions)
	if !ok {
		t.Fatal("no decisions table")
	}
	if !(def.FTSWeightOf("title") > def.FTSWeightOf("body") && def.FTSWeightOf("body") > def.FTSWeightOf("status")) {
		t.Fatalf("weights title %v body %v status %v", def.FTSWeightOf("title"), def.FTSWeightOf("body"), def.FTSWeightOf("status"))
	}
	inTitle, inBody, inStatus := newDec(), newDec(), newDec()
	statusRow := f.decision(inStatus, "alpha beta", "gamma delta")
	doc := must(statusRow.Decode())
	doc.Fields["status"] = text(t, "kubernetes")
	f.apply(c, store.SourceRepo, "1",
		f.decision(inBody, "alpha beta", "kubernetes delta"),
		must(store.FromRow(doc)),
		f.decision(inTitle, "kubernetes beta", "alpha delta"),
	)
	hits := must(c.SearchHits(f.ctx, schema.TableDecisions, "kubernetes", 10))
	got := make([]ids.AnyID, len(hits))
	for i, h := range hits {
		got[i] = h.ID
	}
	if !slices.Equal(got, []ids.AnyID{inTitle, inBody, inStatus}) {
		t.Fatalf("order %v, want title, body, status", got)
	}
	for i, h := range hits {
		if h.Score <= 0 {
			t.Errorf("score %v not positive", h.Score)
		}
		if i > 0 && hits[i-1].Score < h.Score {
			t.Errorf("scores not descending: %v", hits)
		}
	}
}

// TestSearchHitsTokenizer: porter stemming (deploying ~ deployed),
// diacritics folded both ways (cafe ~ café), case folded, a snippet with
// <b>…</b> markers around the match; a bad MATCH and a zero limit are
// E_INVALID_INPUT; an unknown table E_NOT_FOUND.
func TestSearchHitsTokenizer(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("c.db")
	prj := newPrj()
	stem, accent, upper := newMem(), newMem(), newMem()
	f.apply(c, store.SourceRepo, "1",
		f.memory(stem, prj, "we deployed the service on friday"),
		f.memory(accent, prj, "meet at the café near the office"),
		f.memory(upper, prj, "POSTGRES runs the queue"),
	)
	cases := []struct {
		match string
		want  ids.AnyID
	}{
		{"deploying", stem}, {"deploys", stem}, {"cafe", accent}, {"café", accent}, {"CAFE", accent}, {"postgres", upper},
	}
	for _, cs := range cases {
		hits := must(c.SearchHits(f.ctx, schema.TableMemories, cs.match, 10))
		if len(hits) != 1 || hits[0].ID != cs.want {
			t.Errorf("%q: %v", cs.match, hits)
			continue
		}
		if !strings.Contains(hits[0].Snippet, snippetOpen) || !strings.Contains(hits[0].Snippet, snippetClose) {
			t.Errorf("%q: snippet without markers %q", cs.match, hits[0].Snippet)
		}
	}
	h := must(c.SearchHits(f.ctx, schema.TableMemories, "deploying", 10))
	if h[0].Snippet != "we <b>deployed</b> the service on friday" {
		t.Errorf("snippet %q", h[0].Snippet)
	}
	if _, err := c.SearchHits(f.ctx, schema.TableMemories, `"unterminated`, 10); !errs.Has(err, errs.InvalidInput) {
		t.Errorf("bad match: %v", err)
	}
	if _, err := c.SearchHits(f.ctx, schema.TableMemories, "x", 0); !errs.Has(err, errs.InvalidInput) {
		t.Errorf("zero limit: %v", err)
	}
	if _, err := c.SearchHits(f.ctx, "nosuch", "x", 1); !errs.Has(err, errs.NotFound) {
		t.Errorf("unknown table: %v", err)
	}
	if got := ColumnMatch([]string{"title", "body"}, "a OR b"); got != "{f_title f_body} : (a OR b)" {
		t.Errorf("ColumnMatch %q", got)
	}
	if got := ColumnMatch(nil, "a"); got != "a" {
		t.Errorf("ColumnMatch empty %q", got)
	}
}

// TestSearchTurkishI pins I-9: the dotted and dotless i meet I and İ, so
// a word is found whichever form the writer and the searcher typed.
func TestSearchTurkishI(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("c.db")
	prj := newPrj()
	tr := newMem()
	f.apply(c, store.SourceRepo, "1", f.memory(tr, prj, "İSTANBUL ıspanak Iğdır DİYARBAKIR"))
	for _, q := range []string{"istanbul", "İstanbul", "ISTANBUL", "ıspanak", "ISPANAK", "ispanak", "diyarbakır", "DIYARBAKIR", "ığdır", "igdir"} {
		hits := must(c.SearchHits(f.ctx, schema.TableMemories, q, 10))
		if len(hits) != 1 || hits[0].ID != tr {
			t.Errorf("%q: %v", q, hits)
			continue
		}
		// the snippet shows the row's own letters, not the folded index
		if plain := strings.NewReplacer(snippetOpen, "", snippetClose, "").Replace(hits[0].Snippet); plain != "İSTANBUL ıspanak Iğdır DİYARBAKIR" {
			t.Errorf("%q: snippet %q", q, hits[0].Snippet)
		}
	}
}

// TestSearchAcrossSourcesOneFile: one cache file holding two sources
// searches both and orders by score over all of them (a global bm25 order
// within the file); the engine merges separate store files itself.
func TestSearchAcrossSourcesOneFile(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("c.db")
	a, b := newDec(), newDec()
	f.apply(c, store.SourceRepo, "1", f.decision(a, "unrelated", "mentions redis once"))
	f.apply(c, "agency", "1", f.decision(b, "redis", "redis everywhere"))
	hits := must(c.SearchHits(f.ctx, schema.TableDecisions, "redis", 10))
	if len(hits) != 2 || hits[0].ID != b || hits[1].ID != a {
		t.Fatalf("hits %v", hits)
	}
}

// pairs lists decision_tags' (source, from, to) triples, sorted.
func pairs(t *testing.T, c *Cache) []string {
	t.Helper()
	rs, err := c.db.QueryContext(context.Background(), `SELECT "_source" || '|' || from_id || '|' || to_id FROM "decision_tags" ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rs.Close() }()
	var out []string
	for rs.Next() {
		var s string
		if err := rs.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

// TestJoinFill: the cache-only join table of tag_ids holds one pair per
// (source, row, tag): a duplicate id in the list gives one pair, an update
// replaces the pairs, a tombstone and Rebuild(source) clear them, two
// sources keep their own pairs, and a non-id item is skipped.
func TestJoinFill(t *testing.T) {
	t.Parallel()
	if schema.JoinDecisionTags != "decision_tags" {
		t.Fatal("join table renamed: update pairs")
	}
	f := newFixture(t)
	c := f.open("c.db")
	d := newDec()
	t1, t2, t3 := newTag(), newTag(), newTag()
	pair := func(src store.Source, tag ids.AnyID) string {
		return string(src) + "|" + d.String() + "|" + tag.String()
	}
	sorted := func(xs ...string) []string { slices.Sort(xs); return xs }

	f.apply(c, store.SourceRepo, "1", f.decision(d, "t", "b", t1, t1, t2))
	if got := pairs(t, c); !slices.Equal(got, sorted(pair(store.SourceRepo, t1), pair(store.SourceRepo, t2))) {
		t.Fatalf("duplicate id: %v", got)
	}
	f.apply(c, store.SourceRepo, "2", f.decision(d, "t", "b", t3))
	if got := pairs(t, c); !slices.Equal(got, []string{pair(store.SourceRepo, t3)}) {
		t.Fatalf("update: %v", got)
	}
	f.apply(c, "agency", "1", f.decision(d, "t", "b", t1))
	if got := pairs(t, c); !slices.Equal(got, sorted(pair("agency", t1), pair(store.SourceRepo, t3))) {
		t.Fatalf("two sources: %v", got)
	}
	f.apply(c, store.SourceRepo, "3", f.tomb(schema.TableDecisions, d))
	if got := pairs(t, c); !slices.Equal(got, []string{pair("agency", t1)}) {
		t.Fatalf("tombstone: %v", got)
	}
	if err := c.Rebuild(f.ctx, "agency"); err != nil {
		t.Fatal(err)
	}
	if got := pairs(t, c); len(got) != 0 {
		t.Fatalf("rebuild: %v", got)
	}
	// A list item that is not an id (a hand-edited row) is skipped.
	doc := must(f.decision(d, "t", "b").Decode())
	doc.Fields[schema.FieldTagIDs] = must(value.ListOf(value.KindText, text(t, "not-an-id"), text(t, t2.String())))
	f.apply(c, store.SourceRepo, "4", must(store.FromRow(doc)))
	if got := pairs(t, c); !slices.Equal(got, []string{pair(store.SourceRepo, t2)}) {
		t.Fatalf("non-id item: %v", got)
	}
	// A mixed array decodes from the file as JSON, not a List: its id
	// items still make pairs (the form must not hide them).
	doc.Fields[schema.FieldTagIDs] = must(value.JSONOf([]byte(`["` + t3.String() + `",5]`)))
	sr := must(store.FromRow(doc))
	back := must(sr.Decode())
	if _, isJSON := back.Fields[schema.FieldTagIDs].(value.JSON); !isJSON {
		t.Fatalf("fixture: mixed array decoded as %T", back.Fields[schema.FieldTagIDs])
	}
	f.apply(c, store.SourceRepo, "5", sr)
	if got := pairs(t, c); !slices.Equal(got, []string{pair(store.SourceRepo, t3)}) {
		t.Fatalf("json-form list: %v", got)
	}
}

// TestLayoutReset: a file stamped with another layout version (1, before
// the typed PK became (_source, id)) is dropped and rebuilt on Open: every
// row and cursor is gone, so the engine refills it from the stores.
func TestLayoutReset(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	path := filepath.Join(f.dir, "c.db")
	c := must(Open(f.ctx, path, f.reg, f.exp))
	f.apply(c, store.SourceRepo, "7", f.memory(newMem(), newPrj(), "old layout row"))
	if _, err := c.db.ExecContext(f.ctx, "PRAGMA user_version = 1"); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c = f.open("c.db")
	if n := count(t, c, `SELECT COUNT(*) FROM "memories"`); n != 0 {
		t.Fatalf("rows survived a layout reset: %d", n)
	}
	if cur := must(c.Cursor(f.ctx, store.SourceRepo)); cur != "" {
		t.Fatalf("cursor survived: %q", cur)
	}
	if v := count(t, c, "PRAGMA user_version"); v != layoutVersion {
		t.Fatalf("user_version %d", v)
	}
	id := newMem()
	f.apply(c, store.SourceRepo, "1", f.memory(id, newPrj(), "refilled"))
	if hits := must(c.Search(f.ctx, schema.TableMemories, "refilled", 5)); len(hits) != 1 || hits[0] != id {
		t.Fatalf("refill: %v", hits)
	}
	// The same version is not reset.
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c = f.open("c.db")
	if n := count(t, c, `SELECT COUNT(*) FROM "memories"`); n != 1 {
		t.Fatalf("same layout reset: %d", n)
	}
}

// TestCombinedFile: 25 sources applied into one file (past the attach
// limit) all come back through OpenLensCombined's <table>_all with source =
// _source, and an id two sources hold is E_AMBIGUOUS_ID from both the
// cache's and the lens's ResolveID, while a single-source id resolves.
func TestCombinedFile(t *testing.T) {
	t.Parallel()
	const n = 25
	f := newFixture(t)
	path := filepath.Join(f.dir, "combined.db")
	c := must(Open(f.ctx, path, f.reg, f.exp))
	prj, shared := newPrj(), newMem()
	var solo ids.AnyID
	for i := range n {
		src := store.Source("s" + strconv.Itoa(i))
		rows := []store.Row{f.memory(newMem(), prj, "row of "+string(src))}
		if i == 3 || i == 17 {
			rows = append(rows, f.memory(shared, prj, "copied"))
		}
		if i == 24 {
			solo = newMem()
			rows = append(rows, f.memory(solo, prj, "solo"))
		}
		f.apply(c, src, "1", rows...)
	}
	if _, err := c.ResolveID(f.ctx, shared); !errs.Has(err, errs.AmbiguousID) {
		t.Errorf("cache: shared id %v", err)
	}
	if hit := must(c.ResolveID(f.ctx, solo)); hit.Source != "s24" || hit.Table != schema.TableMemories {
		t.Errorf("cache: solo %+v", hit)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	l := must(OpenLensCombined(f.ctx, path))
	t.Cleanup(func() { _ = l.Close() })
	res := must(l.Query(f.ctx, `SELECT source, COUNT(*) FROM memories_all GROUP BY source`))
	if len(res.Rows) != n {
		t.Fatalf("sources in memories_all: %d", len(res.Rows))
	}
	for _, r := range res.Rows {
		want := "1"
		if s := r[0].String(); s == "s3" || s == "s17" || s == "s24" {
			want = "2"
		}
		if r[1].String() != want {
			t.Errorf("source %s: %s rows", r[0], r[1])
		}
	}
	if _, err := l.ResolveID(f.ctx, shared); !errs.Has(err, errs.AmbiguousID) {
		t.Errorf("lens: shared id %v", err)
	}
	if hit := must(l.ResolveID(f.ctx, solo)); hit.Source != "s24" {
		t.Errorf("lens: solo %+v", hit)
	}
	if _, err := OpenLensCombined(f.ctx, ""); !errs.Has(err, errs.InvalidInput) {
		t.Errorf("empty path: %v", err)
	}
}
