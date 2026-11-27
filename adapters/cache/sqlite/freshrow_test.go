package sqlite

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// freshWorld is a set of Apply batches that exercise every path a fresh
// row meets (bug hunt H-282, devco:D-1020): new rows of many tables, a
// tombstone of an id never seen, the same id twice in one batch, the same
// id in two sources, a meta key and an allowed value applied after the
// rows naming them (the re-index side path projects rows of the same
// batch before their turn), a kind with fields and resources (a kind
// table refill), join pairs, references, then updates and deletes of
// rows the file holds.
type freshBatch struct {
	source store.Source
	cursor string
	rows   []store.Row
}

func freshWorld(f *fixture) []freshBatch {
	t := f.t
	prj := newPrj()
	tag1, tag2 := newTag(), newTag()
	stage := must(ids.New[ids.MetaKey]())
	urgent := must(ids.New[ids.MetaValue]())
	kind := must(ids.New[ids.Kind]())
	host := must(ids.New[ids.KindField]())
	res1, res2 := must(ids.New[ids.Resource]()), must(ids.New[ids.Resource]())
	var mems []ids.AnyID
	for range 12 {
		mems = append(mems, newMem())
	}
	tasks := []ids.AnyID{newTsk(), newTsk(), newTsk()}
	dec := newDec()
	shared := newMem() // in both sources
	keyRow := f.live(schema.TableMetaKeys, stage.Any(), map[string]value.Value{
		fieldName: text(t, "stage"), "scope": text(t, "repo"), "type": text(t, "text"),
	})
	a1 := []store.Row{
		f.memory(mems[0], prj, "deploy notes for ${ref:"+mems[1].String()+"} and the gateway"),
		f.memory(mems[1], prj, "gateway runbook"),
		f.metaMemory(mems[2], "release train ıstanbul 東京", map[ids.ID[ids.MetaKey]]string{
			stage: `{"name":"stage","scope":"repo","v":"` + urgent.String() + `"}`,
		}),
		keyRow, // after the row naming it: re-indexes mems[2] in this batch
		f.metaValueRow(urgent, stage, "urgent", "Urgent now"),
		f.tomb(schema.TableMemories, mems[3]), // a tombstone of an id never seen
		f.memory(mems[4], prj, "first version of a twice-applied row"),
		f.memory(mems[4], prj, "second version of a twice-applied row"),
		f.decision(dec, "pick the queue", "we pick the queue ${ref:"+mems[0].String()+"}", tag1, tag2),
		f.taskRow(tasks[0], "", "a", false),
		f.taskRow(tasks[1], tasks[0].String(), "", false),
		f.taskRow(tasks[2], "", "b", true),
		f.kindRow(kind, "server"),
		f.fieldRow(host, kind, "host", "text", ids.ID[ids.Kind]{}),
		f.resource(res1, kind, "hub", map[ids.ID[ids.KindField]][2]string{host: {"host", `"hub.example.com"`}}),
		f.memory(shared, prj, "shared row as source a has it"),
		// the key row arrived above, so it re-indexed this row (already in
		// _rows) before its own turn: its own projection replaces that
		f.metaMemory(mems[8], "late row with ${ref:"+mems[1].String()+"} naming the key", map[ids.ID[ids.MetaKey]]string{
			stage: `{"name":"stage","scope":"repo","v":"` + urgent.String() + `"}`,
		}),
	}
	b1 := []store.Row{
		f.memory(shared, prj, "shared row as source b has it"),
		f.memory(mems[5], prj, "source b note citing ${ref:"+res1.String()+"}"),
		f.resource(res2, kind, "edge", map[ids.ID[ids.KindField]][2]string{host: {"host", `"edge.example.com"`}}),
		f.memory(mems[6], prj, "plain note in b"),
	}
	a2 := []store.Row{
		f.memory(mems[1], prj, "gateway runbook, second edition"), // an update of a held row
		f.tomb(schema.TableMemories, mems[0]),                     // a delete of a held row
		f.decision(dec, "pick the queue", "changed tags", tag2),
		f.taskRow(tasks[1], "", "c", false),
		f.memory(mems[7], prj, "a new row in a later batch ${ref:"+mems[1].String()+"}"),
		f.memory(mems[3], prj, "back after its tombstone"),
	}
	return []freshBatch{{"a", "1", a1}, {"b", "1", b1}, {"a", "2", a2}}
}

