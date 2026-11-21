package sqlite

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// rowMetaRefView is row_meta as it read before D-834: straight from every
// `_rows` document. The test builds it from the live definition with the
// source swapped back, so any other change to the view is compared too.
func rowMetaRefView(t *testing.T) string {
	t.Helper()
	def := rowMetaDDL[1]
	newFrom := `FROM "` + tblRowMetaSrc + `" r, json_each(r.meta) m
		WHERE json_type(m.value) = 'object'`
	oldFrom := `FROM "` + tblRows + `" r, json_each(r.doc, '$.meta') m
		WHERE ` + rowMetaLive("r") + ` AND json_type(m.value) = 'object'`
	head := `CREATE VIEW IF NOT EXISTS "` + ViewRowMeta + `"`
	if !strings.Contains(def, newFrom) || !strings.HasPrefix(def, head) {
		t.Fatalf("row_meta definition changed shape; update the reference view:\n%s", def)
	}
	return strings.Replace(strings.Replace(def, newFrom, oldFrom, 1), head, `CREATE TEMP VIEW "row_meta_ref"`, 1)
}

// rowMetaAll is every column of a row_meta-shaped view, one line per row,
// sorted.
func rowMetaAll(t *testing.T, c *Cache, view string) []string {
	t.Helper()
	q := `SELECT _source || '|' || entity_table || '|' || entity_id || '|' || key_id || '|' || COALESCE(key_name, '~') || '|' ||
		COALESCE(scope, '~') || '|' || sensitive || '|' || path || '|' || item_idx || '|' || COALESCE(value_text, '~') || '|' ||
		COALESCE(value_fold, '~') || '|' || COALESCE(value_num, '~') || '|' || COALESCE(value_bool, '~') || '|' ||
		COALESCE(value_id, '~') || '|' || COALESCE(value_json, '~') FROM "` + view + `" WHERE ? = '1'`
	return strings1(t, c, q, "1")
}

