package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// metaProbe is the value of the sensitive key that must never show.
const metaProbe = "vpn-7-probe"

// metaMemory is a memories row carrying meta entries (raw entry JSON by key).
func (f *fixture) metaMemory(id ids.AnyID, body string, meta map[ids.ID[ids.MetaKey]]string) store.Row {
	f.t.Helper()
	sr := f.memory(id, newPrj(), body)
	r := must(sr.Decode())
	r.Meta = map[ids.ID[ids.MetaKey]]value.JSON{}
	for k, raw := range meta {
		r.Meta[k] = must(value.JSONOf([]byte(raw)))
	}
	return must(store.FromRow(r))
}

func TestRowMetaViewSearchAndMask(t *testing.T) {
	f := newFixture(t)
	c := f.open("repo.db")
	jira, host, deploy, pages, vault := must(ids.New[ids.MetaKey]()), must(ids.New[ids.MetaKey]()), must(ids.New[ids.MetaKey]()), must(ids.New[ids.MetaKey]()), must(ids.New[ids.MetaKey]())
	m1 := newMem()
	f.apply(c, store.SourceRepo, "1",
		// vault: sensitive by the registry only (the entry does not say so)
		f.live(schema.TableMetaKeys, vault.Any(), map[string]value.Value{
			fieldName: text(t, "vault"), "scope": text(t, "repo"), "type": text(t, "text"), fieldSensitive: value.BoolOf(true),
		}),
		f.metaMemory(m1, "booking flow", map[ids.ID[ids.MetaKey]]string{
			jira:   `{"name":"jira","scope":"repo","v":"HUB-412"}`,
			host:   `{"name":"vpn_host","scope":"repo","sensitive":true,"v":"` + metaProbe + `"}`,
			deploy: `{"name":"deploy","scope":"repo","v":{"region":"eu-central","n":3}}`,
			pages:  `{"name":"pages","scope":"repo","v":["invoices","billing-export"]}`,
			vault:  `{"name":"vault","scope":"repo","v":"` + metaProbe + `-2"}`,
		}),
	)
	// view rows: presence + values, object fields with paths, list items
	got := strings1(t, c, `SELECT key_name || '|' || path || '|' || item_idx || '|' || COALESCE(value_text, '') FROM row_meta WHERE entity_id = ? AND item_idx >= 0 ORDER BY 1`, m1.String())
	want := []string{"deploy.n|n|0|3", "deploy.region|region|0|eu-central", "jira||0|HUB-412", "pages||0|invoices", "pages||1|billing-export",
		"vault||0|" + metaProbe + "-2", "vpn_host||0|" + metaProbe}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("row_meta:\n got %v\nwant %v", got, want)
	}
	// FTS: values findable, sensitive ones (entry flag or registry) never
	for q, n := range map[string]int{`"HUB-412"`: 1, `eu`: 1, `invoices`: 1, `"vpn-7-probe"`: 0, `probe`: 0} {
		hits := must(c.Search(context.Background(), schema.TableMemories, q, 10))
		if len(hits) != n {
			t.Errorf("search %s: %d hits, want %d", q, len(hits), n)
		}
	}
	// a key turning sensitive drops its values from the index
	f.apply(c, store.SourceRepo, "2", f.live(schema.TableMetaKeys, jira.Any(), map[string]value.Value{
		fieldName: text(t, "jira"), "scope": text(t, "repo"), "type": text(t, "text"), fieldSensitive: value.BoolOf(true),
	}))
	if hits := must(c.Search(context.Background(), schema.TableMemories, `"HUB-412"`, 10)); len(hits) != 0 {
		t.Errorf("a key turned sensitive is still indexed: %v", hits)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	srcs := []LensSource{{Name: string(store.SourceRepo), Path: filepath.Join(f.dir, "repo.db")}}
	masked := openMasked(t, srcs, true)
	for _, q := range []string{`SELECT * FROM row_meta`, `SELECT * FROM row_meta_all`, `SELECT group_concat(value_text) FROM row_meta_all WHERE value_text LIKE '%probe%'`} {
		res := must(masked.Query(context.Background(), q))
		if d := dump(res); strings.Contains(d, metaProbe) || strings.Contains(d, "HUB-412") {
			t.Errorf("%s leaks a sensitive meta value: %s", q, d)
		}
	}
	res := must(masked.Query(context.Background(), `SELECT COUNT(*) FROM row_meta_all WHERE key_name = 'vpn_host' AND value_text = '`+MaskedValue+`'`))
	if res.Rows[0][0].String() != "1" {
		t.Errorf("sensitive meta row not masked as %s: %v", MaskedValue, res.Rows)
	}
	plain := openMasked(t, srcs, false)
	if d := dump(must(plain.Query(context.Background(), `SELECT value_text FROM row_meta_all`))); !strings.Contains(d, metaProbe) {
		t.Errorf("unmasked lens hides the value: %s", d)
	}
}

// TestRowMetaSkipsTrash: a row in the trash has no metadata rows (3.22).
func TestRowMetaSkipsTrash(t *testing.T) {
	f := newFixture(t)
	c := f.open("repo.db")
	m := newMem()
	sr := f.metaMemory(m, "gone", map[ids.ID[ids.MetaKey]]string{must(ids.New[ids.MetaKey]()): `{"name":"jira","scope":"repo","v":"HUB-1"}`})
	r := must(sr.Decode())
	r.Fields["deleted_at"] = text(t, "2026-11-04T00:00:00Z")
	f.apply(c, store.SourceRepo, "1", must(store.FromRow(r)))
	if n := count(t, c, `SELECT COUNT(*) FROM row_meta WHERE entity_id = ?`, m.String()); n != 0 {
		t.Fatalf("trashed row has %d row_meta rows", n)
	}
}

func TestLensExplain(t *testing.T) {
	_, srcs, _, _ := threeCaches(t)
	l := openLens(t, srcs)
	for _, q := range []string{`EXPLAIN SELECT * FROM memories_all`, `explain query plan SELECT 1`} {
		if _, err := l.Query(context.Background(), q); err != nil {
			t.Errorf("%s: %v", q, err)
		}
	}
	for _, q := range []string{`EXPLAIN DELETE FROM s_0.memories`, `EXPLAIN QUERY DELETE FROM s_0.memories`, `EXPLAIN QUERY PLAN PRAGMA query_only = OFF`, `EXPLAIN`} {
		if _, err := l.Query(context.Background(), q); err == nil {
			t.Errorf("accepted: %s", q)
		}
	}
}