// TestFreshBuildMatchesReplaceBuild: a cache built with the fresh-row path
// (no clearing of rows the file never held) holds exactly what the
// replace path builds from the same batches: every table, row for row,
// FTS contents and matches included, and the FTS indexes pass SQLite's
// integrity check.
func TestFreshBuildMatchesReplaceBuild(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	batches := freshWorld(f)
	fresh := f.open("fresh.db")
	replace := f.open("replace.db")
	replace.noFresh = true
	for i, b := range batches {
		f.apply(fresh, b.source, b.cursor, b.rows...)
		f.apply(replace, b.source, b.cursor, b.rows...)
		got, want := dumpCache(t, fresh), dumpCache(t, replace)
		if d := firstDiff(got, want); d != "" {
			t.Fatalf("after batch %d the fresh build differs from the replace build:\n%s", i+1, d)
		}
		for _, c := range []*Cache{fresh, replace} {
			for _, fts := range ftsTables(t, c) {
				if _, err := c.db.ExecContext(context.Background(), `INSERT INTO "`+fts+`" ("`+fts+`") VALUES ('integrity-check')`); err != nil {
					t.Fatalf("batch %d: %s integrity: %v", i+1, fts, err)
				}
			}
		}
		for _, q := range []string{"gateway", "urgent", "ıstanbul", "東京", "queue", "hub", "edition", "tombstone"} {
			g := must(fresh.Search(f.ctx, schema.TableMemories, q, 50))
			w := must(replace.Search(f.ctx, schema.TableMemories, q, 50))
			if !slices.Equal(g, w) {
				t.Fatalf("batch %d: search %q: fresh %v, replace %v", i+1, q, g, w)
			}
		}
	}
	// the world reaches every path: references, joins, meta, resources
	for q, min := range map[string]int{
		`SELECT COUNT(*) FROM row_refs`:      2,
		`SELECT COUNT(*) FROM row_expanded`:  2,
		`SELECT COUNT(*) FROM decision_tags`: 1,
		`SELECT COUNT(*) FROM resources`:     2,
	} {
		if n := count(t, fresh, q); n < min {
			t.Errorf("%s = %d, want at least %d (the world must reach this path)", q, n, min)
		}
	}
}

// TestFreshRowsClearNothing: a first build runs no per-row DELETE (the
// fresh-row path), and the batched re-expansion asks row_refs once per
// referrersBatch ids, not once per id. Not parallel: it reads the
// package's statement counters.
func TestFreshRowsClearNothing(t *testing.T) {
	f := newFixture(t)
	c := f.open("c.db")
	prj := newPrj()
	var rows []store.Row
	var refIDs []string
	for i := range 300 {
		id := newMem()
		rows = append(rows, f.memory(id, prj, fmt.Sprintf("note %d citing ${ref:%s}", i, prj)))
		refIDs = append(refIDs, id.String())
	}
	before := deletesRun.Load()
	f.apply(c, "s", "1", rows...)
	if n := deletesRun.Load() - before; n != 0 {
		t.Fatalf("a first build of 300 new rows ran %d DELETE statements, want 0", n)
	}
	for range 4 {
		refIDs = append(refIDs, refIDs...) // 4,800 ids
	}
	before = queriesRun.Load()
	if _, err := c.ReexpandIDs(f.ctx, refIDs); err != nil {
		t.Fatal(err)
	}
	// 4,800 ids at 500 a query: 10 queries (one per id before: 4,800)
	if n, most := queriesRun.Load()-before, int64(12); n > most {
		t.Fatalf("re-expanding %d ids ran %d row_refs queries, want at most %d", len(refIDs), n, most)
	}
	// an update of the same rows clears their old copies again
	before = deletesRun.Load()
	f.apply(c, "s", "2", f.memory(rows[0].ID, prj, "changed"))
	if n := deletesRun.Load() - before; n == 0 {
		t.Fatal("an update of a held row ran no DELETE: its old FTS entry and references would stay")
	}
}

// dumpCache lists every row of every table of c (FTS shadow tables
// excepted: their layout depends on merge timing, not on content) as
// "table|quoted values", sorted; FTS tables are dumped by their content.
func dumpCache(t *testing.T, c *Cache) []string {
	t.Helper()
	var out []string
	for _, tbl := range cacheTables(t, c) {
		cols := columnsOf(t, c, tbl)
		if len(cols) == 0 {
			continue
		}
		parts := make([]string, len(cols))
		for i, col := range cols {
			parts[i] = `quote("` + col + `")`
		}
		out = append(out, strings1(t, c, `SELECT ? || '|' || `+strings.Join(parts, ` || '|' || `)+` FROM "`+tbl+`"`, tbl)...)
	}
	slices.Sort(out)
	return out
}

// cacheTables lists c's tables, FTS shadow tables left out.
func cacheTables(t *testing.T, c *Cache) []string {
	t.Helper()
	fts := ftsTables(t, c)
	var out []string
	for _, name := range strings1(t, c, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE ? ORDER BY 1`, "sqlite_%") {
		shadow := false
		for _, f := range fts {
			if strings.HasPrefix(name, f+"_") {
				shadow = true
			}
		}
		if !shadow {
			out = append(out, name)
		}
	}
	return out
}

// ftsTables lists c's FTS5 tables.
func ftsTables(t *testing.T, c *Cache) []string {
	t.Helper()
	return strings1(t, c, `SELECT name FROM sqlite_master WHERE type = 'table' AND sql LIKE ?`, "%USING fts5%")
}

// firstDiff describes the first line where got and want differ ("" when
// equal).
func firstDiff(got, want []string) string {
	for i := range max(len(got), len(want)) {
		var g, w string
		if i < len(got) {
			g = got[i]
		}
		if i < len(want) {
			w = want[i]
		}
		if g != w {
			return fmt.Sprintf("line %d\n fresh:   %s\n replace: %s", i, g, w)
		}
	}
	return ""
}