// TestRowMetaSrcMatchesRows: the row_meta view reads the metadata copy kept
// beside `_rows` (devco D-834: a date question read every row's whole
// document for a few metadata values, ~65 ms of ~185). Over inserts,
// updates that change or drop the metadata, tombstones, the trash, a
// quarantined row and a file from before the copy (no table, no triggers),
// the view answers exactly as the old view over `_rows` did.
func TestRowMetaSrcMatchesRows(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("repo.db")
	ctx := context.Background()
	if _, err := c.db.ExecContext(ctx, rowMetaRefView(t)); err != nil {
		t.Fatal(err)
	}
	same := func(step string, minRows int) {
		t.Helper()
		got, want := rowMetaAll(t, c, ViewRowMeta), rowMetaAll(t, c, "row_meta_ref")
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("%s: row_meta differs from the view over _rows:\n got %v\nwant %v", step, got, want)
		}
		if len(want) < minRows {
			t.Fatalf("%s: only %d row_meta rows, want at least %d (the fixture lost its metadata)", step, len(want), minRows)
		}
	}
	day, owed := must(ids.New[ids.MetaKey]()), must(ids.New[ids.MetaKey]())
	m1, m2, m3, m4, m5, m6 := newMem(), newMem(), newMem(), newMem(), newMem(), newMem()
	trashed := func(r store.Row) store.Row {
		d := must(r.Decode())
		d.Fields["deleted_at"] = text(t, "2026-11-04T00:00:00Z")
		return must(store.FromRow(d))
	}
	f.apply(c, store.SourceRepo, "1",
		f.metaMemory(m1, "one", map[ids.ID[ids.MetaKey]]string{day: `{"name":"day","scope":"repo","v":"2026-11-12"}`}),
		f.metaMemory(m2, "two", map[ids.ID[ids.MetaKey]]string{day: `{"name":"day","scope":"repo","v":"2026-10-12"}`, owed: `{"name":"owed_to","scope":"repo","v":["ana-diaz","ben-ode"]}`}),
		f.metaMemory(m3, "three", map[ids.ID[ids.MetaKey]]string{day: `{"name":"day","scope":"repo","v":"2026-09-07"}`}),
		trashed(f.metaMemory(m4, "four", map[ids.ID[ids.MetaKey]]string{day: `{"name":"day","scope":"repo","v":"2026-07-21"}`})),
		f.memory(m5, newPrj(), "five, no metadata"),
		f.metaMemory(m6, "six", map[ids.ID[ids.MetaKey]]string{owed: `{"name":"owed_by","scope":"repo","v":{"who":"cy-lam","n":2}}`}),
	)
	same("insert", 10)
	f.apply(c, store.SourceRepo, "2",
		f.metaMemory(m1, "one, moved", map[ids.ID[ids.MetaKey]]string{day: `{"name":"day","scope":"repo","v":"2026-11-13"}`}),
		f.tomb(schema.TableMemories, m3),
		f.memory(m6, newPrj(), "six, metadata dropped"),
		trashed(f.metaMemory(m2, "two, trashed", map[ids.ID[ids.MetaKey]]string{day: `{"name":"day","scope":"repo","v":"2026-10-12"}`})),
		f.metaMemory(m4, "four, restored", map[ids.ID[ids.MetaKey]]string{day: `{"name":"day","scope":"repo","v":"2026-07-21"}`}),
	)
	same("update", 4)
	if got := strings1(t, c, `SELECT value_text FROM row_meta WHERE key_name = 'day' AND item_idx >= 0 AND entity_id = ?`, m1.String()); strings.Join(got, ",") != "2026-11-13" {
		t.Fatalf("updated day: %v", got)
	}
	if _, err := c.db.ExecContext(ctx, `INSERT INTO "`+tblMetaQuarantine+`" (source, tbl, id, problems) VALUES (?, ?, ?, '[]')`, string(store.SourceRepo), schema.TableMemories, m4.String()); err != nil {
		t.Fatal(err)
	}
	same("quarantine", 2)
	// a file from before the copy: no table, no triggers, an older DDL stamp
	for _, s := range []string{`DROP TRIGGER "_row_meta_src_ins"`, `DROP TRIGGER "_row_meta_src_upd"`, `DROP TRIGGER "_row_meta_src_del"`, `DROP VIEW "` + ViewRowMeta + `"`, `DROP TABLE "` + tblRowMetaSrc + `"`, `PRAGMA application_id = 0`} {
		if _, err := c.db.ExecContext(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	m7 := newMem()
	f.apply(c, store.SourceRepo, "3", f.metaMemory(m7, "seven, written before the copy", map[ids.ID[ids.MetaKey]]string{day: `{"name":"day","scope":"repo","v":"2026-11-01"}`}))
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c = f.open("repo.db")
	if err := c.ensureInit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.ExecContext(ctx, rowMetaRefView(t)); err != nil {
		t.Fatal(err)
	}
	same("healed", 4)
	if n := count(t, c, `SELECT COUNT(*) FROM row_meta WHERE entity_id = ?`, m7.String()); n == 0 {
		t.Fatal("a row written before the copy existed has no row_meta rows after the reopen")
	}
	f.apply(c, store.SourceRepo, "4", f.metaMemory(m5, "five, metadata added", map[ids.ID[ids.MetaKey]]string{day: `{"name":"day","scope":"repo","v":"2026-11-02"}`}))
	same("after heal", 5)
	// any other writer of `_rows` (an older binary, the source drop): the
	// triggers live in the file, so plain UPDATE and DELETE keep the copy too
	for _, w := range []struct{ step, sql, arg string }{
		{"update deleted", `UPDATE "` + tblRows + `" SET deleted = 1 WHERE id = ?`, m1.String()},
		{"update doc", `UPDATE "` + tblRows + `" SET doc = json_set(doc, '$.meta', json('{}')) WHERE id = ?`, m5.String()},
	} {
		if _, err := c.db.ExecContext(ctx, w.sql, w.arg); err != nil {
			t.Fatalf("%s: %v", w.step, err)
		}
		same(w.step, 2)
	}
	if _, err := c.db.ExecContext(ctx, `DELETE FROM "`+tblRows+`" WHERE source = ?`, string(store.SourceRepo)); err != nil {
		t.Fatal(err)
	}
	same("source dropped", 0)
	if n := count(t, c, `SELECT COUNT(*) FROM "`+tblRowMetaSrc+`"`); n != 0 {
		t.Fatalf("%d metadata copies left after every row went", n)
	}
}
