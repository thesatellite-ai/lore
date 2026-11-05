package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestLensEscapeSet (METADATA 3.16 "tests cover every escape", later 494):
// beyond TestLensRefusesWrites, the lens refuses ATTACH of an existing
// file, VACUUM INTO, PRAGMA writable_schema, load_extension, a write
// hidden after EXPLAIN, and a second statement after a string holding a
// semicolon; EXPLAIN [QUERY PLAN] of a read is allowed; and on a masked
// lens a sensitive meta value never comes back through json_extract over
// the raw row columns, row_meta or the _all views. No file is created or
// changed.
func TestLensEscapeSet(t *testing.T) {
	f := newFixture(t)
	c := f.open("repo.db")
	vault := must(ids.New[ids.MetaKey]())
	m := newMem()
	f.apply(c, store.SourceRepo, "1",
		f.live(schema.TableMetaKeys, vault.Any(), map[string]value.Value{
			fieldName: text(t, "vault"), "scope": text(t, "repo"), "type": text(t, "text"), fieldSensitive: value.BoolOf(true),
		}),
		f.metaMemory(m, "plain body", map[ids.ID[ids.MetaKey]]string{vault: `{"name":"vault","scope":"repo","v":"` + metaProbe + `"}`}),
	)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	srcs := []LensSource{{Name: string(store.SourceRepo), Path: filepath.Join(f.dir, "repo.db")}}
	l := openMasked(t, srcs, true)
	other := filepath.Join(f.dir, "repo.db")
	created := filepath.Join(f.dir, "vacuumed.db")
	refused := []string{
		`ATTACH DATABASE '` + other + `' AS again`,
		`VACUUM INTO '` + created + `'`,
		`PRAGMA writable_schema = 1`,
		`PRAGMA writable_schema`,
		`SELECT load_extension('/tmp/x')`,
		`EXPLAIN DELETE FROM s_0.memories`,
		`EXPLAIN QUERY PLAN UPDATE s_0.memories SET body = 'x'`,
		`SELECT 'a;b'; DELETE FROM s_0.memories`,
		`SELECT "x;" FROM memories_all; DROP VIEW memories_all`,
		`REPLACE INTO s_0.memories (id) VALUES ('x')`,
	}
	for _, q := range refused {
		if _, err := l.Query(context.Background(), q); err == nil {
			t.Errorf("accepted: %s", q)
		}
	}
	if fileExists(created) {
		t.Error("VACUUM INTO created a file")
	}
	for _, q := range []string{`EXPLAIN SELECT * FROM memories_all`, `EXPLAIN QUERY PLAN SELECT * FROM memories_all`, `explain query plan select 1`} {
		if _, err := l.Query(context.Background(), q); err != nil {
			t.Errorf("a read under EXPLAIN refused: %s: %v", q, err)
		}
	}
	// the sensitive value: every path a reader might try on the masked lens
	reads := []string{
		`SELECT * FROM row_meta_all`,
		`SELECT * FROM row_meta`,
		`SELECT value_text, value_lower FROM row_meta_all`,
		`SELECT * FROM memories_all`,
		`SELECT * FROM memories`,
	}
	for _, q := range reads {
		res, err := l.Query(context.Background(), q)
		if err != nil {
			continue // a denied read is fine; a leak is not
		}
		if d := dump(res); strings.Contains(d, metaProbe) {
			t.Errorf("%s leaks the sensitive value", q)
		}
	}
	// the raw document is reachable only through the internal tables,
	// which a masked lens must refuse or mask
	for _, q := range []string{
		`SELECT json_extract(doc, '$.meta') FROM s_0._rows`,
		`SELECT doc FROM "s_0"."_rows"`,
		`SELECT json_extract(meta, '$') FROM memories_all`,
	} {
		res, err := l.Query(context.Background(), q)
		if err != nil {
			if !errs.Has(err, errs.SQLRefused) && !errs.Has(err, errs.InvalidInput) && !errs.Has(err, errs.ReadOnly) {
				t.Logf("%s: %v", q, err)
			}
			continue
		}
		if d := dump(res); strings.Contains(d, metaProbe) {
			t.Errorf("%s leaks the sensitive value through the raw document", q)
		}
	}
}
