package sqlite

import (
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// ruleTag lets tests mint ids for a custom table on the rul prefix.
type ruleTag struct{}

func (ruleTag) Prefix() ids.Prefix { return ids.PrefixRule }

// sqlWords are SQL keywords that are valid snake_case field names.
var sqlWords = []string{
	"order", "select", "group", "table", "from", "where", "values", "key", "index",
	"rank", "rowid", "oid", "match", "default", "check", "primary", "references", "null", "true",
}

// relDef is a custom table with every relation shape and odd field names.
func relDef() schema.TableDef {
	items := []schema.Item{
		schema.Synced(),
		schema.Ref("parent_id", "odd_rules"),
		schema.Text("target_type"),
		schema.Ref("target_id", ""),
		schema.Ref("memory_id", schema.TableMemories),
		schema.SelfRel("parent", "parent_id", schema.SetNull),
		schema.PolymorphicRel("target", "target_type", "target_id", schema.TombstoneOnly),
		schema.BelongsToRel("memory", "memory_id", schema.TableMemories, schema.TombstoneOnly),
		schema.RefList("memory_ids", schema.TableMemories),
		schema.ManyToManyRel("mems", "memory_ids", schema.TableMemories, "rule_memories", schema.Cascade),
	}
	for _, w := range sqlWords {
		items = append(items, schema.Text(w))
	}
	items = append(items, schema.FTS("rank", "order", "select"), schema.UniqueOn("key"))
	return schema.Table("odd_rules", ids.PrefixEntityTag, items...)
}

// TestRelationsAndOddNames: self, polymorphic, belongs-to and many-to-many
// become indexes (and a join table), never foreign keys; SQL keywords and
// FTS5's reserved names work as fields end to end.
func TestRelationsAndOddNames(t *testing.T) {
	f := newFixture(t)
	f.reg = must(f.reg.With(relDef()))
	c := f.open("c.db")
	indexed := func(table string, cols ...string) bool {
		rs := must(c.db.Query(`SELECT l.name, l."unique" FROM pragma_index_list(?) AS l`, table))
		defer func() { _ = rs.Close() }()
		var names []string
		for rs.Next() {
			var n string
			var u int
			if err := rs.Scan(&n, &u); err != nil {
				t.Fatal(err)
			}
			if u == 1 && n != "sqlite_autoindex_"+table+"_1" {
				t.Errorf("%s: index %s is UNIQUE", table, n)
			}
			names = append(names, n)
		}
		for _, n := range names {
			got := strings1(t, c, `SELECT name FROM pragma_index_info(?) ORDER BY seqno`, n)
			if slices.Equal(sortedCopy(cols), got) {
				return true
			}
		}
		return false
	}
	for _, cols := range [][]string{{"parent_id"}, {"target_id", "target_type"}, {"memory_id"}, {"target_id"}, {colSource}, {"key"}} {
		if !indexed("odd_rules", cols...) {
			t.Errorf("rules: no index on %v", cols)
		}
	}
	for _, col := range []string{joinFrom, joinTo} {
		if !indexed("rule_memories", col) {
			t.Errorf("join table: no index on %s", col)
		}
	}
	for _, tb := range []string{"odd_rules", "rule_memories"} {
		if n := count(t, c, `SELECT COUNT(*) FROM pragma_foreign_key_list(?)`, tb); n != 0 {
			t.Errorf("%s has foreign keys", tb)
		}
	}
	vals := map[string]value.Value{
		"parent_id": must(value.RefOf(must(ids.New[ruleTag]()).Any())),
		"target_id": must(value.RefOf(newMem())), "target_type": text(t, "memories"),
	}
	for _, w := range sqlWords {
		vals[w] = text(t, "value of "+w+` with 'quotes' and "double" ; -- not sql`)
	}
	vals["rank"] = text(t, "zebra")
	id := must(ids.New[ruleTag]()).Any()
	f.apply(c, store.SourceRepo, "1", f.live("odd_rules", id, vals))
	if n := count(t, c, `SELECT COUNT(*) FROM "odd_rules" WHERE "order" = ? AND "rank" = 'zebra'`, vals["order"].String()); n != 1 {
		t.Fatal("odd-named values did not round-trip")
	}
	if hits := must(c.Search(f.ctx, "odd_rules", "zebra", 5)); len(hits) != 1 || hits[0] != id {
		t.Fatalf("search over a field named rank = %v", hits)
	}
}

func sortedCopy(s []string) []string {
	c := slices.Clone(s)
	slices.Sort(c)
	return c
}

// TestIdentRefusesOddNames: names no TableDef can hold are refused with
// E_INVALID_IDENTIFIER before any SQL is built, and planFor fails instead of
// building half a table.
func TestIdentRefusesOddNames(t *testing.T) {
	bad := []string{"", "Order", "drop table x", `a"b`, "a'b", "ünïcode", "日本", "a;b", "a-b", "1abc", "a\x00b", "a\nb", "`x`", "[x]"}
	for _, n := range bad {
		if _, err := ident(n); !errs.Has(err, errs.InvalidIdentifier) {
			t.Errorf("ident(%q) = %v", n, err)
		}
		def := schema.TableDef{Name: row.Table("t"), Fields: []schema.FieldDef{{Name: n, Kind: value.KindText}}}
		if _, err := planFor(def, ""); err == nil {
			t.Errorf("planFor accepted field %q", n)
		}
		def = schema.TableDef{Name: row.Table(n)}
		if _, err := planFor(def, ""); err == nil {
			t.Errorf("planFor accepted table %q", n)
		}
	}
	for _, n := range append([]string{"_rows", "res_knd_0193abc", "a__b"}, sqlWords...) {
		q, err := ident(n)
		if err != nil || q != `"`+n+`"` {
			t.Errorf("ident(%q) = %q, %v", n, q, err)
		}
	}
	if got := literal("it's"); got != "'it''s'" {
		t.Errorf("literal = %s", got)
	}
}

// TestAffinityEveryKind: every value kind but null has a declared type.
func TestAffinityEveryKind(t *testing.T) {
	for _, k := range value.Kinds() {
		typ, err := affinity(k)
		if k == value.KindNull {
			if err == nil {
				t.Error("null got a column type")
			}
			continue
		}
		if err != nil || !slices.Contains([]string{sqlText, sqlNumeric, sqlInteger}, typ) {
			t.Errorf("%s → %q, %v", k, typ, err)
		}
	}
}

// TestCheckReadOnly: the statement guard over comments, quotes and
// trailing semicolons.
func TestCheckReadOnly(t *testing.T) {
	ok := []string{
		"SELECT 1", "select 1;", "  -- lead\n SELECT 1 ; -- trail", "/* c */ WITH x AS (SELECT 1) SELECT * FROM x",
		"VALUES (1)", "EXPLAIN SELECT 1", "explain query plan WITH x AS (SELECT 1) SELECT * FROM x", "SELECT ';DROP'", `SELECT "a;b" FROM t`, "SELECT `a;b`", "SELECT [a;b]", "SELECT 'it''s; fine'",
	}
	for _, q := range ok {
		if err := checkReadOnly(q); err != nil {
			t.Errorf("refused %q: %v", q, err)
		}
	}
	bad := []string{
		"", ";", "INSERT INTO t VALUES (1)", "ATTACH 'x' AS y", "PRAGMA query_only=0", "SELECT 1; SELECT 2",
		"SELECT 1;;", "selectx 1", "/* SELECT */ DELETE FROM t", "SELECT 'a'; DROP TABLE t", "EXPLAIN DELETE FROM t", "EXPLAIN QUERY DELETE FROM t", "EXPLAIN SELECT 1; DROP TABLE t", "DETACH s_0",
	}
	for _, q := range bad {
		if err := checkReadOnly(q); !errs.Has(err, errs.ReadOnly) {
			t.Errorf("accepted %q (%v)", q, err)
		}
	}
}
