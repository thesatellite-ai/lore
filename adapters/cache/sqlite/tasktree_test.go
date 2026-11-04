package sqlite

import (
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// task builds a tasks row under parent ("" = top level); trashed sets
// deleted_at.
func (f *fixture) task(id ids.AnyID, parent string, trashed bool) store.Row {
	f.t.Helper()
	fields := map[string]value.Value{"title": text(f.t, "t")}
	if parent != "" {
		fields[schema.FieldTaskParent] = must(value.RefOf(must(ids.ParseAny(parent))))
	}
	if trashed {
		fields["deleted_at"] = text(f.t, "2026-11-04T10:00:00Z")
	}
	return f.live(schema.TableTasks, id, fields)
}

// newTsk mints a tsk_ id (no typed Task tag exists; the hex of a fresh
// UUIDv7, so ids still sort by minting order).
func newTsk() ids.AnyID {
	m := newMem().String()
	return must(ids.ParseAny(string(ids.PrefixTask) + m[strings.IndexByte(m, '_'):]))
}

// closure reads the closure as "ancestor>descendant@depth" lines.
func closure(t *testing.T, c *Cache, names map[string]string) map[string]bool {
	t.Helper()
	rs, err := c.db.QueryContext(t.Context(), `SELECT ancestor_id, descendant_id, depth FROM task_tree`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rs.Close() }()
	out := map[string]bool{}
	for rs.Next() {
		var a, d string
		var n int
		if err := rs.Scan(&a, &d, &n); err != nil {
			t.Fatal(err)
		}
		out[names[a]+">"+names[d]+"@"+strconv.Itoa(n)] = true
	}
	return out
}

// TestTaskTreeClosure: the closure follows parent_id; a missing or trashed
// parent and the smallest id of a cycle read as top level (6.13); a
// tombstone re-roots its children; Rebuild empties it.
func TestTaskTreeClosure(t *testing.T) {
	f := newFixture(t)
	c := f.open("c.db")
	a, b, cc, d, e, ch, x, y := newTsk(), newTsk(), newTsk(), newTsk(), newTsk(), newTsk(), newTsk(), newTsk()
	names := map[string]string{a.String(): "A", b.String(): "B", cc.String(): "C", d.String(): "D", e.String(): "E", ch.String(): "F", x.String(): "X", y.String(): "Y"}
	missing := newTsk().String()
	f.apply(c, "repo", "1",
		f.task(a, "", false), f.task(b, a.String(), false), f.task(cc, b.String(), false),
		f.task(d, missing, false), f.task(e, "", true), f.task(ch, e.String(), false),
		f.task(x, y.String(), false), f.task(y, x.String(), false))
	got := closure(t, c, names)
	want := []string{"A>A@0", "A>B@1", "A>C@2", "B>C@1", "D>D@0", "F>F@0"}
	// x was minted first, so it is the smaller id: the cycle's top
	want = append(want, "X>Y@1")
	for _, w := range want {
		if !got[w] {
			t.Fatalf("closure lacks %s: %v", w, got)
		}
	}
	if got["E>E@0"] || got["E>F@1"] || len(got) != 11 {
		t.Fatalf("closure = %v", got)
	}
	f.apply(c, "repo", "2", f.tomb(schema.TableTasks, b))
	got = closure(t, c, names)
	if got["A>C@2"] || !got["C>C@0"] || got["B>B@0"] {
		t.Fatalf("after tombstone: %v", got)
	}
	if err := c.Rebuild(f.ctx, "repo"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, c, `SELECT COUNT(*) FROM task_tree`); n != 0 {
		t.Fatalf("after Rebuild: %d rows", n)
	}
	// the lens reads it as task_tree_all
	f.apply(c, "repo", "3", f.task(a, "", false), f.task(b, a.String(), false))
	l := openLens(t, []LensSource{{Name: "repo", Path: f.dir + "/c.db"}})
	res := must(l.Query(f.ctx, `SELECT depth FROM task_tree_all WHERE ancestor_id = '`+a.String()+`' AND descendant_id = '`+b.String()+`'`))
	if len(res.Rows) != 1 || res.Rows[0][0].String() != "1" {
		t.Fatalf("lens task_tree_all = %v", res.Rows)
	}
}
