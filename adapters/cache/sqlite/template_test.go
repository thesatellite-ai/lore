package sqlite

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// fileShape is everything about a cache file that a fresh create decides:
// the layout version, the file-level pragmas, every schema object's SQL,
// the recorded table fingerprints, and how many rows each internal table
// holds.
type fileShape struct {
	pragmas  map[string]string
	objects  []string
	recorded []string
	counts   map[string]int
}

// shapeOf reads fileShape from an open cache.
func shapeOf(t *testing.T, c *Cache) fileShape {
	t.Helper()
	s := fileShape{pragmas: map[string]string{}, counts: map[string]int{}}
	for _, p := range []string{"user_version", "journal_mode", "page_size", "auto_vacuum", "encoding", "application_id", "synchronous", "busy_timeout"} {
		var v string
		if err := c.db.QueryRow("PRAGMA " + p).Scan(&v); err != nil {
			t.Fatalf("pragma %s: %v", p, err)
		}
		s.pragmas[p] = v
	}
	s.objects = oneColumn(t, c, `SELECT type || '|' || name || '|' || tbl_name || '|' || COALESCE(sql, '') FROM sqlite_master ORDER BY type, name`)
	s.recorded = oneColumn(t, c, `SELECT name || '|' || fingerprint || '|' || objects FROM "`+tblTables+`" ORDER BY name`)
	for _, tbl := range []string{tblRows, tblCursors, tblRefs, tblExpanded} {
		s.counts[tbl] = count(t, c, `SELECT COUNT(*) FROM "`+tbl+`"`)
	}
	return s
}

// oneColumn returns the one-column result of q.
func oneColumn(t *testing.T, c *Cache, q string) []string {
	t.Helper()
	rs, err := c.db.Query(q)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rs.Close() }()
	var out []string
	for rs.Next() {
		var v string
		if err := rs.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	if err := rs.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestTemplateMatchesFreshCreate: a cache seeded from the empty template
// (UseEmptyTemplates) is, once open, the same file as one created fresh:
// same pragmas and layout version, same schema objects with the same SQL,
// same recorded fingerprints, no rows, same file mode; and it works (a
// write and a read go through). A second copy of the same template is the
// same again, and an existing file is never replaced by a template.
func TestTemplateMatchesFreshCreate(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	fresh := f.open("fresh.db")

	copied := filepath.Join(f.dir, "copied.db")
	if err := copyTemplate(f.ctx, copied, f.reg, f.exp); err != nil {
		t.Fatal(err)
	}
	cp := f.open("copied.db")

	want, got := shapeOf(t, fresh), shapeOf(t, cp)
	if len(want.objects) == 0 || len(want.recorded) == 0 {
		t.Fatalf("fresh cache has no schema: %d objects, %d recorded tables", len(want.objects), len(want.recorded))
	}
	diff(t, "pragmas", want.pragmas, got.pragmas)
	diff(t, "objects", want.objects, got.objects)
	diff(t, "recorded tables", want.recorded, got.recorded)
	diff(t, "row counts", want.counts, got.counts)

	fi, err := os.Stat(filepath.Join(f.dir, "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	ci, err := os.Stat(copied)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode() != ci.Mode() {
		t.Errorf("file mode: fresh %v, copied %v", fi.Mode(), ci.Mode())
	}

	// The copy is a working cache, not just the same schema.
	m, prj := newMem(), newPrj()
	f.apply(cp, store.SourceRepo, "1", f.memory(m, prj, "after a template copy"))
	if n := count(t, cp, `SELECT COUNT(*) FROM "memories" WHERE id = ?`, m.String()); n != 1 {
		t.Fatalf("memories rows for the written id = %d, want 1", n)
	}

	// No temp file is left beside the copy.
	ents, err := os.ReadDir(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if bytes.Contains([]byte(e.Name()), []byte(".tmpl-")) {
			t.Errorf("template temp file left behind: %s", e.Name())
		}
	}

	// An existing file is opened as it is, never overwritten: the copy now
	// holds a row the template does not.
	if err := cp.Close(); err != nil {
		t.Fatal(err)
	}
	if err := copyTemplate(f.ctx, copied, f.reg, f.exp); err != nil {
		t.Fatal(err)
	}
	again := f.open("copied.db")
	if n := count(t, again, `SELECT COUNT(*) FROM "memories" WHERE id = ?`, m.String()); n != 1 {
		t.Fatalf("an existing cache lost its row to a template copy (rows = %d)", n)
	}
}

// TestTemplateKeyFollowsRegistry: two registries with different tables get
// different templates (a template is never reused for another schema),
// and the same registry always gets the same one.
func TestTemplateKeyFollowsRegistry(t *testing.T) {
	t.Parallel()
	full := newFixture(t).reg
	k1, err := templateKey(full)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := templateKey(full)
	if err != nil {
		t.Fatal(err)
	}
	if k1 != k2 {
		t.Fatalf("same registry, two keys: %s %s", k1, k2)
	}
	defs := schema.Builtin()
	fewer, err := schema.NewRegistry(defs[:len(defs)-1]...)
	if err != nil {
		t.Fatal(err)
	}
	k3, err := templateKey(fewer)
	if err != nil {
		t.Fatal(err)
	}
	if k3 == k1 {
		t.Fatal("a registry without a table got the full registry's template")
	}
}

// diff fails with both sides when want and got differ.
func diff[T any](t *testing.T, what string, want, got T) {
	t.Helper()
	w, g := fmt.Sprintf("%v", want), fmt.Sprintf("%v", got)
	if w != g {
		t.Errorf("%s differ between a fresh create and a template copy:\nfresh:  %s\ncopied: %s", what, w, g)
	}
}
